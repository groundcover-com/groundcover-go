package logs

import "github.com/groundcover-com/groundcover-go/internal/selfmetrics"

// Stats is a point-in-time snapshot of the logs client's delivery counters.
type Stats struct {
	Captured             int64
	Sent                 int64
	DroppedOverflow      int64
	DroppedSendExhausted int64
	DroppedBeforeSend    int64
	Retries              int64
	RateLimited          int64
	PanicsRecovered      int64
	QueuePendingItems    int64
	QueuePendingBytes    int64
	SubsystemsDisabled   int64
}

func statsFromSnapshot(snapshot selfmetrics.Snapshot) Stats {
	return Stats{
		Captured:             snapshot.Captured,
		Sent:                 snapshot.Sent,
		DroppedOverflow:      snapshot.DroppedOverflow,
		DroppedSendExhausted: snapshot.DroppedSend,
		DroppedBeforeSend:    snapshot.DroppedBeforeSend,
		Retries:              snapshot.Retries,
		RateLimited:          snapshot.RateLimited,
		PanicsRecovered:      snapshot.PanicsRecovered,
		QueuePendingItems:    snapshot.QueuePendingItems,
		QueuePendingBytes:    snapshot.QueuePendingBytes,
		SubsystemsDisabled:   snapshot.SubsystemsDisabled,
	}
}
