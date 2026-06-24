package logthrottle_test

import (
	"sync"
	"testing"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/logthrottle"
)

type record struct {
	level      logthrottle.Level
	msg        string
	suppressed int
}

type capture struct {
	mu      sync.Mutex
	records []record
}

func (c *capture) Log(level logthrottle.Level, msg string, suppressed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, record{level, msg, suppressed})
}

func (c *capture) snapshot() []record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]record(nil), c.records...)
}

// logErr funnels every call through one source line so the call-site key is
// identical regardless of where the test calls it from.
func logErr(thr *logthrottle.Throttler) { thr.Log(logthrottle.LevelError, "same line") }

func TestPerKeySuppressionWindow(t *testing.T) {
	now := time.Unix(0, 0)
	sink := &capture{}
	thr := logthrottle.New(sink, logthrottle.Options{
		Window:       10 * time.Second,
		GlobalWindow: time.Second,
		GlobalCap:    1000,
		Now:          func() time.Time { return now },
	})

	// All from the same call-site/level: first emits, rest are suppressed.
	for range 5 {
		logErr(thr)
	}
	recs := sink.snapshot()
	if len(recs) != 1 {
		t.Fatalf("expected 1 emitted line within window, got %d", len(recs))
	}
	if recs[0].suppressed != 0 {
		t.Fatalf("first emit should report 0 suppressed, got %d", recs[0].suppressed)
	}

	// Advance past the window; the next line reports the suppressed count.
	now = now.Add(11 * time.Second)
	logErr(thr)
	recs = sink.snapshot()
	if len(recs) != 2 {
		t.Fatalf("expected 2 emitted lines, got %d", len(recs))
	}
	if recs[1].suppressed != 4 {
		t.Fatalf("expected 4 suppressed, got %d", recs[1].suppressed)
	}
}

func TestGlobalCap(t *testing.T) {
	now := time.Unix(0, 0)
	sink := &capture{}
	thr := logthrottle.New(sink, logthrottle.Options{
		Window:       time.Nanosecond, // effectively no per-key window
		GlobalWindow: time.Minute,
		GlobalCap:    2,
		Now:          func() time.Time { return now },
	})

	// Distinct messages so the per-key window never blocks; the global cap does.
	thr.Log(logthrottle.LevelInfo, "a")
	now = now.Add(time.Millisecond)
	thr.Log(logthrottle.LevelWarn, "b")
	now = now.Add(time.Millisecond)
	thr.Log(logthrottle.LevelError, "c")

	if got := len(sink.snapshot()); got != 2 {
		t.Fatalf("global cap should allow only 2 lines, got %d", got)
	}
}

func TestNilSinkDoesNotPanic(t *testing.T) {
	thr := logthrottle.New(nil, logthrottle.Options{})
	thr.Log(logthrottle.LevelInfo, "no sink")
}

func TestPanickingSinkIsContained(t *testing.T) {
	thr := logthrottle.New(logthrottle.SinkFunc(func(logthrottle.Level, string, int) {
		panic("sink blew up")
	}), logthrottle.Options{})
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicking sink must be contained, got %v", r)
		}
	}()
	thr.Log(logthrottle.LevelError, "trigger")
}

func TestLevelString(t *testing.T) {
	cases := map[logthrottle.Level]string{
		logthrottle.LevelDebug: "debug",
		logthrottle.LevelInfo:  "info",
		logthrottle.LevelWarn:  "warn",
		logthrottle.LevelError: "error",
	}
	for lvl, want := range cases {
		if got := lvl.String(); got != want {
			t.Fatalf("level %d: want %q, got %q", lvl, want, got)
		}
	}
}
