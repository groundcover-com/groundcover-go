package metrics

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// sample is one parsed exposition sample line.
type sample struct {
	name      string
	labels    []label
	value     string
	timestamp string
}

// renderer accumulates the exposition body for one push, injecting the fixed
// labels into every sample line, deduplicating final series across sources,
// and enforcing the configured limits.
type renderer struct {
	cfg   Config
	fixed []label
	out   bytes.Buffer
	// seen maps the final series identity (name plus injected labels) to the
	// name of the source that produced it first.
	seen   map[string]string
	series int

	// Per-push counters, transferred into the client stats after rendering.
	malformed  uint64
	duplicates uint64
	overrides  uint64
}

// addSource appends one source's exposition text to the body. Comment lines
// are preserved verbatim, sample lines get the fixed labels injected, and
// malformed sample lines and duplicate series are dropped and counted.
//
// srcErrs reports the source's non-fatal failures, bounded to the first
// malformed line and the first dropped duplicate (all occurrences are
// counted). fatal aborts the whole push: a limit violation, or a duplicate
// series when Config.FailOnDuplicateSeries is set.
func (r *renderer) addSource(sourceName string, text []byte) (srcErrs []error, fatal error) {
	lineNo := 0
	var malformedReported, duplicateReported bool
	for len(text) > 0 {
		lineNo++
		line := text
		if idx := bytes.IndexByte(text, '\n'); idx >= 0 {
			line = text[:idx]
			text = text[idx+1:]
		} else {
			text = nil
		}
		line = bytes.TrimRight(line, "\r")
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if line[0] == '#' {
			r.out.Write(line)
			r.out.WriteByte('\n')
			if r.out.Len() > r.cfg.MaxBodyBytes {
				return srcErrs, &LimitError{Limit: limitMaxBodyBytes, Max: r.cfg.MaxBodyBytes, Actual: r.out.Len()}
			}
			continue
		}
		parsed, err := parseSampleLine(string(line))
		if err != nil {
			r.malformed++
			if !malformedReported {
				malformedReported = true
				srcErrs = append(srcErrs, fmt.Errorf("metric source %q: malformed sample at line %d", sourceName, lineNo))
			}
			continue
		}
		dup, err := r.addSample(sourceName, parsed)
		if err != nil {
			return srcErrs, err
		}
		if dup != nil && !duplicateReported {
			duplicateReported = true
			srcErrs = append(srcErrs, dup)
		}
	}
	return srcErrs, nil
}

// addSample merges the fixed labels into one parsed sample, enforces the
// per-series limits, deduplicates the final series, and appends the rendered
// line. A dropped duplicate is returned as dup; fatal errors abort the push.
func (r *renderer) addSample(sourceName string, s sample) (dup *DuplicateSeriesError, fatal error) {
	merged, overridden := mergeLabels(s.labels, r.fixed)
	r.overrides += overridden
	if len(merged) > r.cfg.MaxLabelsPerSeries {
		return nil, &LimitError{Limit: limitMaxLabelsPerSeries, Max: r.cfg.MaxLabelsPerSeries, Actual: len(merged)}
	}
	for _, l := range merged {
		if len(l.name) > r.cfg.MaxLabelNameBytes {
			return nil, &LimitError{Limit: limitMaxLabelNameBytes, Max: r.cfg.MaxLabelNameBytes, Actual: len(l.name)}
		}
		if len(l.value) > r.cfg.MaxLabelValueBytes {
			return nil, &LimitError{Limit: limitMaxLabelValueBytes, Max: r.cfg.MaxLabelValueBytes, Actual: len(l.value)}
		}
	}

	var b strings.Builder
	b.Grow(len(s.name) + len(s.value) + len(s.timestamp) + 16*len(merged) + 4)
	b.WriteString(s.name)
	writeLabels(&b, merged)
	identity := b.String()
	if first, exists := r.seen[identity]; exists {
		r.duplicates++
		dup = &DuplicateSeriesError{Series: identity, FirstSource: first, DuplicateSource: sourceName}
		if r.cfg.FailOnDuplicateSeries {
			return nil, dup
		}
		return dup, nil
	}

	if r.series+1 > r.cfg.MaxSeries {
		return nil, &LimitError{Limit: limitMaxSeries, Max: r.cfg.MaxSeries, Actual: r.series + 1}
	}
	r.seen[identity] = sourceName
	b.WriteByte(' ')
	b.WriteString(s.value)
	if s.timestamp != "" {
		b.WriteByte(' ')
		b.WriteString(s.timestamp)
	}
	b.WriteByte('\n')
	r.out.WriteString(b.String())
	r.series++
	if r.out.Len() > r.cfg.MaxBodyBytes {
		return nil, &LimitError{Limit: limitMaxBodyBytes, Max: r.cfg.MaxBodyBytes, Actual: r.out.Len()}
	}
	return nil, nil
}

