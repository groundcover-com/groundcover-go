package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Stats is a point-in-time snapshot of a Client's push counters. It never
// contains credentials.
type Stats struct {
	// Pushes is the total number of push attempts (manual and periodic).
	Pushes uint64
	// PushFailures counts pushes whose delivery failed entirely (rendering
	// hit a fatal limit, or the HTTP request did not get a 2xx).
	PushFailures uint64
	// PartialPushes counts pushes that were delivered but had per-source
	// failures (panics, malformed lines, or dropped duplicate series).
	PartialPushes uint64
	// SourceFailures counts sources that had at least one failure during a
	// push, summed over all pushes.
	SourceFailures uint64
	// SourcePanics counts panics recovered from metric sources and user
	// callbacks.
	SourcePanics uint64
	// MalformedLines counts source sample lines that could not be parsed and
	// were dropped.
	MalformedLines uint64
	// DuplicateSeries counts samples dropped (or, in strict mode, rejected)
	// because another source already produced the same final series.
	DuplicateSeries uint64
	// LabelOverrides counts source labels replaced by a same-named fixed
	// label (job, instance, or an ExtraLabels entry).
	LabelOverrides uint64
	// SeriesLastPush is the number of sample lines rendered by the most
	// recent fully rendered push.
	SeriesLastPush int
	// BytesLastPush is the uncompressed exposition size of the most recent
	// fully rendered push.
	BytesLastPush int
	// LastAttempt is when the most recent push started; zero when no push
	// has been attempted.
	LastAttempt time.Time
	// LastSuccess is when the most recent push was delivered with a 2xx;
	// zero when no push has succeeded.
	LastSuccess time.Time
	// LastError is the sanitized message of the most recent push error
	// (partial or failed). It never contains the ingestion key.
	LastError string
	// WorkerRunning reports whether the background worker started by Start
	// is currently running.
	WorkerRunning bool
}

// clientStats holds the client's internal counters.
type clientStats struct {
	pushes          atomic.Uint64
	pushFailures    atomic.Uint64
	partialPushes   atomic.Uint64
	sourceFailures  atomic.Uint64
	sourcePanics    atomic.Uint64
	malformedLines  atomic.Uint64
	duplicateSeries atomic.Uint64
	labelOverrides  atomic.Uint64

	seriesLastPush atomic.Int64
	bytesLastPush  atomic.Int64

	lastAttemptUnixNano atomic.Int64
	lastSuccessUnixNano atomic.Int64

	lastErrorMu sync.Mutex
	lastError   string
}

// setLastError records the most recent push error message.
func (s *clientStats) setLastError(message string) {
	s.lastErrorMu.Lock()
	s.lastError = message
	s.lastErrorMu.Unlock()
}

// snapshot converts the internal counters into an exported Stats value.
func (s *clientStats) snapshot(workerRunning bool) Stats {
	stats := Stats{
		Pushes:          s.pushes.Load(),
		PushFailures:    s.pushFailures.Load(),
		PartialPushes:   s.partialPushes.Load(),
		SourceFailures:  s.sourceFailures.Load(),
		SourcePanics:    s.sourcePanics.Load(),
		MalformedLines:  s.malformedLines.Load(),
		DuplicateSeries: s.duplicateSeries.Load(),
		LabelOverrides:  s.labelOverrides.Load(),
		SeriesLastPush:  int(s.seriesLastPush.Load()),
		BytesLastPush:   int(s.bytesLastPush.Load()),
		WorkerRunning:   workerRunning,
	}
	if nanos := s.lastAttemptUnixNano.Load(); nanos != 0 {
		stats.LastAttempt = time.Unix(0, nanos)
	}
	if nanos := s.lastSuccessUnixNano.Load(); nanos != 0 {
		stats.LastSuccess = time.Unix(0, nanos)
	}
	s.lastErrorMu.Lock()
	stats.LastError = s.lastError
	s.lastErrorMu.Unlock()
	return stats
}
