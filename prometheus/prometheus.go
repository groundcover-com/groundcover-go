// Package prometheus bridges a groundcover Client's self-metrics to a
// Prometheus exposition endpoint using the VictoriaMetrics metrics library. It
// is a separate module so the core SDK stays dependency-free.
package prometheus

import (
	"io"

	"github.com/VictoriaMetrics/metrics"

	groundcover "github.com/groundcover-com/groundcover-go"
)

// statsSource is the subset of *groundcover.Client this collector needs.
type statsSource interface {
	Stats() groundcover.Stats
}

// Collector exposes a client's Stats() as Prometheus metrics. Counters are
// reported with their cumulative values; gauges reflect the live queue depth.
type Collector struct {
	set    *metrics.Set
	source statsSource
}

// NewCollector builds a Collector backed by the given client and registers all
// SDK metrics on a private metrics.Set.
func NewCollector(client *groundcover.Client) *Collector {
	return newCollector(client)
}

func newCollector(source statsSource) *Collector {
	c := &Collector{set: metrics.NewSet(), source: source}
	c.register()
	return c
}

func (c *Collector) register() {
	g := func(name string, f func(groundcover.Stats) int64) {
		c.set.NewGauge(name, func() float64 { return float64(f(c.source.Stats())) })
	}
	g(`groundcover_sdk_captured_total`, func(s groundcover.Stats) int64 { return s.Captured })
	g(`groundcover_sdk_sent_total`, func(s groundcover.Stats) int64 { return s.Sent })
	g(`groundcover_sdk_dropped_total{reason="overflow"}`, func(s groundcover.Stats) int64 { return s.DroppedOverflow })
	g(`groundcover_sdk_dropped_total{reason="send_exhausted"}`, func(s groundcover.Stats) int64 { return s.DroppedSendExhausted })
	g(`groundcover_sdk_dropped_total{reason="before_send"}`, func(s groundcover.Stats) int64 { return s.DroppedBeforeSend })
	g(`groundcover_sdk_retries_total`, func(s groundcover.Stats) int64 { return s.Retries })
	g(`groundcover_sdk_rate_limited_total`, func(s groundcover.Stats) int64 { return s.RateLimited })
	g(`groundcover_sdk_panics_recovered_total`, func(s groundcover.Stats) int64 { return s.PanicsRecovered })
	g(`groundcover_sdk_config_reloads_total`, func(s groundcover.Stats) int64 { return s.ConfigReloads })
	g(`groundcover_sdk_subsystems_disabled_total`, func(s groundcover.Stats) int64 { return s.SubsystemsDisabled })
	g(`groundcover_sdk_queue_pending{unit="items"}`, func(s groundcover.Stats) int64 { return s.QueuePendingItems })
	g(`groundcover_sdk_queue_pending{unit="bytes"}`, func(s groundcover.Stats) int64 { return s.QueuePendingBytes })
}

// Set returns the underlying metrics set, for callers that compose multiple sets.
func (c *Collector) Set() *metrics.Set { return c.set }

// WritePrometheus writes the SDK metrics in Prometheus text exposition format.
func (c *Collector) WritePrometheus(w io.Writer) {
	c.set.WritePrometheus(w)
}
