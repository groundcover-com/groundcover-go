package groundcover

import (
	"fmt"
	"sort"
	"strings"
)

// debugMaxFrames bounds how many stack frames the debug renderer prints.
const debugMaxFrames = 8

// debugMaxValueLen bounds the rendered length of an attribute value.
const debugMaxValueLen = 80

// renderDebug formats a finalized event as a compact, human-readable block for
// local development. It renders the post-scrub, post-hash event, so it honors
// BeforeSend scrubbing and IdentityHasher pseudonymization.
func renderDebug(e *Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[groundcover] %s %s  %s\n", e.Level, e.Type, e.Title)
	fmt.Fprintf(&b, "  fingerprint=%s handled=%t\n", e.Fingerprint, e.ErrorHandled)

	if !e.User.isZero() {
		fmt.Fprintf(&b, "  user: %s\n", userSummary(e.User))
	}
	if len(e.Attributes) > 0 {
		fmt.Fprintf(&b, "  attrs: %s\n", attrsSummary(e.Attributes))
	}
	if n := len(e.Stacktrace); n > 0 {
		b.WriteString("  stack:\n")
		shown := min(n, debugMaxFrames)
		for _, f := range e.Stacktrace[:shown] {
			fmt.Fprintf(&b, "    %s (%s:%d)\n", f.Function, f.File, f.Line)
		}
		if n > shown {
			fmt.Fprintf(&b, "    ... %d more\n", n-shown)
		}
	}
	return b.String()
}

func userSummary(u User) string {
	parts := make([]string, 0, 4)
	if u.ID != "" {
		parts = append(parts, "id="+u.ID)
	}
	if u.Email != "" {
		parts = append(parts, "email="+u.Email)
	}
	if u.Name != "" {
		parts = append(parts, "name="+u.Name)
	}
	if u.Organization != "" {
		parts = append(parts, "org="+u.Organization)
	}
	return strings.Join(parts, " ")
}

func attrsSummary(a Attributes) string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+truncateValue(fmt.Sprintf("%v", a[k])))
	}
	return strings.Join(parts, " ")
}

func truncateValue(s string) string {
	if len(s) <= debugMaxValueLen {
		return s
	}
	return s[:debugMaxValueLen-1] + "\u2026"
}
