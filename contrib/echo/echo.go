// Package echo provides Echo middleware that recovers panics, captures them
// (and optionally errors returned from handlers) through the core SDK, and
// seeds a fresh request scope. It is a separate module so the echo dependency
// never enters the core SDK's go.sum.
//
// Register Echo's own Recover middleware before this one so re-raised panics
// are turned into 500 responses instead of aborting the connection:
//
//	e.Use(middleware.Recover()) // github.com/labstack/echo/v4/middleware
//	e.Use(gcecho.New(gcecho.Options{}))
package echo

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the middleware. The zero value is valid and captures
// panics only: the panic is captured as an unhandled error and re-raised so
// Echo's own recovery (or the server) handles the response exactly as it
// would without the middleware.
type Options struct {
	// CaptureHandlerErrors turns ON capturing errors returned from handlers as
	// handled errors. Off by default: only panics are captured unless this is
	// set. *echo.HTTPError values with a status code below 500 (404s,
	// validation failures, and other client errors) are never captured: they
	// are request outcomes, not application faults.
	CaptureHandlerErrors bool

	// DisableRepanic turns OFF re-raising the panic after capture, so the
	// middleware swallows it instead (the handler chain then returns no
	// error). Leave this off when Echo's Recover middleware is installed:
	// re-raising lets it turn the panic into a 500 as usual. Panics with
	// http.ErrAbortHandler are always re-raised, never captured.
	DisableRepanic bool
}

// New returns Echo middleware that reports to the package-level default client
// configured with the core SDK's Init. Panics are captured as unhandled errors
// and re-raised (unless Options.DisableRepanic is set); returned handler
// errors are captured as handled errors when Options.CaptureHandlerErrors is
// set, excluding client-side HTTP errors.
func New(opts Options) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.SetRequest(c.Request().WithContext(gc.WithIsolatedScope(c.Request().Context())))

			defer func() {
				if rec := recover(); rec != nil {
					if isAbortPanic(rec) {
						panic(rec) // deliberate quiet abort: re-raise, never capture
					}
					gc.CaptureRecovered(c.Request().Context(), rec, panicAttributes(c, !opts.DisableRepanic))
					if !opts.DisableRepanic {
						panic(rec) // re-raise to Echo's recovery / the server
					}
				}
			}()

			err := next(c)
			if opts.CaptureHandlerErrors && err != nil && isServerError(err) {
				gc.CaptureError(c.Request().Context(), err, errorAttributes(c, err))
			}
			return err
		}
	}
}

// isAbortPanic reports whether the recovered value is net/http's deliberate
// abort sentinel (http.ErrAbortHandler): a request outcome, not a fault.
func isAbortPanic(rec any) bool {
	err, ok := rec.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}

// isServerError reports whether err represents an application fault worth
// capturing. HTTP errors below 500 are normal request outcomes (not found,
// bad input, auth failures) and are skipped.
func isServerError(err error) bool {
	var he *echo.HTTPError
	if errors.As(err, &he) {
		return he.Code >= http.StatusInternalServerError
	}
	return true
}

// panicAttributes derives attributes on the panic path. When the panic is
// re-raised (willRepanic) and no response was committed, the status the client
// receives is the 500 Echo's recovery produces, not the in-flight default, so
// that is what is reported. On the swallow path (DisableRepanic) the response
// is finalized as-is, so the in-flight status is kept.
func panicAttributes(c echo.Context, willRepanic bool) gc.Option {
	status := c.Response().Status
	if willRepanic && !c.Response().Committed {
		status = http.StatusInternalServerError
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Request().Method,
		"url.path":                  c.Request().URL.Path,
		"http.route":                c.Path(),
		"http.response.status_code": status,
	})
}

// errorAttributes derives the response status from the returned error: Echo's
// error handler has not written the response yet at capture time, so the
// in-flight Response().Status would be misleading.
func errorAttributes(c echo.Context, err error) gc.Option {
	status := http.StatusInternalServerError
	var he *echo.HTTPError
	if errors.As(err, &he) {
		status = he.Code
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Request().Method,
		"url.path":                  c.Request().URL.Path,
		"http.route":                c.Path(),
		"http.response.status_code": status,
	})
}
