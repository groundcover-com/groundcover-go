package groundcover

import (
	"strings"
	"testing"
)

func TestTitleFor(t *testing.T) {
	cases := []struct {
		name string
		e    *Event
		want string
	}{
		{"type and message", &Event{ErrorType: "*net.OpError", ErrorMessage: "connection refused"}, "*net.OpError: connection refused"},
		{"message notice", &Event{ErrorType: messageErrorType, ErrorMessage: "stale cache"}, "stale cache"},
		{"empty type", &Event{ErrorType: "", ErrorMessage: "bare"}, "bare"},
		{"empty message", &Event{ErrorType: "*x.Err", ErrorMessage: ""}, "*x.Err"},
		{"multiline collapsed", &Event{ErrorType: "E", ErrorMessage: "line1\n\tline2   line3"}, "E: line1 line2 line3"},
	}
	for _, tc := range cases {
		if got := titleFor(tc.e); got != tc.want {
			t.Fatalf("%s: titleFor = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTitleTruncation(t *testing.T) {
	long := strings.Repeat("a", 400)
	got := titleFor(&Event{ErrorType: messageErrorType, ErrorMessage: long})
	if len([]rune(got)) != maxTitleLen {
		t.Fatalf("title length = %d, want %d", len([]rune(got)), maxTitleLen)
	}
	if !strings.HasSuffix(got, "\u2026") {
		t.Fatalf("truncated title should end with an ellipsis: %q", got[len(got)-4:])
	}
}

func TestCollapseWhitespace(t *testing.T) {
	if got := collapseWhitespace("  a\n\n b\t c  "); got != "a b c" {
		t.Fatalf("collapseWhitespace = %q", got)
	}
	if got := collapseWhitespace(""); got != "" {
		t.Fatalf("empty should stay empty, got %q", got)
	}
}

func TestTruncateTitleEdges(t *testing.T) {
	if got := truncateTitle("abc", 0); got != "abc" {
		t.Fatalf("non-positive max should return input, got %q", got)
	}
	if got := truncateTitle("abcdef", 1); got != "\u2026" {
		t.Fatalf("max=1 should be a lone ellipsis, got %q", got)
	}
	if got := truncateTitle("abc", 10); got != "abc" {
		t.Fatalf("short string unchanged, got %q", got)
	}
}
