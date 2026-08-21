package groundcover

import (
	"context"
	"fmt"
	"time"

	"github.com/groundcover-com/groundcover-go/internal/safeguard"
	gclogs "github.com/groundcover-com/groundcover-go/logs"
)

// Log is the aggregate SDK representation of a log record. It is exported so
// Config.BeforeSendLog can inspect, scrub, or drop records.
type Log struct {
	ID          string
	Timestamp   time.Time
	Level       Level
	Message     string
	Attributes  Attributes
	User        User
	SessionID   string
	AnonymousID string
	// Service reports the service the record belongs to. It is informational:
	// the service and version on the wire come from the client's configuration,
	// so setting this field has no effect on delivery.
	Service Service
}

// EmitLog captures a log through this client's standalone logs component.
func (c *Client) EmitLog(ctx context.Context, level Level, msg string, attrs ...Attributes) {
	if c.disabled || c.logClient == nil {
		return
	}
	safeguard.Do(func() {
		record := Log{Level: level, Message: msg}
		for _, attributes := range attrs {
			if record.Attributes == nil {
				record.Attributes = make(Attributes, len(attributes))
			}
			record.Attributes.merge(attributes)
		}
		c.emitLogRecord(ctx, record)
	}, c.onPanic)
}

// EmitLogRecord captures a pre-built log record, preserving the timestamp it
// carries. It exists for logging-library bridges, which observe the time a
// record was created rather than the time it reaches the SDK. Empty fields are
// filled in: a zero Timestamp becomes now and an empty ID is generated. Context
// scope is applied as it is for EmitLog.
func (c *Client) EmitLogRecord(ctx context.Context, record Log) {
	if c.disabled || c.logClient == nil {
		return
	}
	safeguard.Do(func() { c.emitLogRecord(ctx, record) }, c.onPanic)
}

// emitLogRecord normalizes and forwards a record. The caller guards panics.
func (c *Client) emitLogRecord(ctx context.Context, record Log) {
	if !record.Level.valid() {
		record.Level = LevelInfo
	}
	if record.ID == "" {
		record.ID = newUUID()
	}
	if record.Timestamp.IsZero() {
		record.Timestamp = time.Now()
	}
	if record.Service.Name == "" {
		record.Service = Service{Name: c.res.serviceName, Version: c.res.release}
	}
	scopeFromContext(ctx).applyToLog(&record)
	c.logClient.EmitRecord(ctx, toStandaloneRecord(&record))
}

// LogDebug emits a debug-level log record.
func (c *Client) LogDebug(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelDebug, msg, attrs...)
}

// LogInfo emits an info-level log record.
func (c *Client) LogInfo(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelInfo, msg, attrs...)
}

// LogWarn emits a warning-level log record.
func (c *Client) LogWarn(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelWarning, msg, attrs...)
}

// LogError emits an error-level log record.
func (c *Client) LogError(ctx context.Context, msg string, attrs ...Attributes) {
	c.EmitLog(ctx, LevelError, msg, attrs...)
}

// LogDebugf emits a formatted debug-level log record.
func (c *Client) LogDebugf(ctx context.Context, format string, args ...any) {
	c.LogDebug(ctx, fmt.Sprintf(format, args...))
}

// LogInfof emits a formatted info-level log record.
func (c *Client) LogInfof(ctx context.Context, format string, args ...any) {
	c.LogInfo(ctx, fmt.Sprintf(format, args...))
}

// LogWarnf emits a formatted warning-level log record.
func (c *Client) LogWarnf(ctx context.Context, format string, args ...any) {
	c.LogWarn(ctx, fmt.Sprintf(format, args...))
}

// LogErrorf emits a formatted error-level log record.
func (c *Client) LogErrorf(ctx context.Context, format string, args ...any) {
	c.LogError(ctx, fmt.Sprintf(format, args...))
}

// LogStats returns the standalone logs component's delivery counters.
func (c *Client) LogStats() Stats {
	if c == nil || c.logClient == nil {
		return Stats{}
	}
	stats := c.logClient.Stats()
	return Stats{
		Captured:             stats.Captured,
		Sent:                 stats.Sent,
		DroppedOverflow:      stats.DroppedOverflow,
		DroppedSendExhausted: stats.DroppedSendExhausted,
		DroppedBeforeSend:    stats.DroppedBeforeSend,
		Retries:              stats.Retries,
		RateLimited:          stats.RateLimited,
		PanicsRecovered:      stats.PanicsRecovered,
		QueuePendingItems:    stats.QueuePendingItems,
		QueuePendingBytes:    stats.QueuePendingBytes,
		SubsystemsDisabled:   stats.SubsystemsDisabled,
	}
}

// LogsClient returns the standalone logs component owned by this aggregate
// client, or nil when Config.EnableLogs is false. This is useful when passing
// the emitter to a logging-library bridge.
func (c *Client) LogsClient() *gclogs.Client {
	if c == nil {
		return nil
	}
	return c.logClient
}

func adaptBeforeSendLog(callback func(*Log) *Log, service Service) func(*gclogs.Record) *gclogs.Record {
	if callback == nil {
		return nil
	}
	return func(record *gclogs.Record) *gclogs.Record {
		aggregate := fromStandaloneRecord(record)
		aggregate.Service = service
		out := callback(&aggregate)
		if out == nil {
			return nil
		}
		converted := toStandaloneRecord(out)
		return &converted
	}
}

func toStandaloneRecord(record *Log) gclogs.Record {
	return gclogs.Record{
		ID:          record.ID,
		Timestamp:   record.Timestamp,
		Level:       toLogsLevel(record.Level),
		Message:     record.Message,
		Attributes:  gclogs.Attributes(record.Attributes),
		User:        gclogs.User{ID: record.User.ID, Email: record.User.Email, Name: record.User.Name, Organization: record.User.Organization},
		SessionID:   record.SessionID,
		AnonymousID: record.AnonymousID,
	}
}

func fromStandaloneRecord(record *gclogs.Record) Log {
	return Log{
		ID:          record.ID,
		Timestamp:   record.Timestamp,
		Level:       fromLogsLevel(record.Level),
		Message:     record.Message,
		Attributes:  Attributes(record.Attributes),
		User:        User{ID: record.User.ID, Email: record.User.Email, Name: record.User.Name, Organization: record.User.Organization},
		SessionID:   record.SessionID,
		AnonymousID: record.AnonymousID,
	}
}

func toLogsLevel(level Level) gclogs.Level   { return gclogs.Level(level) }
func fromLogsLevel(level gclogs.Level) Level { return Level(level) }
