// Package fiber provides Fiber middleware that recovers panics, captures them
// (and optionally errors returned from handlers) through the core SDK, and
// seeds a fresh request scope. It is a separate module so the fiber dependency
// never enters the core SDK's go.sum.
//
// Register Fiber's own recover middleware before this one so re-raised panics
// are turned into 500 responses instead of crashing the process (Fiber, unlike
// net/http, does not recover handler panics by itself). If no recovery is
// installed, the middleware still performs a bounded, best-effort flush before
// re-raising so the panic event survives the crash:
//
//	app.Use(recover.New()) // github.com/gofiber/fiber/v2/middleware/recover
//	app.Use(gcfiber.New(gcfiber.Options{}))
package fiber

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the middleware. The zero value is valid and captures
// panics only, mirroring Sentry's Fiber integration: the panic is captured as
// an unhandled error and re-raised so Fiber's recover middleware (or the
// process) handles it exactly as it would without the middleware.
type Options struct {
	// CaptureHandlerErrors turns ON capturing errors returned from handlers as
	// handled errors. Off by default: only panics are captured unless this is
	// set. *fiber.Error values with a status code below 500 (404s, validation
	// failures, and other client errors) are never captured: they are request
	// outcomes, not application faults.
	CaptureHandlerErrors bool

	// DisableRepanic turns OFF re-raising the panic after capture, so the
	// middleware swallows it instead (the handler chain then returns no
	// error and Fiber finalizes the response as-is, an empty 200 when nothing
	// was written). Leave this off when Fiber's recover middleware is
	// installed: re-raising lets it turn the panic into a 500 as usual.
	DisableRepanic bool
}

// New returns Fiber middleware that reports to the package-level default
// client configured with the core SDK's Init. Panics are captured as unhandled
// errors and re-raised (unless Options.DisableRepanic is set); returned
// handler errors are captured as handled errors when
// Options.CaptureHandlerErrors is set, excluding client-side HTTP errors.
func New(opts Options) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.SetUserContext(gc.WithIsolatedScope(c.UserContext()))

		defer func() {
			if rec := recover(); rec != nil {
				gc.CaptureRecovered(c.UserContext(), rec, panicAttributes(c))
				if !opts.DisableRepanic {
					// Fiber has no built-in recovery: unless recover.New() (or
					// similar) is installed above us, the re-raised panic kills
					// the process and the async queue with it. Flush (bounded,
					// best-effort) so the event survives the crash.
					flushBestEffort()
					panic(rec)
				}
			}
		}()

		err := c.Next()
		if opts.CaptureHandlerErrors && err != nil && isServerError(err) {
			gc.CaptureError(c.UserContext(), err, errorAttributes(c, err))
		}
		return err
	}
}

// isServerError reports whether err represents an application fault worth
// capturing. HTTP errors below 500 are normal request outcomes (not found,
// bad input, auth failures) and are skipped.
func isServerError(err error) bool {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code >= fiber.StatusInternalServerError
	}
	return true
}

// panicAttributes derives attributes on the panic path. The response has not
// been finalized yet: unless the handler already set a status or wrote a body,
// the status the request ends with is the 500 the recovery layer produces (or
// the process dies), not the in-flight default.
func panicAttributes(c *fiber.Ctx) gc.Option {
	status := c.Response().StatusCode()
	if status == fiber.StatusOK && len(c.Response().Body()) == 0 {
		status = fiber.StatusInternalServerError
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Method(),
		"url.path":                  c.Path(),
		"http.route":                routePath(c),
		"http.response.status_code": status,
	})
}

// errorAttributes derives the response status from the returned error: Fiber's
// error handler has not written the response yet at capture time, so the
// in-flight response status would be misleading.
func errorAttributes(c *fiber.Ctx, err error) gc.Option {
	status := fiber.StatusInternalServerError
	var fe *fiber.Error
	if errors.As(err, &fe) {
		status = fe.Code
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Method(),
		"url.path":                  c.Path(),
		"http.route":                routePath(c),
		"http.response.status_code": status,
	})
}

func routePath(c *fiber.Ctx) string {
	if r := c.Route(); r != nil && r.Path != "" {
		return r.Path
	}
	return c.Path()
}

// panicFlushTimeout bounds the best-effort flush performed before re-raising a
// panic that may take the process down (mirrors the core SDK's Recover).
const panicFlushTimeout = 2 * time.Second

func flushBestEffort() {
	ctx, cancel := context.WithTimeout(context.Background(), panicFlushTimeout)
	defer cancel()
	_ = gc.Flush(ctx)
}
