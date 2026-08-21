package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// metricType is the exposition type of a metric family.
type metricType string

const (
	typeGauge     metricType = "gauge"
	typeCounter   metricType = "counter"
	typeHistogram metricType = "histogram"
)

// Registry is a thread-safe collection of gauges, counters, and histograms
// that renders itself in Prometheus text exposition format. It implements
// MetricSource. The zero value is not usable; construct with NewRegistry.
//
// Instrument lookups are idempotent: the same (name, labels) tuple always
// returns the same instrument. Invalid metric or label names never panic;
// they return a detached no-op instrument and record an error retrievable
// via Err.
type Registry struct {
	mu       sync.RWMutex
	families map[string]*family
	errCount int
	firstErr error
}

// family groups all series sharing one metric name.
type family struct {
	typ    metricType
	series map[string]*seriesEntry
}

// seriesEntry is one (name, labels) series and its instrument.
type seriesEntry struct {
	labels    []label
	gauge     *Gauge
	counter   *Counter
	histogram *Histogram
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{families: make(map[string]*family)}
}

// Gauge returns the gauge for the given name and labels, creating it on
// first use. An invalid name or label set returns a detached no-op gauge and
// records an error retrievable via Err.
func (r *Registry) Gauge(name string, labels map[string]string) *Gauge {
	entry := r.getOrCreate(name, labels, typeGauge, nil)
	if entry == nil {
		return &Gauge{}
	}
	return entry.gauge
}

// Counter returns the counter for the given name and labels, creating it on
// first use. An invalid name or label set returns a detached no-op counter
// and records an error retrievable via Err.
func (r *Registry) Counter(name string, labels map[string]string) *Counter {
	entry := r.getOrCreate(name, labels, typeCounter, nil)
	if entry == nil {
		return &Counter{}
	}
	return entry.counter
}

// Histogram returns the histogram for the given name, labels, and bucket
// upper bounds, creating it on first use. Nil or empty buckets use the
// default Prometheus buckets; buckets are sorted and deduplicated, and
// non-finite bounds are dropped (+Inf is implicit). When the series already
// exists its original buckets are kept. An invalid name or label set returns
// a detached no-op histogram and records an error retrievable via Err.
func (r *Registry) Histogram(name string, labels map[string]string, buckets []float64) *Histogram {
	entry := r.getOrCreate(name, labels, typeHistogram, buckets)
	if entry == nil {
		return newHistogram(buckets)
	}
	return entry.histogram
}

// Unregister removes the series with the given name and labels. It reports
// whether a series was removed.
func (r *Registry) Unregister(name string, labels map[string]string) bool {
	sig := labelSignature(sortedLabels(labels))
	r.mu.Lock()
	defer r.mu.Unlock()
	fam, ok := r.families[name]
	if !ok {
		return false
	}
	if _, ok := fam.series[sig]; !ok {
		return false
	}
	delete(fam.series, sig)
	if len(fam.series) == 0 {
		delete(r.families, name)
	}
	return true
}

// Err reports registration errors (invalid metric or label names, or a name
// reused with a conflicting type). It returns nil when every registration
// succeeded, otherwise an error describing the count and the first failure.
func (r *Registry) Err() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.errCount == 0 {
		return nil
	}
	return fmt.Errorf("metrics: %d registration error(s); first: %w", r.errCount, r.firstErr)
}

// WritePrometheus renders every series in Prometheus text format with
// deterministic ordering: metric names sorted, label names sorted, series
// within a family sorted by label signature. It implements MetricSource.
func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.RLock()
	names := make([]string, 0, len(r.families))
	for name := range r.families {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		fam := r.families[name]
		fmt.Fprintf(&b, "# TYPE %s %s\n", name, fam.typ)
		sigs := make([]string, 0, len(fam.series))
		for sig := range fam.series {
			sigs = append(sigs, sig)
		}
		sort.Strings(sigs)
		for _, sig := range sigs {
			writeSeries(&b, name, fam.series[sig])
		}
	}
	r.mu.RUnlock()
	_, _ = io.WriteString(w, b.String())
}

