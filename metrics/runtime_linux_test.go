//go:build linux

package metrics

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

// procFixture is a complete, well-formed /proc + cgroup v2 fixture.
func procFixture() fstest.MapFS {
	return fstest.MapFS{
		// comm contains spaces and a parenthesis to exercise the parser.
		"proc/self/stat": {Data: []byte(
			"1234 (my (odd) proc) S 1 1234 1234 0 -1 4194560 1000 0 0 0 250 150 0 0 20 0 8 0 5000 1000000 2048 18446744073709551615 1 1 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0\n")},
		"proc/stat":                {Data: []byte("cpu  1 2 3 4\nbtime 1700000000\nprocesses 100\n")},
		"proc/self/statm":          {Data: []byte("4096 2048 100 10 0 500 0\n")},
		"proc/self/fd/0":           {Data: []byte("")},
		"proc/self/fd/1":           {Data: []byte("")},
		"proc/self/fd/2":           {Data: []byte("")},
		"proc/meminfo":             {Data: []byte("MemTotal:       16384000 kB\nMemFree:        1000 kB\n")},
		"sys/fs/cgroup/cpu.max":    {Data: []byte("200000 100000\n")},
		"sys/fs/cgroup/memory.max": {Data: []byte("1073741824\n")},
	}
}

func TestProcessMetricsFromFixtures(t *testing.T) {
	src := &runtimeSource{fsys: procFixture()}
	var b strings.Builder
	src.writeProcessMetrics(&b)
	body := b.String()

	// utime=250 + stime=150 ticks at USER_HZ=100 → 4 seconds.
	if !strings.Contains(body, "process_cpu_seconds_total 4\n") {
		t.Errorf("cpu seconds wrong:\n%s", body)
	}
	// btime 1700000000 + starttime 5000 ticks / 100 → 1700000050.
	if !strings.Contains(body, "process_start_time_seconds 1.70000005e+09\n") {
		t.Errorf("start time wrong:\n%s", body)
	}
	// statm RSS = 2048 pages.
	wantRSS := formatFloat(2048 * float64(os.Getpagesize()))
	if !strings.Contains(body, "process_resident_memory_bytes "+wantRSS+"\n") {
		t.Errorf("rss wrong (want %s):\n%s", wantRSS, body)
	}
	if !strings.Contains(body, "process_open_fds 3\n") {
		t.Errorf("open fds wrong:\n%s", body)
	}
	// cgroup v2: 200000/100000 = 2 cores.
	if !strings.Contains(body, "process_cpu_cores_available 2\n") {
		t.Errorf("cpu cores wrong:\n%s", body)
	}
	if !strings.Contains(body, "process_memory_limit_bytes 1.073741824e+09\n") {
		t.Errorf("memory limit wrong:\n%s", body)
	}
}

func TestCgroupV2CPUMaxFallsBack(t *testing.T) {
	fixture := procFixture()
	fixture["sys/fs/cgroup/cpu.max"] = &fstest.MapFile{Data: []byte("max 100000\n")}
	src := &runtimeSource{fsys: fixture}
	want := float64(runtime.GOMAXPROCS(0))
	if got := src.cpuCoresAvailable(); got != want {
		t.Errorf("cpuCoresAvailable = %v, want GOMAXPROCS fallback %v", got, want)
	}
}

func TestCgroupV2CPUMalformedFallsBackToV1(t *testing.T) {
	fixture := procFixture()
	fixture["sys/fs/cgroup/cpu.max"] = &fstest.MapFile{Data: []byte("garbage\n")}
	fixture["sys/fs/cgroup/cpu/cpu.cfs_quota_us"] = &fstest.MapFile{Data: []byte("150000\n")}
	fixture["sys/fs/cgroup/cpu/cpu.cfs_period_us"] = &fstest.MapFile{Data: []byte("100000\n")}
	src := &runtimeSource{fsys: fixture}
	if got := src.cpuCoresAvailable(); got != 1.5 {
		t.Errorf("cpuCoresAvailable = %v, want 1.5 from cgroup v1", got)
	}
}

func TestCgroupV1CPUUnlimitedQuota(t *testing.T) {
	fixture := procFixture()
	delete(fixture, "sys/fs/cgroup/cpu.max")
	fixture["sys/fs/cgroup/cpu/cpu.cfs_quota_us"] = &fstest.MapFile{Data: []byte("-1\n")}
	fixture["sys/fs/cgroup/cpu/cpu.cfs_period_us"] = &fstest.MapFile{Data: []byte("100000\n")}
	src := &runtimeSource{fsys: fixture}
	want := float64(runtime.GOMAXPROCS(0))
	if got := src.cpuCoresAvailable(); got != want {
		t.Errorf("cpuCoresAvailable = %v, want GOMAXPROCS fallback %v", got, want)
	}
}

func TestCgroupCPUMissingFilesFallBack(t *testing.T) {
	src := &runtimeSource{fsys: fstest.MapFS{}}
	want := float64(runtime.GOMAXPROCS(0))
	if got := src.cpuCoresAvailable(); got != want {
		t.Errorf("cpuCoresAvailable = %v, want GOMAXPROCS fallback %v", got, want)
	}
}

