package groundcover

import "github.com/groundcover-com/groundcover-go/internal/selfmetrics"

// Stats is a point-in-time snapshot of the SDK's self-observability counters.
// Field names mirror the exported Prometheus metric names.
type Stats struct {
	// Captured is the number of events accepted into the pipeline.
	Captured int64
	// Sent is the number of events successfully delivered.
	Sent int64
	// DroppedOverflow is the number of events evicted by buffer overflow.
	DroppedOverflow int64
	// DroppedSendExhausted is the number of events dropped after delivery failed.
	DroppedSendExhausted int64
	// DroppedBeforeSend is the number of events dropped by BeforeSend.
	DroppedBeforeSend int64
	// Retries is the number of delivery retry attempts.
	Retries int64
	// RateLimited is the number of 429 responses observed.
	RateLimited int64
	// PanicsRecovered is the number of recovered SDK-internal panics.
	PanicsRecovered int64
	// ConfigReloads is the number of configuration swaps.
	ConfigReloads int64
	// QueuePendingItems is the current number of buffered events.
	QueuePendingItems int64
	// QueuePendingBytes is the current estimated size of buffered events.
	QueuePendingBytes int64
	// SubsystemsDisabled is the number of background subsystems self-disabled
	// after a panic.
	SubsystemsDisabled int64
}

// statsFromSnapshot maps an internal counter snapshot to the public Stats type.
func statsFromSnapshot(s selfmetrics.Snapshot) Stats {
	return Stats{
		Captured:             s.Captured,
		Sent:                 s.Sent,
		DroppedOverflow:      s.DroppedOverflow,
		DroppedSendExhausted: s.DroppedSend,
		DroppedBeforeSend:    s.DroppedBeforeSend,
		Retries:              s.Retries,
		RateLimited:          s.RateLimited,
		PanicsRecovered:      s.PanicsRecovered,
		ConfigReloads:        s.ConfigReloads,
		QueuePendingItems:    s.QueuePendingItems,
		QueuePendingBytes:    s.QueuePendingBytes,
		SubsystemsDisabled:   s.SubsystemsDisabled,
	}
}
