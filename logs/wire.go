package logs

import (
	"bytes"
	"encoding/json"
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
	for key := range out {
		if reserved(key) {
			delete(out, key)
		}
	}
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
	for key, value := range attributes {
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

func estimateSize(record *Record) int {
	const base = 192
	size := base + len(record.Message)
	for key, value := range record.Attributes {
		size += len(key) + estimateValue(value)
	}
	return size + len(record.User.ID) + len(record.User.Email) +
		len(record.User.Name) + len(record.User.Organization)
}

func estimateValue(value any) int {
	switch typed := value.(type) {
	case string:
		return len(typed)
	case map[string]any:
		total := 2
		for key, item := range typed {
			total += len(key) + estimateValue(item)
		}
		return total
	case Attributes:
		return estimateValue(map[string]any(typed))
	case []any:
		total := 2
		for _, item := range typed {
			total += estimateValue(item)
		}
		return total
	default:
		return 16
	}
}
