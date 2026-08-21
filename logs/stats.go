package logs

import "github.com/groundcover-com/groundcover-go/internal/selfmetrics"

// Stats is a point-in-time snapshot of the logs client's delivery counters.
type Stats struct {
	// Captured is the number of records accepted into the pipeline.
	Captured int64
	// Sent is the number of records delivered to the intake.
	Sent int64
	// DroppedOverflow is the number of records evicted because the queue
	// exceeded its item or byte budget, or that were too large to queue.
	DroppedOverflow int64
	// DroppedSendExhausted is the number of records dropped after delivery
	// retries were exhausted or the intake returned a permanent error.
	DroppedSendExhausted int64
	// DroppedBeforeSend is the number of records dropped by BeforeSend.
	DroppedBeforeSend int64
	// DroppedShutdown is the number of records refused because the client had
	// started closing.
	DroppedShutdown int64
	// Retries is the number of delivery retry attempts.
	Retries int64
	// RateLimited is the number of 429 responses from the intake.
	RateLimited int64
	// PanicsRecovered is the number of SDK-internal panics contained.
	PanicsRecovered int64
	// QueuePendingItems is the number of records currently queued.
	QueuePendingItems int64
	// QueuePendingBytes is the encoded size of the currently queued records.
	QueuePendingBytes int64
	// SubsystemsDisabled counts background workers that stopped themselves
	// after a panic. A non-zero value means delivery is degraded.
	SubsystemsDisabled int64
}

func statsFromSnapshot(snapshot selfmetrics.Snapshot) Stats {
	return Stats{
		Captured:             snapshot.Captured,
		Sent:                 snapshot.Sent,
		DroppedOverflow:      snapshot.DroppedOverflow,
		DroppedSendExhausted: snapshot.DroppedSend,
		DroppedBeforeSend:    snapshot.DroppedBeforeSend,
		DroppedShutdown:      snapshot.DroppedShutdown,
		Retries:              snapshot.Retries,
		RateLimited:          snapshot.RateLimited,
		PanicsRecovered:      snapshot.PanicsRecovered,
		QueuePendingItems:    snapshot.QueuePendingItems,
		QueuePendingBytes:    snapshot.QueuePendingBytes,
		SubsystemsDisabled:   snapshot.SubsystemsDisabled,
	}
}
