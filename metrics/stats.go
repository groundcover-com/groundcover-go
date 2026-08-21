package metrics

import (
	"sync/atomic"
	"time"
)

// Stats is a point-in-time snapshot of a Client's push counters.
type Stats struct {
	// PushesAttempted is the total number of pushes started (manual and
	// background).
	PushesAttempted int64
	// PushesSucceeded is the number of pushes acknowledged with a 2xx status.
	PushesSucceeded int64
	// PushesFailed is the number of pushes that failed to render or deliver.
	PushesFailed int64
	// LastErrorTime is when the most recent push failure occurred; the zero
	// time means no push has failed.
	LastErrorTime time.Time
	// SeriesLastPush is the number of sample lines rendered by the most
	// recent successfully rendered push.
	SeriesLastPush int64
	// LinesSkipped counts malformed source sample lines that were dropped
	// instead of failing the push.
	LinesSkipped int64
	// SourcePanics counts panics recovered from Exposition sources and user
	// callbacks.
	SourcePanics int64
}

// clientStats holds the client's internal atomic counters.
type clientStats struct {
	attempted         atomic.Int64
	succeeded         atomic.Int64
	failed            atomic.Int64
	lastErrorUnixNano atomic.Int64
	seriesLastPush    atomic.Int64
	linesSkipped      atomic.Int64
	sourcePanics      atomic.Int64
}

// snapshot converts the atomic counters into an exported Stats value.
func (s *clientStats) snapshot() Stats {
	stats := Stats{
		PushesAttempted: s.attempted.Load(),
		PushesSucceeded: s.succeeded.Load(),
		PushesFailed:    s.failed.Load(),
		SeriesLastPush:  s.seriesLastPush.Load(),
		LinesSkipped:    s.linesSkipped.Load(),
		SourcePanics:    s.sourcePanics.Load(),
	}
	if nanos := s.lastErrorUnixNano.Load(); nanos != 0 {
		stats.LastErrorTime = time.Unix(0, nanos)
	}
	return stats
}
