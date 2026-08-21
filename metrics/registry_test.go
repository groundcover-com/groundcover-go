package metrics

import (
	"strings"
	"sync"
	"testing"
)

// render is a test helper returning the registry's exposition text.
func render(r *Registry) string {
	var b strings.Builder
	r.WritePrometheus(&b)
	return b.String()
}

func TestRegistryGetOrCreateIdentity(t *testing.T) {
	r := NewRegistry()
	g1 := r.Gauge("g", map[string]string{"a": "1", "b": "2"})
	g2 := r.Gauge("g", map[string]string{"b": "2", "a": "1"})
	if g1 != g2 {
		t.Error("same (name, labels) returned different gauges")
	}
	c1 := r.Counter("c", nil)
	c2 := r.Counter("c", map[string]string{})
	if c1 != c2 {
		t.Error("nil and empty label maps returned different counters")
	}
	h1 := r.Histogram("h", nil, []float64{1, 2})
	h2 := r.Histogram("h", nil, []float64{5, 10}) // existing buckets kept
	if h1 != h2 {
		t.Error("same histogram series returned different instruments")
	}
	if g3 := r.Gauge("g", map[string]string{"a": "other"}); g3 == g1 {
		t.Error("different labels returned the same gauge")
	}
	if err := r.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
}

func TestRegistryDeterministicRendering(t *testing.T) {
	r := NewRegistry()
	r.Gauge("zeta", map[string]string{"x": "1"}).Set(1)
	r.Counter("alpha_total", nil).Add(2)
	r.Gauge("zeta", map[string]string{"a": "0"}).Set(3)
	r.Gauge("mid", nil).Set(-4.5)

	want := strings.Join([]string{
		"# TYPE alpha_total counter",
		"alpha_total 2",
		"# TYPE mid gauge",
		"mid -4.5",
		"# TYPE zeta gauge",
		`zeta{a="0"} 3`,
		`zeta{x="1"} 1`,
	}, "\n") + "\n"
	first := render(r)
	if first != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", first, want)
	}
	if second := render(r); second != first {
		t.Error("rendering is not deterministic across calls")
	}
}

func TestRegistryGaugeAndCounterSemantics(t *testing.T) {
	r := NewRegistry()
	g := r.Gauge("g", nil)
	g.Set(10)
	g.Add(-2.5)
	if got := render(r); !strings.Contains(got, "g 7.5\n") {
		t.Errorf("gauge value wrong:\n%s", got)
	}

	c := r.Counter("c_total", nil)
	c.Inc()
	c.Add(2)
	c.Add(-100) // ignored: counters never decrease
	if got := render(r); !strings.Contains(got, "c_total 3\n") {
		t.Errorf("counter value wrong:\n%s", got)
	}
}

func TestRegistryHistogramCorrectness(t *testing.T) {
	r := NewRegistry()
	h := r.Histogram("lat", map[string]string{"svc": "a"}, []float64{1, 2, 5})
	h.Observe(0.5)
	h.Observe(1.5)
	h.Observe(10)

	want := strings.Join([]string{
		"# TYPE lat histogram",
		`lat_bucket{le="1",svc="a"} 1`,
		`lat_bucket{le="2",svc="a"} 2`,
		`lat_bucket{le="5",svc="a"} 2`,
		`lat_bucket{le="+Inf",svc="a"} 3`,
		`lat_sum{svc="a"} 12`,
		`lat_count{svc="a"} 3`,
	}, "\n") + "\n"
	if got := render(r); got != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, want)
	}
}