// mergeLabels overlays the fixed labels on the source labels. Fixed labels
// replace same-named source labels; overridden reports how many source
// labels were replaced. The result is sorted by name and never contains
// duplicates.
func mergeLabels(source, fixed []label) (merged []label, overridden uint64) {
	byName := make(map[string]string, len(source)+len(fixed))
	for _, l := range source {
		byName[l.name] = l.value
	}
	for _, l := range fixed {
		if _, exists := byName[l.name]; exists {
			overridden++
		}
		byName[l.name] = l.value
	}
	merged = make([]label, 0, len(byName))
	for name, value := range byName {
		merged = append(merged, label{name: name, value: value})
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].name < merged[j].name })
	return merged, overridden
}

// parseSampleLine parses one Prometheus text-format sample line:
//
//	name[{label="value",...}] value [timestamp]
//
// Label values may contain escaped quotes, backslashes, and newlines. The
// value and optional timestamp are validated but kept verbatim.
func parseSampleLine(line string) (sample, error) {
	i := 0
	for i < len(line) && isMetricNameChar(line[i], i == 0) {
		i++
	}
	name := line[:i]
	if !isValidMetricName(name) {
		return sample{}, fmt.Errorf("metrics: invalid metric name in %q", line)
	}
	var labels []label
	if i < len(line) && line[i] == '{' {
		var err error
		labels, i, err = parseLabels(line, i+1)
		if err != nil {
			return sample{}, err
		}
	}
	rest := strings.TrimSpace(line[i:])
	if rest == "" || (i < len(line) && line[i] != ' ' && line[i] != '\t') {
		return sample{}, fmt.Errorf("metrics: missing value in %q", line)
	}
	fields := strings.Fields(rest)
	if len(fields) > 2 {
		return sample{}, fmt.Errorf("metrics: trailing garbage in %q", line)
	}
	if _, err := strconv.ParseFloat(fields[0], 64); err != nil {
		return sample{}, fmt.Errorf("metrics: invalid sample value in %q", line)
	}
	s := sample{name: name, labels: labels, value: fields[0]}
	if len(fields) == 2 {
		if _, err := strconv.ParseInt(fields[1], 10, 64); err != nil {
			return sample{}, fmt.Errorf("metrics: invalid timestamp in %q", line)
		}
		s.timestamp = fields[1]
	}
	return s, nil
}

// parseLabels parses a {name="value",...} block starting just after the
// opening brace. It returns the decoded labels and the index just past the
// closing brace.
func parseLabels(line string, i int) ([]label, int, error) {
	var labels []label
	for {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i < len(line) && line[i] == '}' {
			return labels, i + 1, nil
		}
		start := i
		for i < len(line) && isLabelNameChar(line[i], i == start) {
			i++
		}
		name := line[start:i]
		if !isValidLabelName(name) {
			return nil, 0, fmt.Errorf("metrics: invalid label name in %q", line)
		}
		if i >= len(line) || line[i] != '=' {
			return nil, 0, fmt.Errorf("metrics: expected '=' in %q", line)
		}
		i++
		if i >= len(line) || line[i] != '"' {
			return nil, 0, fmt.Errorf("metrics: expected '\"' in %q", line)
		}
		i++
		value, next, err := parseQuotedValue(line, i)
		if err != nil {
			return nil, 0, err
		}
		i = next
		labels = append(labels, label{name: name, value: value})
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			return nil, 0, fmt.Errorf("metrics: unterminated label block in %q", line)
		}
		switch line[i] {
		case ',':
			i++
		case '}':
			return labels, i + 1, nil
		default:
			return nil, 0, fmt.Errorf("metrics: expected ',' or '}' in %q", line)
		}
	}
}

// parseQuotedValue decodes a label value starting just after the opening
// quote, handling \\, \", and \n escapes. It returns the decoded value and
// the index just past the closing quote.
func parseQuotedValue(line string, i int) (string, int, error) {
	var b strings.Builder
	for i < len(line) {
		switch line[i] {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+1 >= len(line) {
				return "", 0, fmt.Errorf("metrics: dangling escape in %q", line)
			}
			switch line[i+1] {
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case 'n':
				b.WriteByte('\n')
			default:
				return "", 0, fmt.Errorf("metrics: unsupported escape in %q", line)
			}
			i += 2
		default:
			b.WriteByte(line[i])
			i++
		}
	}
	return "", 0, fmt.Errorf("metrics: unterminated label value in %q", line)
}

// isMetricNameChar reports whether c may appear in a metric name at the given
// position.
func isMetricNameChar(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c == ':':
		return true
	case c >= '0' && c <= '9':
		return !first
	default:
		return false
	}
}

// isLabelNameChar reports whether c may appear in a label name at the given
// position.
func isLabelNameChar(c byte, first bool) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		return true
	case c >= '0' && c <= '9':
		return !first
	default:
		return false
	}
}
