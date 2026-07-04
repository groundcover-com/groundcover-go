// Package negroni provides Negroni middleware that recovers panics, captures
// them through the core SDK, and seeds a fresh request scope. It is a separate
// module so the negroni dependency never enters the core SDK's go.sum.
package negroni

import (
	"errors"
	"net/http"

	"github.com/urfave/negroni/v3"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the middleware. The zero value is valid and captures
// panics, re-raising them after capture so Negroni's own recovery (or the
// server) handles the response exactly as it would without the middleware.
type Options struct {
	// DisableRepanic turns OFF re-raising the panic after capture, so the
	// middleware swallows it instead (the response is finalized as-is, an
	// empty 200 when nothing was written). Leave this off when Negroni's
	// Recovery middleware is installed: re-raising lets it turn the panic into
	// a 500 as usual. Panics with http.ErrAbortHandler are always re-raised,
	// never captured.
	DisableRepanic bool
}

type middleware struct {
	opts Options
}

// New returns Negroni middleware that reports to the package-level default
// client configured with the core SDK's Init. Panics are captured as unhandled
// errors and re-raised (unless Options.DisableRepanic is set); panics with
// http.ErrAbortHandler are re-raised without capture.
func New(opts Options) negroni.Handler {
	return &middleware{opts: opts}
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	r = r.WithContext(gc.WithIsolatedScope(r.Context()))

	defer func() {
		if rec := recover(); rec != nil {
			if isAbortPanic(rec) {
				panic(rec) // deliberate quiet abort: re-raise, never capture
			}
			gc.CaptureRecovered(r.Context(), rec, requestAttributes(r))
			if !m.opts.DisableRepanic {
				panic(rec)
			}
		}
	}()

	next(w, r)
}

// isAbortPanic reports whether the recovered value is net/http's deliberate
// abort sentinel (http.ErrAbortHandler): a request outcome, not a fault.
func isAbortPanic(rec any) bool {
	err, ok := rec.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}

func requestAttributes(r *http.Request) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method": r.Method,
		"url.path":            r.URL.Path,
		"server.address":      r.Host,
	})
}
