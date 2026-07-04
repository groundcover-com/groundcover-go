// Package echo provides Echo middleware that recovers panics, captures them (and
// server-fault errors returned from handlers) through the SDK, and seeds a
// fresh request scope. It is a separate module so the echo dependency never
// enters the core SDK's go.sum.
//
// Register Echo's own Recover middleware before this one so re-raised panics
// are turned into 500 responses instead of aborting the connection:
//
//	e.Use(middleware.Recover()) // github.com/labstack/echo/v4/middleware
//	e.Use(gcecho.Middleware())
package echo

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	gc "github.com/groundcover-com/groundcover-go"
)

type config struct {
	client       *gc.Client
	captureError bool
}

// Option configures the middleware.
type Option func(*config)

// WithClient routes captures to an explicit client instead of the global one.
func WithClient(c *gc.Client) Option {
	return func(cfg *config) { cfg.client = c }
}

// WithErrorCapture toggles capturing errors returned from handlers as handled
// errors. Enabled by default. *echo.HTTPError values with a status code below
// 500 (404s, validation failures, and other client errors) are never captured:
// they are request outcomes, not application faults.
func WithErrorCapture(enabled bool) Option {
	return func(cfg *config) { cfg.captureError = enabled }
}

// Middleware returns Echo middleware. Panics are captured as unhandled errors and
// re-raised (so Echo's own recovery, if installed, still runs); panics with
// http.ErrAbortHandler are re-raised without capture. Returned handler errors
// are captured as handled errors unless they are client-side HTTP errors (see
// WithErrorCapture).
func Middleware(opts ...Option) echo.MiddlewareFunc {
	cfg := config{captureError: true}
	for _, o := range opts {
		o(&cfg)
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := seedScope(c.Request().Context(), cfg.client)
			c.SetRequest(c.Request().WithContext(ctx))

			defer func() {
				if rec := recover(); rec != nil {
					if !isAbortPanic(rec) {
						captureRecovered(c.Request().Context(), cfg.client, rec, requestAttributes(c))
					}
					panic(rec) // re-raise to Echo's recovery / the server
				}
			}()

			err := next(c)
			if cfg.captureError && err != nil && isServerError(err) {
				captureError(c.Request().Context(), cfg.client, err, errorAttributes(c, err))
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

func requestAttributes(c echo.Context) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Request().Method,
		"url.path":                  c.Request().URL.Path,
		"http.route":                c.Path(),
		"http.response.status_code": c.Response().Status,
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

func seedScope(ctx context.Context, client *gc.Client) context.Context {
	if client != nil {
		return client.WithIsolatedScope(ctx)
	}
	return gc.WithIsolatedScope(ctx)
}

func captureRecovered(ctx context.Context, client *gc.Client, rec any, opts ...gc.Option) {
	if client != nil {
		client.CaptureRecovered(ctx, rec, opts...)
		return
	}
	gc.CaptureRecovered(ctx, rec, opts...)
}

func captureError(ctx context.Context, client *gc.Client, err error, opts ...gc.Option) {
	if client != nil {
		client.CaptureError(ctx, err, opts...)
		return
	}
	gc.CaptureError(ctx, err, opts...)
}
