package logs

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
)

const timestampFormat = "2006-01-02T15:04:05.999999999Z07:00"

func encodeBatch(records []*Record, res resource) ([]byte, error) {
	var buf bytes.Buffer
	for _, record := range records {
		line, err := json.Marshal(buildWireRecord(record, res))
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func buildWireRecord(record *Record, res resource) map[string]any {
	out := make(map[string]any, len(record.Attributes)+len(res.attrs)+12)
	flatten(out, "", record.Attributes)
	relocateReserved(out)
	for key, value := range res.attrs {
		out[key] = value
	}
	set(out, "user.id", record.User.ID)
	set(out, "user.email", record.User.Email)
	set(out, "user.name", record.User.Name)
	set(out, "user.organization", record.User.Organization)
	set(out, "session.id", record.SessionID)
	set(out, "anonymous_id", record.AnonymousID)
	out["timestamp"] = record.Timestamp.UTC().Format(timestampFormat)
	out["msg"] = record.Message
	out["level"] = record.Level.wire()
	out["severity_number"] = record.Level.severityNumber()
	out["service"] = res.service
	out["env"] = res.env
	out["cluster"] = res.cluster
	out["source"] = res.source
	return out
}

// reservedKeyPrefix namespaces a caller attribute whose key is reserved. Keys
// like "text" or "body" are ordinary attribute names at existing call sites, so
// the value is kept under a prefixed key rather than discarded.
const reservedKeyPrefix = "attributes."

// relocateReserved moves attributes whose keys are reserved out of the way.
// When the first prefixed destination is already taken, it keeps prefixing
// until it finds a free key so neither caller value is silently discarded.
func relocateReserved(out map[string]any) {
	var collisions []string
	for key := range out {
		if reserved(key) {
			collisions = append(collisions, key)
		}
	}
	for _, key := range collisions {
		value := out[key]
		delete(out, key)
		relocated := reservedKeyPrefix + key
		for {
			if _, taken := out[relocated]; !taken {
				out[relocated] = value
				break
			}
			relocated = reservedKeyPrefix + relocated
		}
	}
}

// reserved reports whether a key is claimed by the intake or the SDK. The
// intake extracts the message from message/msg/log/body/content/text and the
// timestamp from timestamp/time/ts, so an attribute under one of those names
// would silently become the record's message or time.
func reserved(key string) bool {
	switch key {
	case "timestamp", "time", "ts",
		"message", "msg", "log", "body", "content", "text",
		"level", "severity_number", "service", "env", "cluster", "source",
		"user.id", "user.email", "user.name", "user.organization",
		"session.id", "anonymous_id":
		return true
	default:
		return false
	}
}

func flatten(out map[string]any, prefix string, attributes map[string]any) {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := attributes[key]
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok {
			flatten(out, fullKey, nested)
			continue
		}
		out[fullKey] = value
	}
}

func set(out map[string]any, key, value string) {
	if value != "" {
		out[key] = value
	}
}

// encodedSize returns the exact NDJSON byte cost of one sanitized record.
// Marshal failures are treated as oversized so the record is rejected before
// it can poison a batch.
func encodedSize(record *Record, res resource) int {
	line, err := json.Marshal(buildWireRecord(record, res))
	if err != nil {
		return math.MaxInt
	}
	return len(line) + 1
}
