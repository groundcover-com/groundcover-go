package logs

import (
	"context"
	"time"
)

// User is identity associated with a log record.
type User struct {
	ID           string
	Email        string
	Name         string
	Organization string
}

// Record is one structured log occurrence. It is also the value passed to
// Config.BeforeSend.
type Record struct {
	// ID is an optional caller-assigned record identifier. It is not written
	// to the logs intake unless also supplied as an attribute.
	ID string
	// Timestamp is when the record was emitted.
	Timestamp time.Time
	// Level is the severity.
	Level Level
	// Message is the log body.
	Message string
	// Attributes are structured fields.
	Attributes Attributes
	// User is optional identity.
	User User
	// SessionID is an optional session identifier.
	SessionID string
	// AnonymousID is an optional pre-auth identifier.
	AnonymousID string
}

// Emitter is the narrow interface implemented by Client and consumed by
// logging-library bridges.
type Emitter interface {
	EmitLog(ctx context.Context, level Level, msg string, attrs ...Attributes)
}
