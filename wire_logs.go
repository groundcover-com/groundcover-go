package groundcover

import (
	"bytes"
	"encoding/json"
)

// wireLogLevel maps a Level to the direct logs intake convention, which uses
// "warn" rather than OTel's "warning".
func wireLogLevel(l Level) string {
	if l == LevelWarning {
		return "warn"
	}
	return string(l)
}

// isLogReservedKey reports whether an NDJSON field is owned by the SDK and
// must not be set by caller attributes.
func isLogReservedKey(k string) bool {
	switch k {
	case "timestamp", "msg", "message", "service", "env", "cluster", "source",
		wireKeyLevel, wireKeySeverity,
		wireKeyUserID, wireKeyUserEmail, wireKeyUserName, wireKeyUserOrg,
		wireKeySessionID, wireKeyAnonymousID:
		return true
	default:
		return false
	}
}

// buildWireLog assembles one flat NDJSON record for /json/logs. Caller
// attributes are written first; SDK-managed fields are written afterwards so
// a caller cannot override them.
func buildWireLog(l *Log, res resource, source string) map[string]any {
	record := make(map[string]any, len(l.Attributes)+len(res.attrs)+10)

	flattenInto(record, "", l.Attributes)
	// Remove reserved keys a caller may have injected (directly or via a
	// flattened nested map) before the SDK-managed values are written.
	for k := range record {
		if isLogReservedKey(k) {
			delete(record, k)
		}
	}

	for k, v := range res.attrs {
		if isSessionLevelKey(k) {
			continue
		}
		record[k] = v
	}

	setIfNonEmptyAny(record, wireKeyUserID, l.User.ID)
	setIfNonEmptyAny(record, wireKeyUserEmail, l.User.Email)
	setIfNonEmptyAny(record, wireKeyUserName, l.User.Name)
	setIfNonEmptyAny(record, wireKeyUserOrg, l.User.Organization)
	setIfNonEmptyAny(record, wireKeySessionID, l.SessionID)
	setIfNonEmptyAny(record, wireKeyAnonymousID, l.AnonymousID)

	record["timestamp"] = l.Timestamp.UTC().Format(timeRFC3339Nano)
	record["msg"] = l.Message
	record[wireKeyLevel] = wireLogLevel(l.Level)
	record[wireKeySeverity] = l.Level.severityNumber()
	record["service"] = res.serviceName
	record["env"] = res.env
	record["cluster"] = res.cluster
	record["source"] = source
	return record
}

const timeRFC3339Nano = "2006-01-02T15:04:05.999999999Z07:00"

// flattenInto writes m into out, flattening nested maps to dotted keys
// (mirroring the browser SDK's event formatting). Non-map values, including
// slices, are written as-is under their dotted key.
func flattenInto(out map[string]any, prefix string, m map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		if nested, ok := v.(map[string]any); ok {
			flattenInto(out, key, nested)
			continue
		}
		out[key] = v
	}
}

func setIfNonEmptyAny(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

// encodeLogBatch serializes a batch into newline-delimited JSON. The trailing
// newline is intentional and accepted by the logs intake.
func encodeLogBatch(logs []*Log, res resource, source string) ([]byte, error) {
	var buf bytes.Buffer
	for _, l := range logs {
		line, err := json.Marshal(buildWireLog(l, res, source))
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
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
