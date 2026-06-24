package groundcover

import "encoding/json"

// scalarSizeEstimate is the assumed byte cost of a non-string scalar attribute
// value (number/bool) in the buffer's byte budget.
const scalarSizeEstimate = 16

// userAgent is the SDK identifier sent on the wire.
func userAgent() string { return sdkName + "/" + Version }

type wireFrame struct {
	Filename string `json:"filename"`
	Function string `json:"function"`
	Lineno   int    `json:"lineno"`
	Colno    int    `json:"colno"`
}

type wireAttributes struct {
	ErrorType        string         `json:"error_type"`
	ErrorMessage     string         `json:"error_message"`
	ErrorHandled     bool           `json:"error_handled"`
	ErrorStacktrace  []wireFrame    `json:"error_stacktrace,omitempty"`
	ErrorFingerprint string         `json:"error_fingerprint,omitempty"`
	ErrorMetadata    map[string]any `json:"error_metadata,omitempty"`
}

type wireEvent struct {
	Type         string         `json:"type"`
	Level        string         `json:"level,omitempty"`
	Timestamp    int64          `json:"timestamp"`
	ID           string         `json:"id"`
	SpanID       string         `json:"spanId"`
	ParentSpanID string         `json:"parentSpanId"`
	TraceID      string         `json:"traceId"`
	Attributes   wireAttributes `json:"attributes"`
}

type wirePayload struct {
	SessionAttributes map[string]any `json:"sessionAttributes"`
	Events            []wireEvent    `json:"events"`
}

// sessionAttributes builds the per-batch resource spine. These keys are
// first-class on the RUM ingestion endpoint and become queryable top-level
// fields (service.name, env, namespace, cluster, releaseId).
func (r resource) sessionAttributes() map[string]any {
	return map[string]any{
		attrServiceName:      r.serviceName,
		"env":                r.env,
		"namespace":          r.namespace,
		"cluster":            r.cluster,
		"releaseId":          r.release,
		"session_start_time": r.startTime.UnixNano(),
		"userAgent":          userAgent(),
	}
}

// isSessionLevelKey reports whether a resource attribute already travels in
// sessionAttributes and so should be omitted from per-event error_metadata to
// avoid redundancy.
func isSessionLevelKey(k string) bool {
	switch k {
	case attrServiceName, attrServiceVer, attrDeployEnv, attrK8sNamespace, attrK8sCluster:
		return true
	default:
		return false
	}
}

// buildMetadata assembles the error_metadata bag for a single event: identity,
// detailed resource attributes, severity, and custom attributes. On the RUM
// endpoint this is the only durable custom bag (top-level custom attributes are
// dropped), so all queryable custom data is nested here.
func buildMetadata(e *Event, res resource) map[string]any {
	md := make(map[string]any, len(e.Attributes)+len(res.attrs)+8)

	// Detailed resource attributes (telemetry.sdk.*, process.*, host.*, k8s.pod.*).
	for k, v := range res.attrs {
		if isSessionLevelKey(k) {
			continue
		}
		md[k] = v
	}

	// Identity as dotted keys.
	setIfNonEmpty(md, "user.id", e.User.ID)
	setIfNonEmpty(md, "user.email", e.User.Email)
	setIfNonEmpty(md, "user.name", e.User.Name)
	setIfNonEmpty(md, "user.organization", e.User.Organization)
	setIfNonEmpty(md, "session.id", e.SessionID)
	setIfNonEmpty(md, "anonymous_id", e.AnonymousID)

	// Severity.
	md["level"] = string(e.Level)
	md["severity_number"] = e.Level.severityNumber()

	// Custom attributes (sanitized). Applied before the reserved gc.* keys so
	// the SDK-managed namespace always wins.
	for k, v := range e.Attributes {
		md[k] = sanitizeValue(v, 0)
	}

	// Reserved gc.* namespace: the human-readable display title (separate from
	// the opaque error_fingerprint grouping key).
	if e.Title != "" {
		md["gc.title"] = e.Title
	}
	return md
}

func setIfNonEmpty(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// toWireEvent converts an internal Event to its wire representation.
func toWireEvent(e *Event, res resource) wireEvent {
	frames := make([]wireFrame, 0, len(e.Stacktrace))
	for _, f := range e.Stacktrace {
		frames = append(frames, wireFrame{
			Filename: f.File,
			Function: f.Function,
			Lineno:   f.Line,
			Colno:    0,
		})
	}
	return wireEvent{
		Type:         e.Type,
		Level:        string(e.Level),
		Timestamp:    e.Timestamp.UnixNano(),
		ID:           e.ID,
		SpanID:       newSpanID(),
		ParentSpanID: "",
		TraceID:      newTraceID(),
		Attributes: wireAttributes{
			ErrorType:        e.ErrorType,
			ErrorMessage:     e.ErrorMessage,
			ErrorHandled:     e.ErrorHandled,
			ErrorStacktrace:  frames,
			ErrorFingerprint: e.Fingerprint,
			ErrorMetadata:    buildMetadata(e, res),
		},
	}
}

// encodeBatch serializes a batch of events into the RUM ingestion body.
func encodeBatch(events []*Event, res resource) ([]byte, error) {
	payload := wirePayload{
		SessionAttributes: res.sessionAttributes(),
		Events:            make([]wireEvent, 0, len(events)),
	}
	for _, e := range events {
		payload.Events = append(payload.Events, toWireEvent(e, res))
	}
	return json.Marshal(payload)
}

// estimateSize returns a cheap byte estimate of an event for the buffer's byte
// budget. It intentionally over- rather than under-estimates.
func estimateSize(e *Event) int {
	const base = 256
	const perFrameOverhead = 24
	size := base + len(e.Type) + len(e.ErrorType) + len(e.ErrorMessage) + len(e.Fingerprint) + len(e.Title)
	for _, f := range e.Stacktrace {
		size += len(f.Function) + len(f.File) + perFrameOverhead
	}
	for k, v := range e.Attributes {
		size += len(k) + estimateValueSize(v)
	}
	size += len(e.User.ID) + len(e.User.Email) + len(e.User.Name) + len(e.User.Organization)
	return size
}

func estimateValueSize(v any) int {
	switch val := v.(type) {
	case string:
		return len(val)
	case nil:
		return scalarSizeEstimate
	case map[string]any:
		return estimateMapSize(val)
	case Attributes:
		return estimateMapSize(val)
	case []any:
		total := 2
		for _, item := range val {
			total += estimateValueSize(item)
		}
		return total
	default:
		return scalarSizeEstimate
	}
}

func estimateMapSize(m map[string]any) int {
	total := 2
	for k, item := range m {
		total += len(k) + estimateValueSize(item)
	}
	return total
}