// getOrCreate returns the series entry for (name, labels), creating it with
// the given type on first use. It returns nil (and records an error) when
// the name or labels are invalid or the name is already registered with a
// different type.
func (r *Registry) getOrCreate(name string, labels map[string]string, typ metricType, buckets []float64) *seriesEntry {
	if r == nil {
		return nil
	}
	if err := validateSeries(name, labels, typ); err != nil {
		r.recordErr(err)
		return nil
	}
	sorted := sortedLabels(labels)
	sig := labelSignature(sorted)

	r.mu.Lock()
	defer r.mu.Unlock()
	fam, ok := r.families[name]
	if !ok {
		if err := histogramNameConflict(r.families, name, typ); err != nil {
			r.recordErrLocked(err)
			return nil
		}
		fam = &family{typ: typ, series: make(map[string]*seriesEntry)}
		r.families[name] = fam
	}
	if fam.typ != typ {
		r.recordErrLocked(fmt.Errorf("metrics: %q already registered as %s, requested %s", name, fam.typ, typ))
		return nil
	}
	entry, ok := fam.series[sig]
	if !ok {
		entry = &seriesEntry{labels: sorted}
		switch typ {
		case typeGauge:
			entry.gauge = &Gauge{}
		case typeCounter:
			entry.counter = &Counter{}
		case typeHistogram:
			entry.histogram = newHistogram(buckets)
		}
		fam.series[sig] = entry
	}
	return entry
}

func histogramNameConflict(families map[string]*family, name string, typ metricType) error {
	const (
		bucketSuffix = "_bucket"
		sumSuffix    = "_sum"
		countSuffix  = "_count"
	)
	suffixes := [...]string{bucketSuffix, sumSuffix, countSuffix}
	if typ == typeHistogram {
		for _, suffix := range suffixes {
			if _, exists := families[name+suffix]; exists {
				return fmt.Errorf("metrics: histogram %q conflicts with registered metric %q", name, name+suffix)
			}
		}
	}
	for existing, fam := range families {
		if fam.typ != typeHistogram {
			continue
		}
		for _, suffix := range suffixes {
			if name == existing+suffix {
				return fmt.Errorf("metrics: metric %q conflicts with histogram %q", name, existing)
			}
		}
	}
	return nil
}

// recordErr records a registration error under the lock.
func (r *Registry) recordErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recordErrLocked(err)
}

// recordErrLocked records a registration error; the caller holds mu.
func (r *Registry) recordErrLocked(err error) {
	r.errCount++
	if r.firstErr == nil {
		r.firstErr = err
	}
}

// validateSeries checks the metric name and label names for one series.
func validateSeries(name string, labels map[string]string, typ metricType) error {
	if !isValidMetricName(name) {
		return fmt.Errorf("metrics: invalid metric name %q", name)
	}
	for labelName := range labels {
		if !isValidLabelName(labelName) {
			return fmt.Errorf("metrics: invalid label name %q on %q", labelName, name)
		}
		if strings.HasPrefix(labelName, "__") {
			return fmt.Errorf("metrics: label name %q on %q is reserved", labelName, name)
		}
		if typ == typeHistogram && labelName == "le" {
			return fmt.Errorf("metrics: label name \"le\" on histogram %q is reserved", name)
		}
	}
	return nil
}

// sortedLabels converts a label map into a slice sorted by name.
func sortedLabels(labels map[string]string) []label {
	if len(labels) == 0 {
		return nil
	}
	sorted := make([]label, 0, len(labels))
	for name, value := range labels {
		sorted = append(sorted, label{name: name, value: value})
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].name < sorted[j].name })
	return sorted
}

// labelSignature builds the map key identifying one sorted label set.
func labelSignature(sorted []label) string {
	var b strings.Builder
	for _, l := range sorted {
		b.WriteString(l.name)
		b.WriteByte('\x01')
		b.WriteString(l.value)
		b.WriteByte('\x02')
	}
	return b.String()
}

// writeSeries renders one series' sample lines.
func writeSeries(b *strings.Builder, name string, entry *seriesEntry) {
	switch {
	case entry.gauge != nil:
		writeSample(b, name, entry.labels, entry.gauge.value())
	case entry.counter != nil:
		writeSample(b, name, entry.labels, entry.counter.value())
	case entry.histogram != nil:
		entry.histogram.write(b, name, entry.labels)
	}
}

// writeSample renders one "name{labels} value" line.
func writeSample(b *strings.Builder, name string, labels []label, value float64) {
	b.WriteString(name)
	writeLabels(b, labels)
	b.WriteByte(' ')
	b.WriteString(formatFloat(value))
	b.WriteByte('\n')
}

// positiveInf is the exposition spelling of positive infinity, used both as
// a sample value and as the implicit last histogram bucket bound.
const positiveInf = "+Inf"

