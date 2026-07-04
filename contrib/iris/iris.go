// Package iris provides Iris middleware that recovers panics, captures them
// (and optionally errors recorded on the Iris context) through the core SDK,
// and seeds a fresh request scope. It is a separate module so the iris
// dependency never enters the core SDK's go.sum.
//
// Register Iris's own recover middleware before this one so re-raised panics
// are turned into 500 responses instead of aborting the connection:
//
//	app.Use(recover.New()) // github.com/kataras/iris/v12/middleware/recover
//	app.Use(gciris.New(gciris.Options{}))
package iris

import (
	"errors"
	"net/http"

	"github.com/kataras/iris/v12"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the middleware. The zero value is valid and captures
// panics only, mirroring Sentry's Iris integration: the panic is captured as an
// unhandled error and re-raised so Iris's own recovery (or the server) handles
// the response exactly as it would without the middleware.
type Options struct {
	// CaptureContextErrors turns ON capturing errors recorded on the Iris
	// context (via StopWithError and similar) as handled errors. Off by
	// default: only panics are captured unless this is set. Errors recorded
	// with a 4xx status code (validation failures, not-found, and other client
	// errors) are never captured: they are request outcomes, not application
	// faults.
	CaptureContextErrors bool

	// DisableRepanic turns OFF re-raising the panic after capture, so the
	// middleware swallows it instead. Leave this off when Iris's recover
	// middleware is installed: re-raising lets it turn the panic into a 500 as
	// usual. Panics with http.ErrAbortHandler are always re-raised, never
	// captured.
	DisableRepanic bool
}

// New returns Iris middleware that reports to the package-level default client
// configured with the core SDK's Init. Panics are captured as unhandled errors
// and re-raised (unless Options.DisableRepanic is set); context errors are
// captured as handled errors when Options.CaptureContextErrors is set,
// excluding errors recorded with a client-error status.
func New(opts Options) iris.Handler {
	return func(ctx iris.Context) {
		req := ctx.Request()
		ctx.ResetRequest(req.WithContext(gc.WithIsolatedScope(req.Context())))

		defer func() {
			if rec := recover(); rec != nil {
				if isAbortPanic(rec) {
					panic(rec) // deliberate quiet abort: re-raise, never capture
				}
				gc.CaptureRecovered(ctx.Request().Context(), rec, panicAttributes(ctx))
				if !opts.DisableRepanic {
					panic(rec) // re-raise to Iris's recovery / the server
				}
			}
		}()

		ctx.Next()

		if opts.CaptureContextErrors {
			if err := ctx.GetErr(); err != nil && isServerError(ctx) {
				gc.CaptureError(ctx.Request().Context(), err, requestAttributes(ctx))
			}
		}
	}
}

// isAbortPanic reports whether the recovered value is net/http's deliberate
// abort sentinel (http.ErrAbortHandler): a request outcome, not a fault.
func isAbortPanic(rec any) bool {
	err, ok := rec.(error)
	return ok && errors.Is(err, http.ErrAbortHandler)
}

// isServerError reports whether the recorded error represents an application
// fault worth capturing. Errors that resulted in a 4xx response are normal
// request outcomes (not found, bad input, auth failures) and are skipped.
func isServerError(ctx iris.Context) bool {
	status := ctx.GetStatusCode()
	return status < http.StatusBadRequest || status >= http.StatusInternalServerError
}

func requestAttributes(ctx iris.Context) gc.Option {
	return gc.WithAttributes(attributesWithStatus(ctx, ctx.GetStatusCode()))
}

// panicAttributes derives attributes on the panic path. The response has not
// been finalized yet: unless the handler already wrote a response, the status
// the request ends with is the 500 Iris's recover middleware produces, not the
// in-flight default.
func panicAttributes(ctx iris.Context) gc.Option {
	status := ctx.GetStatusCode()
	if ctx.ResponseWriter().Written() < 0 { // context.NoWritten: nothing committed yet
		status = http.StatusInternalServerError
	}
	return gc.WithAttributes(attributesWithStatus(ctx, status))
}

func attributesWithStatus(ctx iris.Context, status int) gc.Attributes {
	route := ctx.Path()
	if r := ctx.GetCurrentRoute(); r != nil {
		route = r.Path()
	}
	return gc.Attributes{
		"http.request.method":       ctx.Method(),
		"url.path":                  ctx.Path(),
		"http.route":                route,
		"http.response.status_code": status,
	}
}
