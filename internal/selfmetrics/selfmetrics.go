// Package selfmetrics holds the SDK's self-observability counters. Every
// internal action increments an atomic counter so the SDK can report on its own
// behavior through Stats() and an optional Prometheus bridge without depending
// on any metrics library in the core.
package selfmetrics

import "sync/atomic"

// DropReason enumerates the reasons an event can be dropped.
type DropReason int

const (
	// DropOverflow indicates an event was evicted because the bounded buffer
	// exceeded its item or byte budget.
	DropOverflow DropReason = iota
	// DropSendExhausted indicates an event was dropped after delivery retries
	// were exhausted or the server returned a permanent error.
	DropSendExhausted
	// DropBeforeSend indicates an event was dropped by a BeforeSend callback.
	DropBeforeSend
)

// Snapshot is an immutable copy of all counters at a point in time. Field names
// mirror the exported Prometheus metric names.
type Snapshot struct {
	Captured           int64
	Sent               int64
	DroppedOverflow    int64
	DroppedSend        int64
	DroppedBeforeSend  int64
	Retries            int64
	RateLimited        int64
	PanicsRecovered    int64
	ConfigReloads      int64
	QueuePendingItems  int64
	QueuePendingBytes  int64
	SubsystemsDisabled int64
}

// Metrics is a set of atomic counters. The zero value is ready to use and safe
// for concurrent access.
type Metrics struct {
	captured           atomic.Int64
	sent               atomic.Int64
	droppedOverflow    atomic.Int64
	droppedSend        atomic.Int64
	droppedBeforeSend  atomic.Int64
	retries            atomic.Int64
	rateLimited        atomic.Int64
	panicsRecovered    atomic.Int64
	configReloads      atomic.Int64
	queuePendingItems  atomic.Int64
	queuePendingBytes  atomic.Int64
	subsystemsDisabled atomic.Int64
}

// New returns a fresh, zeroed metrics set.
func New() *Metrics { return &Metrics{} }

// IncCaptured records a successful capture (an event accepted into the pipeline).
func (m *Metrics) IncCaptured() { m.captured.Add(1) }

// AddSent records n successfully delivered events.
func (m *Metrics) AddSent(n int64) { m.sent.Add(n) }

// AddDropped records n dropped events for the given reason.
func (m *Metrics) AddDropped(reason DropReason, n int64) {
	switch reason {
	case DropOverflow:
		m.droppedOverflow.Add(n)
	case DropSendExhausted:
		m.droppedSend.Add(n)
	case DropBeforeSend:
		m.droppedBeforeSend.Add(n)
	}
}

// IncRetries records a delivery retry attempt.
func (m *Metrics) IncRetries() { m.retries.Add(1) }

// IncRateLimited records a 429 (rate limited) response from the server.
func (m *Metrics) IncRateLimited() { m.rateLimited.Add(1) }

// IncPanicsRecovered records a recovered SDK-internal panic.
func (m *Metrics) IncPanicsRecovered() { m.panicsRecovered.Add(1) }

// IncConfigReloads records an atomic configuration swap.
func (m *Metrics) IncConfigReloads() { m.configReloads.Add(1) }

// IncSubsystemsDisabled records a background subsystem self-disabling after a panic.
func (m *Metrics) IncSubsystemsDisabled() { m.subsystemsDisabled.Add(1) }

// SetQueuePending updates the pending-queue gauges (current items and bytes).
func (m *Metrics) SetQueuePending(items, bytes int64) {
	m.queuePendingItems.Store(items)
	m.queuePendingBytes.Store(bytes)
}

// Snapshot returns an immutable copy of all counters.
func (m *Metrics) Snapshot() Snapshot {
	return Snapshot{
		Captured:           m.captured.Load(),
		Sent:               m.sent.Load(),
		DroppedOverflow:    m.droppedOverflow.Load(),
		DroppedSend:        m.droppedSend.Load(),
		DroppedBeforeSend:  m.droppedBeforeSend.Load(),
		Retries:            m.retries.Load(),
		RateLimited:        m.rateLimited.Load(),
		PanicsRecovered:    m.panicsRecovered.Load(),
		ConfigReloads:      m.configReloads.Load(),
		QueuePendingItems:  m.queuePendingItems.Load(),
		QueuePendingBytes:  m.queuePendingBytes.Load(),
		SubsystemsDisabled: m.subsystemsDisabled.Load(),
	}
}

// Reset zeroes every counter. It exists for test isolation and must not be used
// on a live client.
func (m *Metrics) Reset() {
	m.captured.Store(0)
	m.sent.Store(0)
	m.droppedOverflow.Store(0)
	m.droppedSend.Store(0)
	m.droppedBeforeSend.Store(0)
	m.retries.Store(0)
	m.rateLimited.Store(0)
	m.panicsRecovered.Store(0)
	m.configReloads.Store(0)
	m.queuePendingItems.Store(0)
	m.queuePendingBytes.Store(0)
	m.subsystemsDisabled.Store(0)
}