func TestMemoryLimitMaxUsesHostTotal(t *testing.T) {
	fixture := procFixture()
	fixture["sys/fs/cgroup/memory.max"] = &fstest.MapFile{Data: []byte("max\n")}
	src := &runtimeSource{fsys: fixture}
	limit, ok := src.memoryLimitBytes()
	if !ok || limit != 16384000*1024 {
		t.Errorf("memoryLimitBytes = %v, %v; want host MemTotal %v", limit, ok, 16384000*1024)
	}
}

func TestMemoryLimitMalformedUsesHostTotal(t *testing.T) {
	fixture := procFixture()
	fixture["sys/fs/cgroup/memory.max"] = &fstest.MapFile{Data: []byte("not-a-number\n")}
	src := &runtimeSource{fsys: fixture}
	limit, ok := src.memoryLimitBytes()
	if !ok || limit != 16384000*1024 {
		t.Errorf("memoryLimitBytes = %v, %v; want host MemTotal", limit, ok)
	}
}

func TestMemoryLimitV1(t *testing.T) {
	fixture := procFixture()
	delete(fixture, "sys/fs/cgroup/memory.max")
	fixture["sys/fs/cgroup/memory/memory.limit_in_bytes"] = &fstest.MapFile{Data: []byte("536870912\n")}
	src := &runtimeSource{fsys: fixture}
	limit, ok := src.memoryLimitBytes()
	if !ok || limit != 536870912 {
		t.Errorf("memoryLimitBytes = %v, %v; want 536870912", limit, ok)
	}
}

func TestMemoryLimitV1Unlimited(t *testing.T) {
	fixture := procFixture()
	delete(fixture, "sys/fs/cgroup/memory.max")
	fixture["sys/fs/cgroup/memory/memory.limit_in_bytes"] = &fstest.MapFile{Data: []byte("9223372036854771712\n")}
	src := &runtimeSource{fsys: fixture}
	limit, ok := src.memoryLimitBytes()
	if !ok || limit != 16384000*1024 {
		t.Errorf("memoryLimitBytes = %v, %v; want host MemTotal", limit, ok)
	}
}

func TestMemoryLimitNothingAvailableIsOmitted(t *testing.T) {
	src := &runtimeSource{fsys: fstest.MapFS{}}
	if limit, ok := src.memoryLimitBytes(); ok {
		t.Errorf("memoryLimitBytes = %v, want omitted", limit)
	}
	var b strings.Builder
	src.writeProcessMetrics(&b)
	if strings.Contains(b.String(), "process_memory_limit_bytes") {
		t.Errorf("memory limit emitted with no data:\n%s", b.String())
	}
	if strings.Contains(b.String(), "process_cpu_seconds_total") {
		t.Errorf("cpu seconds emitted with no /proc:\n%s", b.String())
	}
}

func TestNestedCgroupV2MembershipAndAncestorLimits(t *testing.T) {
	fixture := fstest.MapFS{
		"proc/self/cgroup": {Data: []byte("0::/kubepods/pod/container\n")},
		"proc/self/mountinfo": {Data: []byte(
			"29 23 0:26 /kubepods/pod /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw\n",
		)},
		"sys/fs/cgroup/container/cpu.max":    {Data: []byte("200000 100000\n")},
		"sys/fs/cgroup/cpu.max":              {Data: []byte("25000 100000\n")},
		"sys/fs/cgroup/container/memory.max": {Data: []byte("1073741824\n")},
		"sys/fs/cgroup/memory.max":           {Data: []byte("536870912\n")},
	}
	src := &runtimeSource{fsys: fixture}
	if got := src.cpuCoresAvailable(); got != 0.25 {
		t.Fatalf("cpuCoresAvailable = %v, want tightest ancestor quota 0.25", got)
	}
	if got, ok := src.memoryLimitBytes(); !ok || got != 536870912 {
		t.Fatalf("memoryLimitBytes = %v, %v; want tightest ancestor 536870912", got, ok)
	}
}

func TestMalformedProcFilesAreOmitted(t *testing.T) {
	src := &runtimeSource{fsys: fstest.MapFS{
		"proc/self/stat":  {Data: []byte("garbage without parens\n")},
		"proc/self/statm": {Data: []byte("only-one-field\n")},
	}}
	var b strings.Builder
	src.writeProcessMetrics(&b)
	for _, metric := range []string{"process_cpu_seconds_total", "process_start_time_seconds", "process_resident_memory_bytes", "process_open_fds"} {
		if strings.Contains(b.String(), metric) {
			t.Errorf("%s emitted from malformed files:\n%s", metric, b.String())
		}
	}
}

func TestRuntimeSourceOnRealHost(t *testing.T) {
	// Smoke test against the real /proc of the test process.
	src := newRuntimeSource()
	var b strings.Builder
	src.WritePrometheus(&b)
	body := b.String()
	for _, metric := range []string{
		"process_cpu_seconds_total", "process_resident_memory_bytes",
		"process_start_time_seconds", "process_open_fds",
		"process_cpu_cores_available",
	} {
		if !strings.Contains(body, metric+" ") {
			t.Errorf("%s missing on a real Linux host", metric)
		}
	}
}
