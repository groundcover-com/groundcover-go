// Package logs provides the standalone groundcover logs client.
package logs

// Level is a log severity.
type Level string

// Supported log levels.
const (
	LevelDebug   Level = "debug"
	LevelInfo    Level = "info"
	LevelWarning Level = "warning"
	LevelError   Level = "error"
	LevelFatal   Level = "fatal"
)

func (l Level) valid() bool {
	switch l {
	case LevelDebug, LevelInfo, LevelWarning, LevelError, LevelFatal:
		return true
	default:
		return false
	}
}

func (l Level) severityNumber() int {
	switch l {
	case LevelDebug:
		return 5
	case LevelInfo:
		return 9
	case LevelWarning:
		return 13
	case LevelError:
		return 17
	case LevelFatal:
		return 21
	default:
		return 9
	}
}

func (l Level) wire() string {
	if l == LevelWarning {
		return "warn"
	}
	return string(l)
}
