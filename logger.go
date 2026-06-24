package groundcover

import (
	"context"
	"log/slog"

	"github.com/groundcover-com/groundcover-go/internal/logthrottle"
)

// Logger is the pluggable sink for SDK-internal logs. Implementations must never
// panic; a panicking logger is contained by the SDK. suppressed reports how many
// identical lines were throttled since the last emitted line.
type Logger interface {
	Log(level Level, msg string, suppressed int)
}

// LoggerFunc adapts a function to a Logger.
type LoggerFunc func(level Level, msg string, suppressed int)

// Log calls the underlying function.
func (f LoggerFunc) Log(level Level, msg string, suppressed int) { f(level, msg, suppressed) }

// slogLogger is the default Logger, writing through log/slog.
type slogLogger struct{ l *slog.Logger }

func newSlogLogger() slogLogger { return slogLogger{l: slog.Default()} }

func (s slogLogger) Log(level Level, msg string, suppressed int) {
	attrs := []any{slog.String("component", "groundcover-sdk")}
	if suppressed > 0 {
		attrs = append(attrs, slog.Int("suppressed", suppressed))
	}
	s.l.LogAttrs(context.Background(), toSlogLevel(level), msg, toAttrs(attrs)...)
}

func toAttrs(kv []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(kv))
	for _, v := range kv {
		if a, ok := v.(slog.Attr); ok {
			out = append(out, a)
		}
	}
	return out
}

func toSlogLevel(l Level) slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarning:
		return slog.LevelWarn
	case LevelError, LevelFatal:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// loggerSink adapts a Logger to the throttler's Sink, translating levels.
type loggerSink struct{ logger Logger }

func (s loggerSink) Log(level logthrottle.Level, msg string, suppressed int) {
	if s.logger == nil {
		return
	}
	s.logger.Log(fromThrottleLevel(level), msg, suppressed)
}

func fromThrottleLevel(l logthrottle.Level) Level {
	switch l {
	case logthrottle.LevelDebug:
		return LevelDebug
	case logthrottle.LevelInfo:
		return LevelInfo
	case logthrottle.LevelWarn:
		return LevelWarning
	case logthrottle.LevelError:
		return LevelError
	default:
		return LevelInfo
	}
}
