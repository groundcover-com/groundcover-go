package groundcover

import (
	"strings"
	"unicode"
)

// maxTitleLen caps the computed display title length (in runes).
const maxTitleLen = 256

// messageErrorType is the ErrorType marker used for CaptureMessage events.
const messageErrorType = "message"

// titleFor derives a human-readable, single-line display title from an event's
// type and message, rendered as "{type}: {value}", with the
// message reduced to a single line and the whole title length-capped. Non-error
// notices (CaptureMessage) use the bare message.
func titleFor(e *Event) string {
	msg := collapseWhitespace(e.ErrorMessage)
	switch {
	case e.ErrorType == "" || e.ErrorType == messageErrorType:
		return truncateTitle(msg, maxTitleLen)
	case msg == "":
		return truncateTitle(e.ErrorType, maxTitleLen)
	default:
		return truncateTitle(e.ErrorType+": "+msg, maxTitleLen)
	}
}

// collapseWhitespace replaces every run of Unicode whitespace (including
// newlines and tabs) with a single space and trims the ends.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inSpace = true
			continue
		}
		if inSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// truncateTitle shortens s to at most maxRunes runes, appending an ellipsis when
// it cuts.
func truncateTitle(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return "\u2026"
	}
	return string(r[:maxRunes-1]) + "\u2026"
}
