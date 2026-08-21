//go:build linux

package metrics

import (
	"io/fs"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// userHZ is the kernel clock tick rate used to scale /proc/self/stat CPU
// times. It is 100 on effectively every Linux configuration; reading the
// real value requires cgo or an exec, which the SDK avoids.
const userHZ = 100

// v1UnlimitedThreshold marks cgroup v1 memory.limit_in_bytes values that
// mean "no limit" (the kernel reports a page-rounded max int64).
const v1UnlimitedThreshold = int64(1) << 62

// rootFS returns the host filesystem the process metrics are read from.
func rootFS() fs.FS {
	return os.DirFS("/")
}

// writeProcessMetrics emits process and cgroup-aware metrics from /proc and
// /sys/fs/cgroup. Missing or malformed files cause individual metrics to
// fall back or be omitted; they never fail the push.
func (s *runtimeSource) writeProcessMetrics(b *strings.Builder) {
	if s.fsys == nil {
		return
	}
	if utime, stime, starttime, ok := s.readSelfStat(); ok {
		writeTypedSample(b, "process_cpu_seconds_total", typeCounter, (utime+stime)/userHZ)
		if btime, ok := s.readBootTime(); ok {
			writeTypedSample(b, "process_start_time_seconds", typeGauge, btime+starttime/userHZ)
		}
	}
	if rss, ok := s.readResidentBytes(); ok {
		writeTypedSample(b, "process_resident_memory_bytes", typeGauge, rss)
	}
	if fds, ok := s.readOpenFDs(); ok {
		writeTypedSample(b, "process_open_fds", typeGauge, fds)
	}
	writeTypedSample(b, "process_cpu_cores_available", typeGauge, s.cpuCoresAvailable())
	if limit, ok := s.memoryLimitBytes(); ok {
		writeTypedSample(b, "process_memory_limit_bytes", typeGauge, limit)
	}
}

// readSelfStat parses /proc/self/stat into utime, stime, and starttime, all
// in clock ticks.
func (s *runtimeSource) readSelfStat() (utime, stime, starttime float64, ok bool) {
	data, err := fs.ReadFile(s.fsys, "proc/self/stat")
	if err != nil {
		return 0, 0, 0, false
	}
	// The comm field (2) may contain spaces and parentheses; fields are
	// positional only after the last ')'.
	text := string(data)
	idx := strings.LastIndexByte(text, ')')
	if idx < 0 {
		return 0, 0, 0, false
	}
	fields := strings.Fields(text[idx+1:])
	// After ')' the slice starts at overall field 3 (state), so overall
	// field N is fields[N-3]: utime=14, stime=15, starttime=22.
	if len(fields) < 20 {
		return 0, 0, 0, false
	}
	utime, err1 := strconv.ParseFloat(fields[11], 64)
	stime, err2 := strconv.ParseFloat(fields[12], 64)
	starttime, err3 := strconv.ParseFloat(fields[19], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	return utime, stime, starttime, true
}

// readBootTime parses the btime line (seconds since epoch) from /proc/stat.
func (s *runtimeSource) readBootTime() (float64, bool) {
	data, err := fs.ReadFile(s.fsys, "proc/stat")
	if err != nil {
		return 0, false
	}
	for line := range strings.Lines(string(data)) {
		rest, found := strings.CutPrefix(line, "btime ")
		if !found {
			continue
		}
		btime, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			return 0, false
		}
		return btime, true
	}
	return 0, false
}

// readResidentBytes parses the resident set size from /proc/self/statm.
func (s *runtimeSource) readResidentBytes() (float64, bool) {
	data, err := fs.ReadFile(s.fsys, "proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return 0, false
	}
	return pages * float64(os.Getpagesize()), true
}

// readOpenFDs counts the entries of /proc/self/fd, best-effort.
func (s *runtimeSource) readOpenFDs() (float64, bool) {
	entries, err := fs.ReadDir(s.fsys, "proc/self/fd")
	if err != nil {
		return 0, false
	}
	return float64(len(entries)), true
}

// cpuCoresAvailable returns the CPU quota available to the process: the
// cgroup v2 cpu.max quota/period when set, else the cgroup v1
// cpu.cfs_quota_us/cpu.cfs_period_us quota when set, else GOMAXPROCS (which
// is itself cgroup-aware on modern Go).
func (s *runtimeSource) cpuCoresAvailable() float64 {
	if cores, ok := s.cgroupV2CPU(); ok {
		return cores
	}
	if cores, ok := s.cgroupV1CPU(); ok {
		return cores
	}
	return float64(runtime.GOMAXPROCS(0))
}

// cgroupV2CPU parses /sys/fs/cgroup/cpu.max ("<quota|max> <period>").
func (s *runtimeSource) cgroupV2CPU() (float64, bool) {
	data, err := fs.ReadFile(s.fsys, "sys/fs/cgroup/cpu.max")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false
	}
	quota, err1 := strconv.ParseFloat(fields[0], 64)
	period, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || quota <= 0 || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

// cgroupV1CPU parses the cgroup v1 cpu.cfs_quota_us / cpu.cfs_period_us pair
// (-1 quota means no limit).
func (s *runtimeSource) cgroupV1CPU() (float64, bool) {
	quota, ok1 := s.readNumericFile("sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	period, ok2 := s.readNumericFile("sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if !ok1 || !ok2 || quota <= 0 || period <= 0 {
		return 0, false
	}
	return quota / period, true
}

// memoryLimitBytes returns the memory available to the process: the cgroup
// v2 memory.max limit when set ("max" falls back to host MemTotal), else the
// cgroup v1 memory.limit_in_bytes when set, else host MemTotal. It reports
// false when nothing trustworthy is available.
func (s *runtimeSource) memoryLimitBytes() (float64, bool) {
	if data, err := fs.ReadFile(s.fsys, "sys/fs/cgroup/memory.max"); err == nil {
		text := strings.TrimSpace(string(data))
		if text == "max" {
			return s.hostMemTotalBytes()
		}
		if limit, err := strconv.ParseFloat(text, 64); err == nil && limit > 0 {
			return limit, true
		}
		return s.hostMemTotalBytes()
	}
	if limit, ok := s.readNumericFile("sys/fs/cgroup/memory/memory.limit_in_bytes"); ok {
		if limit > 0 && limit < float64(v1UnlimitedThreshold) {
			return limit, true
		}
		return s.hostMemTotalBytes()
	}
	return s.hostMemTotalBytes()
}

// hostMemTotalBytes parses MemTotal from /proc/meminfo (reported in kB).
func (s *runtimeSource) hostMemTotalBytes() (float64, bool) {
	data, err := fs.ReadFile(s.fsys, "proc/meminfo")
	if err != nil {
		return 0, false
	}
	for line := range strings.Lines(string(data)) {
		rest, found := strings.CutPrefix(line, "MemTotal:")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 1 {
			return 0, false
		}
		kb, err := strconv.ParseFloat(fields[0], 64)
		if err != nil || kb <= 0 {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

// readNumericFile reads a file containing a single number.
func (s *runtimeSource) readNumericFile(path string) (float64, bool) {
	data, err := fs.ReadFile(s.fsys, path)
	if err != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
