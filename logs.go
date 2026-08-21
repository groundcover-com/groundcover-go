package groundcover

import (
	"context"
	"fmt"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/safeguard"
	"github.com/groundcover-com/groundcover-go/internal/selfmetrics"
)

// Log is the internal representation of a captured log record. Public callers
// never build a Log directly; the EmitLog / Log* APIs do. It is exported only
// so that Config.BeforeSendLog can inspect, scrub, or drop records.
type Log struct {
	// ID is a per-record identifier used for de-duplication.
	ID string
	// Timestamp is when the record was emitted.
	Timestamp time.Time
	// Level is the severity.
	Level Level
	// Message is the log body.
	Message string
	// Attributes is the custom data bag.
	Attributes Attributes
	// User is the associated identity, if any.
	User User
	// SessionID is an optional session identifier (usually empty for backends).
	SessionID string
	// AnonymousID is a caller-supplied pre-auth identifier (no PII by construction).
	AnonymousID string
	// Service identifies the instrumented service.
	Service Service
}

// EmitLog captures a single log record and enqueues it for direct delivery to
// groundcover — no agent, sidecar, or file tailer involved. It never blocks on
// I/O and never affects control flow. It is a no-op unless Config.EnableLogs
// is true.
//
// The record inherits identity and attributes from the context scope (set via
// SetUser / WithScope); per-call attrs merge on top and win on key conflicts.
// An invalid level defaults to LevelInfo.
func (c *Client) EmitLog(ctx context.Context, level Level, msg string, attrs ...Attributes) {
	if c.disabled || c.logRing == nil {
		return
	}
	safeguard.Do(func() {
		if !level.valid() {
			level = LevelInfo
		}
		l := &Log{
			ID:        newUUID(),
			Timestamp: time.Now(),
			Level:     level,
			Message:   msg,
			Service:   Service{Name: c.res.serviceName, Version: c.res.release},
		}
		for _, a := range attrs {
			if len(a) == 0 {
				continue
			}
			if l.Attributes == nil {
				l.Attributes = make(Attributes, len(a))
			}
			l.Attributes.merge(a)
		}
		c.finishAndEnqueueLog(ctx, l)
	}, c.onPanic)
}

// LogDebug emits a debug-level log record. See EmitLog.
func (c *Client) LogDebug(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelDebug, msg, attrs...)
}

// LogInfo emits an info-level log record. See EmitLog.
func (c *Client) LogInfo(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelInfo, msg, attrs...)
}

// LogWarn emits a warning-level log record. See EmitLog.
func (c *Client) LogWarn(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelWarning, msg, attrs...)
}

// LogError emits an error-level log record. See EmitLog.
func (c *Client) LogError(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelError, msg, attrs...)
}

// LogDebugf emits a debug-level log record with fmt.Sprintf formatting.
func (c *Client) LogDebugf(ctx context.Context, format string, args ...any) {
	c.EmitLog(ctx, LevelDebug, fmt.Sprintf(format, args...))
}

// LogInfof emits an info-level log record with fmt.Sprintf formatting.
func (c *Client) LogInfof(ctx context.Context, format string, args ...any) {
	c.EmitLog(ctx, LevelInfo, fmt.Sprintf(format, args...))
}

// LogWarnf emits a warning-level log record with fmt.Sprintf formatting.
func (c *Client) LogWarnf(ctx context.Context, format string, args ...any) {
	c.EmitLog(ctx, LevelWarning, fmt.Sprintf(format, args...))
}

// LogErrorf emits an error-level log record with fmt.Sprintf formatting.
func (c *Client) LogErrorf(ctx context.Context, format string, args ...any) {
	c.EmitLog(ctx, LevelError, fmt.Sprintf(format, args...))
}

// finishAndEnqueueLog applies scope, runs BeforeSendLog, pseudonymizes identity,
// snapshots attributes, and enqueues the record on the log pipeline.
func (c *Client) finishAndEnqueueLog(ctx context.Context, l *Log) {
	cfg := c.config()

	scopeFromContext(ctx).applyToLog(l)

	if cfg.BeforeSendLog != nil {
		out := c.runBeforeSendLog(cfg.BeforeSendLog, l)
		if out == nil {
			c.logMetrics.AddDropped(selfmetrics.DropBeforeSend, 1)
			return
		}
		l = out
	}

	// Pseudonymize identity as the final step before enqueue, mirroring the
	// error pipeline: nothing after this point can put a raw user.id/email on
	// the wire.
	if cfg.Hasher != nil {
		l.User.ID = cfg.Hasher.HashIdentity(l.User.ID)
		l.User.Email = cfg.Hasher.HashIdentity(l.User.Email)
	}

	// Snapshot the attributes: a deep, JSON-coerced copy so later caller
	// mutation cannot change the queued record.
	l.Attributes = sanitizeAttributes(l.Attributes)

	if c.debug {
		c.writeLogDebug(l)
	}

	c.logMetrics.IncCaptured()
	c.enqueueLog(cfg, l)
}

// runBeforeSendLog invokes the user callback inside a panic guard. A panic is
// treated as "keep the record unmodified".
func (c *Client) runBeforeSendLog(fn func(*Log) *Log, l *Log) (out *Log) {
	out = l
	safeguard.Do(func() { out = fn(l) }, c.onPanic)
	return out
}

// enqueueLog performs the bounded, non-blocking hand-off to the log pipeline.
func (c *Client) enqueueLog(cfg *Config, l *Log) {
	dropped := c.logRing.Push(l)
	if dropped > 0 {
		c.logMetrics.AddDropped(selfmetrics.DropOverflow, int64(dropped))
	}
	c.logMetrics.SetQueuePending(int64(c.logRing.Len()), int64(c.logRing.Bytes()))
	if c.logRing.Len() >= cfg.BatchSize {
		c.logWorker.Notify()
	}
}

// writeLogDebug renders a finalized log record to the debug writer, guarded so
// it can never affect capture.
func (c *Client) writeLogDebug(l *Log) {
	if c.debugOut == nil {
		return
	}
	safeguard.Do(func() { _, _ = fmt.Fprint(c.debugOut, renderLogDebug(l)) }, c.onPanic)
}

// LogStats returns a snapshot of the log pipeline's self-observability
// counters. Client-level counters (panics recovered, config reloads) are
// reported on Stats, not here.
func (c *Client) LogStats() Stats {
	return statsFromSnapshot(c.logMetrics.Snapshot())
}
