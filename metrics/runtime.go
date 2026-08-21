package metrics

import (
	"io"
	"io/fs"
	"runtime"
	"strings"
)

// runtimeSource is the built-in Exposition collected on every push unless
// Config.DisableRuntimeMetrics is set. It emits standard Go runtime metrics
// on every platform and, on Linux, process and cgroup-aware metrics read
// from /proc and /sys/fs/cgroup.
//
// go_gc_duration_seconds is approximated from runtime.MemStats: only the
// summary _sum and _count series are emitted, without quantiles.
type runtimeSource struct {
	// fsys is the filesystem the process metrics are read from, rooted at
	// "/". It is injectable so tests can provide fixtures; nil disables the
	// process metrics.
	fsys fs.FS
}

// newRuntimeSource returns the built-in runtime metrics source reading from
// the host filesystem.
func newRuntimeSource() *runtimeSource {
	return &runtimeSource{fsys: rootFS()}
}

// WritePrometheus implements Exposition.
func (s *runtimeSource) WritePrometheus(w io.Writer) {
	var b strings.Builder
	writeGoMetrics(&b)
	s.writeProcessMetrics(&b)
	_, _ = io.WriteString(w, b.String())
}

// writeGoMetrics emits the platform-independent Go runtime metrics.
func writeGoMetrics(b *strings.Builder) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	threads, _ := runtime.ThreadCreateProfile(nil)

	writeTypedSample(b, "go_goroutines", typeGauge, float64(runtime.NumGoroutine()))
	writeTypedSample(b, "go_threads", typeGauge, float64(threads))

	b.WriteString("# TYPE go_info gauge\n")
	writeSample(b, "go_info", []label{{name: "version", value: runtime.Version()}}, 1)

	writeTypedSample(b, "go_memstats_alloc_bytes", typeGauge, float64(ms.Alloc))
	writeTypedSample(b, "go_memstats_heap_alloc_bytes", typeGauge, float64(ms.HeapAlloc))
	writeTypedSample(b, "go_memstats_heap_inuse_bytes", typeGauge, float64(ms.HeapInuse))
	writeTypedSample(b, "go_memstats_heap_idle_bytes", typeGauge, float64(ms.HeapIdle))
	writeTypedSample(b, "go_memstats_heap_objects", typeGauge, float64(ms.HeapObjects))
	writeTypedSample(b, "go_memstats_stack_inuse_bytes", typeGauge, float64(ms.StackInuse))
	writeTypedSample(b, "go_memstats_sys_bytes", typeGauge, float64(ms.Sys))
	writeTypedSample(b, "go_memstats_mallocs_total", typeCounter, float64(ms.Mallocs))
	writeTypedSample(b, "go_memstats_frees_total", typeCounter, float64(ms.Frees))
	writeTypedSample(b, "go_memstats_gc_sys_bytes", typeGauge, float64(ms.GCSys))
	writeTypedSample(b, "go_memstats_next_gc_bytes", typeGauge, float64(ms.NextGC))
	writeTypedSample(b, "go_memstats_last_gc_time_seconds", typeGauge, float64(ms.LastGC)/1e9)

	// Approximated summary: quantiles are not tracked, only sum and count.
	b.WriteString("# TYPE go_gc_duration_seconds summary\n")
	writeSample(b, "go_gc_duration_seconds_sum", nil, float64(ms.PauseTotalNs)/1e9)
	writeSample(b, "go_gc_duration_seconds_count", nil, float64(ms.NumGC))

	writeTypedSample(b, "go_sched_gomaxprocs_threads", typeGauge, float64(runtime.GOMAXPROCS(0)))
}

// writeTypedSample emits a # TYPE line followed by one unlabeled sample.
func writeTypedSample(b *strings.Builder, name string, typ metricType, value float64) {
	b.WriteString("# TYPE ")
	b.WriteString(name)
	b.WriteByte(' ')
	b.WriteString(string(typ))
	b.WriteByte('\n')
	writeSample(b, name, nil, value)
}