// formatFloat renders a sample value in the shortest exact representation.
func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return positiveInf
	case math.IsInf(v, -1):
		return "-Inf"
	default:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
}

// Gauge is a value that can go up and down. It is safe for concurrent use.
// Obtain instances from Registry.Gauge; the zero value is a detached no-op.
type Gauge struct {
	bits atomic.Uint64
}

// Set replaces the gauge value.
func (g *Gauge) Set(v float64) {
	if g == nil {
		return
	}
	g.bits.Store(math.Float64bits(v))
}

// Add increments the gauge by delta, which may be negative.
func (g *Gauge) Add(delta float64) {
	if g == nil {
		return
	}
	for {
		old := g.bits.Load()
		updated := math.Float64bits(math.Float64frombits(old) + delta)
		if g.bits.CompareAndSwap(old, updated) {
			return
		}
	}
}

// value returns the current gauge value.
func (g *Gauge) value() float64 {
	return math.Float64frombits(g.bits.Load())
}

// Counter is a monotonically increasing value. It is safe for concurrent
// use. Obtain instances from Registry.Counter; the zero value is a detached
// no-op.
type Counter struct {
	bits atomic.Uint64
}

// Inc increments the counter by one.
func (c *Counter) Inc() {
	c.Add(1)
}

// Add increments the counter by delta. Negative or NaN deltas are ignored so
// the counter never decreases.
func (c *Counter) Add(delta float64) {
	if c == nil || delta < 0 || math.IsNaN(delta) {
		return
	}
	for {
		old := c.bits.Load()
		updated := math.Float64bits(math.Float64frombits(old) + delta)
		if c.bits.CompareAndSwap(old, updated) {
			return
		}
	}
}

// value returns the current counter value.
func (c *Counter) value() float64 {
	return math.Float64frombits(c.bits.Load())
}

// Histogram counts observations into cumulative buckets. It is safe for
// concurrent use. Obtain instances from Registry.Histogram; the zero value
// is a detached no-op.
type Histogram struct {
	mu          sync.Mutex
	upperBounds []float64
	counts      []uint64
	sum         float64
	count       uint64
}

// defaultBuckets returns the default Prometheus histogram buckets.
func defaultBuckets() []float64 {
	return []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
}

// newHistogram builds a histogram with sanitized bucket bounds.
func newHistogram(buckets []float64) *Histogram {
	if len(buckets) == 0 {
		buckets = defaultBuckets()
	}
	bounds := make([]float64, 0, len(buckets))
	for _, b := range buckets {
		if math.IsNaN(b) || math.IsInf(b, 0) {
			continue
		}
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)
	deduped := bounds[:0]
	for i, b := range bounds {
		if i == 0 || b != bounds[i-1] {
			deduped = append(deduped, b)
		}
	}
	return &Histogram{upperBounds: deduped, counts: make([]uint64, len(deduped))}
}

// Observe records one observation. NaN observations are ignored.
func (h *Histogram) Observe(v float64) {
	if h == nil || math.IsNaN(v) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, bound := range h.upperBounds {
		if v <= bound {
			h.counts[i]++
			break
		}
	}
	h.sum += v
	h.count++
}

// write renders the histogram's _bucket, _sum, and _count series with
// cumulative bucket counts and the implicit +Inf bucket.
func (h *Histogram) write(b *strings.Builder, name string, labels []label) {
	h.mu.Lock()
	bounds := h.upperBounds
	counts := make([]uint64, len(h.counts))
	copy(counts, h.counts)
	sum := h.sum
	count := h.count
	h.mu.Unlock()

	cumulative := uint64(0)
	for i, bound := range bounds {
		cumulative += counts[i]
		bucketLabels := append(append(make([]label, 0, len(labels)+1), labels...), label{name: "le", value: formatFloat(bound)})
		sort.Slice(bucketLabels, func(i, j int) bool { return bucketLabels[i].name < bucketLabels[j].name })
		writeSample(b, name+"_bucket", bucketLabels, float64(cumulative))
	}
	infLabels := append(append(make([]label, 0, len(labels)+1), labels...), label{name: "le", value: positiveInf})
	sort.Slice(infLabels, func(i, j int) bool { return infLabels[i].name < infLabels[j].name })
	writeSample(b, name+"_bucket", infLabels, float64(count))
	writeSample(b, name+"_sum", labels, sum)
	writeSample(b, name+"_count", labels, float64(count))
}
