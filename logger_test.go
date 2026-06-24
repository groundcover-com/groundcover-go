package groundcover

import (
	"log/slog"
	"testing"

	"github.com/groundcover-com/groundcover-go/internal/logthrottle"
)

func TestToSlogLevel(t *testing.T) {
	cases := map[Level]slog.Level{
		LevelDebug:     slog.LevelDebug,
		LevelInfo:      slog.LevelInfo,
		LevelWarning:   slog.LevelWarn,
		LevelError:     slog.LevelError,
		LevelFatal:     slog.LevelError,
		Level("weird"): slog.LevelInfo,
	}
	for lvl, want := range cases {
		if got := toSlogLevel(lvl); got != want {
			t.Fatalf("toSlogLevel(%q) = %v, want %v", lvl, got, want)
		}
	}
}

func TestFromThrottleLevel(t *testing.T) {
	cases := map[logthrottle.Level]Level{
		logthrottle.LevelDebug: LevelDebug,
		logthrottle.LevelInfo:  LevelInfo,
		logthrottle.LevelWarn:  LevelWarning,
		logthrottle.LevelError: LevelError,
		logthrottle.Level(99):  LevelInfo,
	}
	for in, want := range cases {
		if got := fromThrottleLevel(in); got != want {
			t.Fatalf("fromThrottleLevel(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestLoggerSinkTranslatesAndForwards(t *testing.T) {
	var (
		gotLevel      Level
		gotMsg        string
		gotSuppressed int
	)
	sink := loggerSink{logger: LoggerFunc(func(l Level, msg string, suppressed int) {
		gotLevel, gotMsg, gotSuppressed = l, msg, suppressed
	})}
	sink.Log(logthrottle.LevelWarn, "hello", 3)
	if gotLevel != LevelWarning || gotMsg != "hello" || gotSuppressed != 3 {
		t.Fatalf("loggerSink did not forward correctly: %q %q %d", gotLevel, gotMsg, gotSuppressed)
	}
}

func TestLoggerSinkNilLoggerIsSafe(t *testing.T) {
	sink := loggerSink{logger: nil}
	sink.Log(logthrottle.LevelInfo, "ignored", 0) // must not panic
}

func TestSlogLoggerDoesNotPanic(t *testing.T) {
	l := newSlogLogger()
	l.Log(LevelError, "diagnostic", 0)
	l.Log(LevelWarning, "throttled", 5)
}

func TestResolveLogger(t *testing.T) {
	custom := LoggerFunc(func(Level, string, int) {})
	if resolveLogger(custom) == nil {
		t.Fatal("custom logger should be returned")
	}
	if resolveLogger(nil) == nil {
		t.Fatal("nil logger should resolve to a default")
	}
}
