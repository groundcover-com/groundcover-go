package metrics

import "strings"

// Fixed label names owned by the client configuration.
const (
	labelJob      = "job"
	labelInstance = "instance"
)

// label is a single Prometheus label pair. Values are stored unescaped and
// escaped only when rendered.
type label struct {
	name  string
	value string
}

// isValidMetricName reports whether name matches [a-zA-Z_:][a-zA-Z0-9_:]*.
func isValidMetricName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == ':':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isValidLabelName reports whether name matches [a-zA-Z_][a-zA-Z0-9_]*.
func isValidLabelName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// escapeLabelValue escapes backslash, double quote, and newline per the
// Prometheus text exposition format.
func escapeLabelValue(value string) string {
	if !strings.ContainsAny(value, "\\\"\n") {
		return value
	}
	var b strings.Builder
	b.Grow(len(value) + 8)
	for i := range len(value) {
		switch value[i] {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteByte(value[i])
		}
	}
	return b.String()
}

// writeLabels renders a sorted, escaped {name="value",...} block. It writes
// nothing when labels is empty.
func writeLabels(b *strings.Builder, labels []label) {
	if len(labels) == 0 {
		return
	}
	b.WriteByte('{')
	for i, l := range labels {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.name)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(l.value))
		b.WriteByte('"')
	}
	b.WriteByte('}')
}
