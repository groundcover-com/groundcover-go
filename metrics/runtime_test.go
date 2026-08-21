package metrics

import (
	"context"
	"strings"
	"testing"
)

func TestRuntimeMetricsEnabledByDefault(t *testing.T) {
	cs := newCaptureServer(t)
	cfg := testConfig(cs)
	cfg.DisableRuntimeMetrics = false
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	body := cs.last().body

	for _, metric := range []string{
		"go_goroutines", "go_threads", "go_info",
		"go_memstats_alloc_bytes", "go_memstats_heap_alloc_bytes",
		"go_memstats_heap_inuse_bytes", "go_memstats_heap_idle_bytes",
		"go_memstats_heap_objects", "go_memstats_stack_inuse_bytes",
		"go_memstats_sys_bytes", "go_memstats_mallocs_total",
		"go_memstats_frees_total", "go_memstats_gc_sys_bytes",
		"go_memstats_next_gc_bytes", "go_memstats_last_gc_time_seconds",
		"go_gc_duration_seconds_sum", "go_gc_duration_seconds_count",
		"go_sched_gomaxprocs_threads",
	} {
		if !strings.Contains(body, metric+"{") {
			t.Errorf("runtime metric %s missing (or missing fixed labels):\n%s", metric, truncate(body, 2000))
		}
	}
	// Fixed labels are injected on runtime metrics like everything else.
	if !strings.Contains(body, `go_goroutines{instance="test-instance",job="test-job"} `) {
		t.Errorf("go_goroutines missing fixed labels:\n%s", truncate(body, 2000))
	}
	// The go_info version label survives injection.
	if !strings.Contains(body, `go_info{instance="test-instance",job="test-job",version="go`) {
		t.Errorf("go_info version label wrong:\n%s", truncate(body, 2000))
	}
}

func TestRuntimeMetricsDisabled(t *testing.T) {
	cs := newCaptureServer(t)
	reg := NewRegistry()
	reg.Gauge("only_metric", nil).Set(1)
	client, err := New(testConfig(cs), reg) // testConfig disables runtime metrics
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := client.Push(context.Background()); err != nil {
		t.Fatalf("Push: %v", err)
	}
	body := cs.last().body
	if strings.Contains(body, "go_goroutines") {
		t.Errorf("runtime metrics present despite DisableRuntimeMetrics:\n%s", body)
	}
	if !strings.Contains(body, "only_metric") {
		t.Errorf("user metric missing:\n%s", body)
	}
}

// truncate bounds test failure output.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
