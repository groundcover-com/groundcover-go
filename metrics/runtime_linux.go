//go:build linux

package metrics

import (
	"io/fs"
	"os"
	"path"
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

// cgroupV2CPU parses cpu.max along the process's cgroup ancestry and returns
// the tightest quota.
func (s *runtimeSource) cgroupV2CPU() (float64, bool) {
	leaf, mount, ok := s.cgroupDir("", true)
	if !ok {
		leaf, mount = "sys/fs/cgroup", "sys/fs/cgroup"
	}
	var tightest float64
	for _, filename := range cgroupAncestorFiles(leaf, mount, "cpu.max") {
		data, err := fs.ReadFile(s.fsys, filename)
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) != 2 || fields[0] == "max" {
			continue
		}
		quota, err1 := strconv.ParseFloat(fields[0], 64)
		period, err2 := strconv.ParseFloat(fields[1], 64)
		if err1 == nil && err2 == nil && quota > 0 && period > 0 {
			cores := quota / period
			if tightest == 0 || cores < tightest {
				tightest = cores
			}
		}
	}
	return tightest, tightest > 0
}

// cgroupV1CPU parses the cgroup v1 cpu.cfs_quota_us / cpu.cfs_period_us pair
// (-1 quota means no limit).
func (s *runtimeSource) cgroupV1CPU() (float64, bool) {
	leaf, mount, ok := s.cgroupDir("cpu", false)
	if !ok {
		leaf, mount = "sys/fs/cgroup/cpu", "sys/fs/cgroup/cpu"
	}
	var tightest float64
	dirs := cgroupAncestorDirs(leaf, mount)
	for _, dir := range dirs {
		quota, ok1 := s.readNumericFile(path.Join(dir, "cpu.cfs_quota_us"))
		period, ok2 := s.readNumericFile(path.Join(dir, "cpu.cfs_period_us"))
		if ok1 && ok2 && quota > 0 && period > 0 {
			cores := quota / period
			if tightest == 0 || cores < tightest {
				tightest = cores
			}
		}
	}
	return tightest, tightest > 0
}

// memoryLimitBytes returns the memory available to the process: the cgroup
// v2 memory.max limit when set ("max" falls back to host MemTotal), else the
// cgroup v1 memory.limit_in_bytes when set, else host MemTotal. It reports
// false when nothing trustworthy is available.
func (s *runtimeSource) memoryLimitBytes() (float64, bool) {
	if leaf, mount, ok := s.cgroupDir("", true); ok {
		var tightest float64
		var sawMemoryMax bool
		for _, filename := range cgroupAncestorFiles(leaf, mount, "memory.max") {
			data, err := fs.ReadFile(s.fsys, filename)
			if err != nil {
				continue
			}
			sawMemoryMax = true
			text := strings.TrimSpace(string(data))
			if text == "max" {
				continue
			}
			if limit, err := strconv.ParseFloat(text, 64); err == nil && limit > 0 &&
				(tightest == 0 || limit < tightest) {
				tightest = limit
			}
		}
		if tightest > 0 {
			return tightest, true
		}
		if sawMemoryMax {
			return s.hostMemTotalBytes()
		}
		// Hybrid layouts expose a cgroup2 hierarchy but keep memory on v1.
	}
	if data, err := fs.ReadFile(s.fsys, "sys/fs/cgroup/memory.max"); err == nil {
		text := strings.TrimSpace(string(data))
		if limit, err := strconv.ParseFloat(text, 64); err == nil && limit > 0 {
			return limit, true
		}
		return s.hostMemTotalBytes()
	}
	leaf, mount, ok := s.cgroupDir("memory", false)
	if !ok {
		leaf, mount = "sys/fs/cgroup/memory", "sys/fs/cgroup/memory"
	}
	var tightest float64
	for _, filename := range cgroupAncestorFiles(leaf, mount, "memory.limit_in_bytes") {
		if limit, found := s.readNumericFile(filename); found &&
			limit > 0 && limit < float64(v1UnlimitedThreshold) &&
			(tightest == 0 || limit < tightest) {
			tightest = limit
		}
	}
	if tightest > 0 {
		return tightest, true
	}
	return s.hostMemTotalBytes()
}

// cgroupDir resolves the process's cgroup membership against the corresponding
// mount root from /proc/self/mountinfo.
func (s *runtimeSource) cgroupDir(controller string, v2 bool) (leaf, mount string, ok bool) {
	membership, found := s.cgroupMembership(controller, v2)
	if !found {
		return "", "", false
	}
	mountRoot, mountPoint, found := s.cgroupMount(controller, v2)
	if !found {
		if v2 {
			mountRoot, mountPoint = "/", "/sys/fs/cgroup"
		} else {
			mountRoot, mountPoint = "/", "/sys/fs/cgroup/"+controller
		}
	}
	member := path.Clean("/" + strings.TrimPrefix(membership, "/"))
	root := path.Clean("/" + strings.TrimPrefix(mountRoot, "/"))
	relative := strings.TrimPrefix(member, "/")
	if member == root {
		relative = ""
	} else if strings.HasPrefix(member, root+"/") {
		relative = strings.TrimPrefix(member, root+"/")
	}
	mount = strings.TrimPrefix(path.Clean(mountPoint), "/")
	leaf = path.Join(mount, relative)
	return leaf, mount, true
}

func (s *runtimeSource) cgroupMembership(controller string, v2 bool) (string, bool) {
	data, err := fs.ReadFile(s.fsys, "proc/self/cgroup")
	if err != nil {
		return "", false
	}
	for line := range strings.Lines(string(data)) {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if v2 && parts[0] == "0" && parts[1] == "" {
			return parts[2], true
		}
		if !v2 {
			for configured := range strings.SplitSeq(parts[1], ",") {
				if configured == controller {
					return parts[2], true
				}
			}
		}
	}
	return "", false
}

func (s *runtimeSource) cgroupMount(controller string, v2 bool) (root, mountPoint string, ok bool) {
	data, err := fs.ReadFile(s.fsys, "proc/self/mountinfo")
	if err != nil {
		return "", "", false
	}
	for line := range strings.Lines(string(data)) {
		left, right, found := strings.Cut(strings.TrimSpace(line), " - ")
		if !found {
			continue
		}
		leftFields, rightFields := strings.Fields(left), strings.Fields(right)
		if len(leftFields) < 5 || len(rightFields) < 3 {
			continue
		}
		if v2 && rightFields[0] == "cgroup2" {
			return leftFields[3], leftFields[4], true
		}
		if !v2 && rightFields[0] == "cgroup" {
			for option := range strings.SplitSeq(rightFields[2], ",") {
				if option == controller {
					return leftFields[3], leftFields[4], true
				}
			}
		}
	}
	return "", "", false
}

func cgroupAncestorFiles(leaf, mount, filename string) []string {
	dirs := cgroupAncestorDirs(leaf, mount)
	files := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		files = append(files, path.Join(dir, filename))
	}
	return files
}

func cgroupAncestorDirs(leaf, mount string) []string {
	leaf, mount = path.Clean(leaf), path.Clean(mount)
	dirs := []string{leaf}
	for leaf != mount {
		parent := path.Dir(leaf)
		if parent == leaf || (!strings.HasPrefix(parent+"/", mount+"/") && parent != mount) {
			break
		}
		dirs = append(dirs, parent)
		leaf = parent
	}
	return dirs
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