func TestRegistryHistogramBucketSanitization(t *testing.T) {
	r := NewRegistry()
	h := r.Histogram("h", nil, []float64{5, 1, 5, 2})
	h.Observe(1.5)
	got := render(r)
	idx1 := strings.Index(got, `le="1"`)
	idx2 := strings.Index(got, `le="2"`)
	idx5 := strings.Index(got, `le="5"`)
	if idx1 < 0 || idx2 < 0 || idx5 < 0 || idx1 >= idx2 || idx2 >= idx5 {
		t.Errorf("buckets not sorted/deduped:\n%s", got)
	}
	if strings.Count(got, `le="5"`) != 1 {
		t.Errorf("duplicate bucket not removed:\n%s", got)
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := NewRegistry()
	r.Gauge("g", map[string]string{"a": "1"}).Set(1)
	if !r.Unregister("g", map[string]string{"a": "1"}) {
		t.Error("Unregister = false, want true")
	}
	if r.Unregister("g", map[string]string{"a": "1"}) {
		t.Error("second Unregister = true, want false")
	}
	if r.Unregister("missing", nil) {
		t.Error("Unregister(missing) = true, want false")
	}
	if got := render(r); got != "" {
		t.Errorf("output after unregister:\n%s", got)
	}
}

func TestRegistryInvalidNamesNeverPanic(t *testing.T) {
	r := NewRegistry()
	g := r.Gauge("bad-name", nil)
	if g == nil {
		t.Fatal("invalid name returned nil gauge")
	}
	g.Set(1) // must not panic
	g.Add(1)

	c := r.Counter("ok_total", map[string]string{"bad-label": "x"})
	c.Inc()
	h := r.Histogram("h", map[string]string{"le": "reserved"}, nil)
	h.Observe(1)
	r.Gauge("g", map[string]string{"__reserved": "x"}).Set(1)

	if got := render(r); got != "" {
		t.Errorf("invalid registrations rendered output:\n%s", got)
	}
	err := r.Err()
	if err == nil {
		t.Fatal("Err() = nil, want registration errors")
	}
	if !strings.Contains(err.Error(), "4 registration error(s)") {
		t.Errorf("Err() = %v, want 4 recorded errors", err)
	}
}

func TestRegistryTypeConflict(t *testing.T) {
	r := NewRegistry()
	r.Counter("m", nil).Inc()
	g := r.Gauge("m", nil)
	g.Set(99) // detached no-op
	if err := r.Err(); err == nil {
		t.Error("Err() = nil, want type-conflict error")
	}
	if got := render(r); !strings.Contains(got, "# TYPE m counter\nm 1\n") {
		t.Errorf("original counter corrupted:\n%s", got)
	}
}

func TestRegistryLabelValueEscaping(t *testing.T) {
	r := NewRegistry()
	r.Gauge("g", map[string]string{"msg": "a \"b\" \\ c\nd"}).Set(1)
	want := `g{msg="a \"b\" \\ c\nd"} 1` + "\n"
	if got := render(r); !strings.Contains(got, want) {
		t.Errorf("escaping wrong:\n%s\nwant contains:\n%s", got, want)
	}
}

func TestRegistryConcurrency(t *testing.T) {
	r := NewRegistry()
	stopRender := make(chan struct{})
	var renderWG sync.WaitGroup
	renderWG.Add(1)
	go func() {
		defer renderWG.Done()
		for {
			select {
			case <-stopRender:
				return
			default:
				_ = render(r)
			}
		}
	}()

	const workers = 8
	const iterations = 500
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range iterations {
				r.Gauge("g", map[string]string{"w": string(rune('a' + w))}).Add(1)
				r.Counter("c_total", nil).Inc()
				r.Histogram("h", nil, []float64{1, 10}).Observe(float64(i))
				if i%100 == 0 {
					r.Unregister("g", map[string]string{"w": string(rune('a' + w))})
				}
			}
		}()
	}
	wg.Wait()
	close(stopRender)
	renderWG.Wait()

	if got := render(r); !strings.Contains(got, "c_total 4000\n") {
		t.Errorf("counter lost updates:\n%s", got)
	}
	if got := render(r); !strings.Contains(got, "h_count 4000\n") {
		t.Errorf("histogram lost observations:\n%s", got)
	}
}
