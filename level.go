package groundcover

// Level is the severity of a captured event. It follows OTel SeverityText
// conventions and maps to a numeric SeverityNumber on the wire.
type Level string

// Supported severity levels.
const (
	// LevelDebug is fine-grained diagnostic information.
	LevelDebug Level = "debug"
	// LevelInfo is informational.
	LevelInfo Level = "info"
	// LevelWarning indicates a recoverable problem or notable condition.
	LevelWarning Level = "warning"
	// LevelError indicates an error; the default for CaptureError.
	LevelError Level = "error"
	// LevelFatal indicates an unrecoverable error.
	LevelFatal Level = "fatal"
)

// severityNumber maps a Level to the OTel SeverityNumber range.
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
		return 17
	}
}

// valid reports whether l is one of the known levels.
func (l Level) valid() bool {
	switch l {
	case LevelDebug, LevelInfo, LevelWarning, LevelError, LevelFatal:
		return true
	default:
		return false
	}
}
