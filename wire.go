package groundcover

import (
	"encoding/json"
	"math"
)

const (
	wireKeyLevel       = "level"
	wireKeySeverity    = "severity_number"
	wireKeyUserID      = "user.id"
	wireKeyUserEmail   = "user.email"
	wireKeyUserName    = "user.name"
	wireKeyUserOrg     = "user.organization"
	wireKeySessionID   = "session.id"
	wireKeyAnonymousID = "anonymous_id"
)

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
		"userAgent":          r.userAgent,
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
//
// Caller attributes are written first; SDK-managed keys (resource, identity,
// severity, gc.title) are written afterwards so a caller cannot override them —
// e.g. inject a raw, unhashed user.id or a non-numeric severity_number. The
// event's Attributes are expected to be sanitized already (done at capture).
func buildMetadata(e *Event, res resource) map[string]any {
	md := make(map[string]any, len(e.Attributes)+len(res.attrs)+8)

	// Caller-supplied custom attributes, minus the reserved SDK-managed keys.
	for k, v := range e.Attributes {
		if isSDKManagedKey(k) {
			continue
		}
		md[k] = v
	}

	// Detailed resource attributes (telemetry.sdk.*, process.*, host.*, k8s.pod.*).
	for k, v := range res.attrs {
		if isSessionLevelKey(k) {
			continue
		}
		md[k] = v
	}

	// Identity as dotted keys.
	setIfNonEmpty(md, wireKeyUserID, e.User.ID)
	setIfNonEmpty(md, wireKeyUserEmail, e.User.Email)
	setIfNonEmpty(md, wireKeyUserName, e.User.Name)
	setIfNonEmpty(md, wireKeyUserOrg, e.User.Organization)
	setIfNonEmpty(md, wireKeySessionID, e.SessionID)
	setIfNonEmpty(md, wireKeyAnonymousID, e.AnonymousID)

	// Severity (always numeric severity_number + string level).
	md[wireKeyLevel] = string(e.Level)
	md[wireKeySeverity] = e.Level.severityNumber()

	// Reserved gc.* namespace: the human-readable display title (separate from
	// the opaque error_fingerprint grouping key).
	if e.Title != "" {
		md["gc.title"] = e.Title
	}
	return md
}

// isSDKManagedKey reports whether a metadata key is owned by the SDK and must
// not be set or overridden by caller attributes.
func isSDKManagedKey(k string) bool {
	switch k {
	case wireKeyUserID, wireKeyUserEmail, wireKeyUserName, wireKeyUserOrg,
		wireKeySessionID, wireKeyAnonymousID, wireKeyLevel, wireKeySeverity, "gc.title":
		return true
	default:
		return false
	}
}

func setIfNonEmpty(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// toWireEvent converts an internal Event to its wire representation.
func toWireEvent(e *Event, res resource) wireEvent {
	return buildWireEvent(e, res, newSpanID(), newTraceID())
}

// buildWireEvent converts e using the supplied fixed-width IDs. Keeping ID
// generation outside lets sizing use placeholders instead of reading
// crypto/rand on the caller's capture path.
func buildWireEvent(e *Event, res resource, spanID, traceID string) wireEvent {
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
		SpanID:       spanID,
		ParentSpanID: "",
		TraceID:      traceID,
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

// encodedEventSize returns the exact request size for e when sent by itself.
// Using that as the ring-buffer size safely overcounts a multi-event request,
// because each per-event measurement includes the shared payload envelope and
// session attributes. Marshal failures are treated as oversized so one bad
// event cannot poison and drop an otherwise healthy batch.
func encodedEventSize(e *Event, res resource) int {
	const (
		placeholderSpanID  = "0000000000000000"
		placeholderTraceID = "00000000000000000000000000000000"
	)
	payload := wirePayload{
		SessionAttributes: res.sessionAttributes(),
		Events: []wireEvent{
			buildWireEvent(e, res, placeholderSpanID, placeholderTraceID),
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return math.MaxInt
	}
	return len(body)
}
