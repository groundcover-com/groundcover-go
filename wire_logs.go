package groundcover

import "encoding/json"

// wireLogAttributes is the attributes bag of a "log" wire event, symmetric with
// wireAttributes for "exception" events.
type wireLogAttributes struct {
	Message     string         `json:"message"`
	LogMetadata map[string]any `json:"log_metadata,omitempty"`
}

// wireLogEvent is the wire representation of a single log record.
type wireLogEvent struct {
	Type         string            `json:"type"`
	Level        string            `json:"level,omitempty"`
	Timestamp    int64             `json:"timestamp"`
	ID           string            `json:"id"`
	SpanID       string            `json:"spanId"`
	ParentSpanID string            `json:"parentSpanId"`
	TraceID      string            `json:"traceId"`
	Attributes   wireLogAttributes `json:"attributes"`
}

// wireLogPayload is the request body for a batch of log records. It shares the
// session-attribute spine with the event payload.
type wireLogPayload struct {
	SessionAttributes map[string]any `json:"sessionAttributes"`
	Events            []wireLogEvent `json:"events"`
}

// buildLogMetadata assembles the log_metadata bag for a single record:
// identity, detailed resource attributes, severity, and custom attributes.
// Caller attributes are written first; SDK-managed keys are written afterwards
// so a caller cannot override them. The record's Attributes are expected to be
// sanitized already (done at capture).
func buildLogMetadata(l *Log, res resource) map[string]any {
	md := make(map[string]any, len(l.Attributes)+len(res.attrs)+8)

	for k, v := range l.Attributes {
		if isSDKManagedKey(k) {
			continue
		}
		md[k] = v
	}

	for k, v := range res.attrs {
		if isSessionLevelKey(k) {
			continue
		}
		md[k] = v
	}

	setIfNonEmpty(md, "user.id", l.User.ID)
	setIfNonEmpty(md, "user.email", l.User.Email)
	setIfNonEmpty(md, "user.name", l.User.Name)
	setIfNonEmpty(md, "user.organization", l.User.Organization)
	setIfNonEmpty(md, "session.id", l.SessionID)
	setIfNonEmpty(md, "anonymous_id", l.AnonymousID)

	md["level"] = string(l.Level)
	md["severity_number"] = l.Level.severityNumber()
	return md
}

// toWireLogEvent converts an internal Log to its wire representation.
func toWireLogEvent(l *Log, res resource) wireLogEvent {
	return wireLogEvent{
		Type:         logEventType,
		Level:        string(l.Level),
		Timestamp:    l.Timestamp.UnixNano(),
		ID:           l.ID,
		SpanID:       newSpanID(),
		ParentSpanID: "",
		TraceID:      newTraceID(),
		Attributes: wireLogAttributes{
			Message:     l.Message,
			LogMetadata: buildLogMetadata(l, res),
		},
	}
}

// encodeLogBatch serializes a batch of log records into the ingestion body.
func encodeLogBatch(logs []*Log, res resource) ([]byte, error) {
	payload := wireLogPayload{
		SessionAttributes: res.sessionAttributes(),
		Events:            make([]wireLogEvent, 0, len(logs)),
	}
	for _, l := range logs {
		payload.Events = append(payload.Events, toWireLogEvent(l, res))
	}
	return json.Marshal(payload)
}

// estimateLogSize returns a cheap byte estimate of a log record for the
// buffer's byte budget. It intentionally over- rather than under-estimates.
func estimateLogSize(l *Log) int {
	const base = 192
	size := base + len(l.Message)
	for k, v := range l.Attributes {
		size += len(k) + estimateValueSize(v)
	}
	size += len(l.User.ID) + len(l.User.Email) + len(l.User.Name) + len(l.User.Organization)
	return size
}
