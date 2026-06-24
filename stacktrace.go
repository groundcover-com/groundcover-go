package groundcover

import (
	"runtime"
	"strings"
)

// captureStack walks the current goroutine's stack, skipping the first skip
// frames (relative to captureStack's caller) and capturing at most maxDepth
// frames. inAppPrefix is the main module path used to classify in-app frames.
func captureStack(skip, maxDepth int, inAppPrefix string) []Frame {
	if maxDepth <= 0 {
		maxDepth = 128
	}
	// +2 to skip runtime.Callers and captureStack itself.
	pcs := make([]uintptr, maxDepth)
	n := runtime.Callers(skip+2, pcs)
	if n == 0 {
		return nil
	}
	frames := runtime.CallersFrames(pcs[:n])

	out := make([]Frame, 0, n)
	for {
		fr, more := frames.Next()
		if fr.Function != "" || fr.File != "" {
			out = append(out, Frame{
				Function: fr.Function,
				File:     fr.File,
				Line:     fr.Line,
				InApp:    isInApp(fr.Function, fr.File, inAppPrefix),
			})
		}
		if !more {
			break
		}
	}
	return out
}

// isInApp reports whether a frame belongs to the application: its function lives
// under the main module path and it is not vendored or part of the runtime.
func isInApp(function, file, inAppPrefix string) bool {
	if strings.Contains(file, "/vendor/") {
		return false
	}
	if inAppPrefix == "" {
		// Without a known module path, treat non-stdlib, non-runtime frames as
		// in-app on a best-effort basis.
		return !strings.HasPrefix(function, "runtime.") && strings.Contains(function, ".")
	}
	return strings.HasPrefix(function, inAppPrefix)
}

// inAppFrames returns only the in-app frames of e, preserving order.
func inAppFrames(frames []Frame) []Frame {
	out := make([]Frame, 0, len(frames))
	for _, f := range frames {
		if f.InApp {
			out = append(out, f)
		}
	}
	return out
}
