package groundcover

import "time"

// Frame is a single resolved stack frame. Field names follow OTel code.*
// semantics internally; they are mapped to the wire representation at encode time.
type Frame struct {
	// Function is the fully-qualified function name (code.function.name).
	Function string
	// File is the source file path (code.file.path).
	File string
	// Line is the source line number (code.line.number).
	Line int
	// InApp reports whether the frame belongs to the application (under the main
	// module path, excluding vendored code).
	InApp bool
}

// Service identifies the instrumented service on the wire.
type Service struct {
	// Name is service.name.
	Name string
	// Version is service.version.
	Version string
}

// Event is the internal representation of a captured occurrence. Public callers
// never build an Event directly; Options mutate it before enqueue. It is
// exported only so that Option and BeforeSend can operate on it.
type Event struct {
	// ID is a per-occurrence identifier used for de-duplication.
	ID string
	// Timestamp is when the event was captured.
	Timestamp time.Time
	// Type is the event type (always "exception" in v1).
	Type string
	// Level is the severity.
	Level Level
	// User is the associated identity, if any.
	User User
	// SessionID is an optional session identifier (usually empty for backends).
	SessionID string
	// AnonymousID is a caller-supplied pre-auth identifier (no PII by construction).
	AnonymousID string
	// Service identifies the instrumented service.
	Service Service
	// ErrorType is the innermost meaningful error type.
	ErrorType string
	// ErrorMessage is the error message.
	ErrorMessage string
	// ErrorHandled reports whether the error was handled (vs. an unrecovered panic).
	ErrorHandled bool
	// Stacktrace is the resolved frames, innermost first.
	Stacktrace []Frame
	// Fingerprint is the client-computed grouping key (opaque hash).
	Fingerprint string
	// Title is the human-readable display label (e.g. "*net.OpError: connection
	// refused"). It is derived from ErrorType and ErrorMessage when left empty.
	// Unlike Fingerprint, it is for display, not grouping.
	Title string
	// Attributes is the custom data bag.
	Attributes Attributes
	// Resource is the detected resource/spine attributes (telemetry.sdk.*, k8s.*, ...).
	Resource map[string]string

	// levelLocked marks an intrinsically-severe event (a recovered panic) whose
	// Level must not be downgraded by a request scope. Per-call options may still
	// change it.
	levelLocked bool
}

// Option mutates an Event before it is enqueued. Options are applied last and
// therefore take precedence over global defaults and the request scope.
type Option func(*Event)

// WithAttributes merges the given attributes into the event (per-call override).
func WithAttributes(a Attributes) Option {
	return func(e *Event) {
		if e.Attributes == nil {
			e.Attributes = make(Attributes, len(a))
		}
		e.Attributes.merge(a)
	}
}

// WithLevel overrides the event severity.
func WithLevel(l Level) Option {
	return func(e *Event) {
		if l.valid() {
			e.Level = l
		}
	}
}

// WithFingerprint overrides the client-computed grouping fingerprint.
func WithFingerprint(fp string) Option {
	return func(e *Event) { e.Fingerprint = fp }
}

// WithTitle overrides the human-readable display title for the event.
func WithTitle(title string) Option {
	return func(e *Event) { e.Title = title }
}

// WithUser sets the identity on the event (per-call override).
func WithUser(u User) Option {
	return func(e *Event) { e.User = u }
}
