// Package logthrottle implements a self-throttling log front-end. SDK-internal
// logging must never become a source of noise or load: lines are de-duplicated
// by call-site and level, suppressed within a per-key window, and capped by a
// global rate. When a key is allowed to log again it reports how many lines were
// suppressed in the meantime.
package logthrottle

import (
	"runtime"
	"sync"
	"time"
)

// Level is the severity of a throttled log line.
type Level int

// Log levels in increasing severity.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the lowercase name of the level.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// Sink is the pluggable destination for throttled log lines. suppressed is the
// number of lines that were dropped for the same call-site/level since the last
// emitted line. A Sink must not panic; if it does, the panic is contained.
type Sink interface {
	Log(level Level, msg string, suppressed int)
}

// SinkFunc adapts a function to a Sink.
type SinkFunc func(level Level, msg string, suppressed int)

// Log calls the underlying function.
func (f SinkFunc) Log(level Level, msg string, suppressed int) { f(level, msg, suppressed) }

type key struct {
	file  string
	line  int
	level Level
}

type entry struct {
	nextAllowed time.Time
	suppressed  int
}

// Throttler de-duplicates and rate-limits log lines. The zero value is not
// usable; construct one with New.
type Throttler struct {
	mu           sync.Mutex
	window       time.Duration
	globalWindow time.Duration
	globalCap    int
	now          func() time.Time

	entries           map[key]*entry
	globalWindowStart time.Time
	globalCount       int

	sink Sink
}

// Options configures a Throttler.
type Options struct {
	// Window is the per-call-site suppression window.
	Window time.Duration
	// GlobalWindow is the window over which GlobalCap is enforced.
	GlobalWindow time.Duration
	// GlobalCap is the maximum number of lines emitted per GlobalWindow across
	// all call-sites. Zero or negative disables the global cap.
	GlobalCap int
	// Now overrides the clock; nil uses time.Now (used for deterministic tests).
	Now func() time.Time
}

// New returns a Throttler writing allowed lines to sink. A nil sink discards
// output. Sensible defaults are applied for any zero option.
func New(sink Sink, opts Options) *Throttler {
	if opts.Window <= 0 {
		opts.Window = 5 * time.Second
	}
	if opts.GlobalWindow <= 0 {
		opts.GlobalWindow = time.Second
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Throttler{
		window:       opts.Window,
		globalWindow: opts.GlobalWindow,
		globalCap:    opts.GlobalCap,
		now:          now,
		entries:      make(map[key]*entry),
		sink:         sink,
	}
}

// Log throttles a log line attributed to the immediate caller's source location.
func (t *Throttler) Log(level Level, msg string) {
	_, file, line, ok := runtime.Caller(1)
	if !ok {
		file, line = "?", 0
	}
	t.logAt(key{file: file, line: line, level: level}, level, msg)
}

func (t *Throttler) logAt(k key, level Level, msg string) {
	emit, suppressed := t.admit(k)
	if !emit {
		return
	}
	t.deliver(level, msg, suppressed)
}

// admit applies the per-key window and global cap, returning whether the line
// should be emitted and how many prior lines were suppressed for the key.
func (t *Throttler) admit(k key) (emit bool, suppressed int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	if t.globalWindowStart.IsZero() || now.Sub(t.globalWindowStart) >= t.globalWindow {
		t.globalWindowStart = now
		t.globalCount = 0
	}

	e := t.entries[k]
	if e == nil {
		e = &entry{}
		t.entries[k] = e
	}

	if now.Before(e.nextAllowed) {
		e.suppressed++
		return false, 0
	}
	if t.globalCap > 0 && t.globalCount >= t.globalCap {
		e.suppressed++
		return false, 0
	}

	suppressed = e.suppressed
	e.suppressed = 0
	e.nextAllowed = now.Add(t.window)
	t.globalCount++
	return true, suppressed
}

// deliver sends a line to the sink, containing any panic from the sink itself.
func (t *Throttler) deliver(level Level, msg string, suppressed int) {
	if t.sink == nil {
		return
	}
	defer func() { _ = recover() }()
	t.sink.Log(level, msg, suppressed)
}
