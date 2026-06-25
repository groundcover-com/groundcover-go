package groundcover

import (
	"hash/fnv"
	"strconv"
	"strings"
	"unicode"
)

// fingerprint computes the naive v1 client-side grouping key for an event.
//
// Component priority mirrors the server pipeline: when in-app frames exist, the
// fingerprint is the error type plus the in-app frame signatures (function+file,
// no line numbers, so the group is stable across edits); otherwise it falls back
// to a normalized message with identifiers and numbers stripped.
func fingerprint(e *Event) string {
	h := fnv.New64a()

	frames := inAppFrames(e.Stacktrace)
	if len(frames) > 0 {
		_, _ = h.Write([]byte(e.ErrorType))
		for _, f := range frames {
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(f.Function))
			_, _ = h.Write([]byte{0})
			_, _ = h.Write([]byte(f.File))
		}
	} else {
		_, _ = h.Write([]byte(normalizeMessage(e.ErrorMessage)))
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// normalizeMessage collapses each maximal run of digits into a single "0" so
// that messages differing only by identifiers/counters group together when no
// stack is available.
func normalizeMessage(msg string) string {
	var b strings.Builder
	b.Grow(len(msg))
	inDigits := false
	for _, r := range msg {
		if unicode.IsDigit(r) {
			if !inDigits {
				b.WriteByte('0')
				inDigits = true
			}
			continue
		}
		inDigits = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
