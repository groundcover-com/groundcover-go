// Package echo provides Echo middleware that recovers panics, captures them (and
// any errors returned from handlers) through groundcover, and seeds a fresh
// request scope. It is a separate module so the echo dependency never enters
// the core SDK's go.sum.
package echo

import (
	"context"

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
// errors. Enabled by default.
func WithErrorCapture(enabled bool) Option {
	return func(cfg *config) { cfg.captureError = enabled }
}

// Middleware returns Echo middleware. Panics are captured as unhandled errors and
// re-raised (so Echo's own recovery, if installed, still runs); returned handler
// errors are captured as handled errors.
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
					captureRecovered(c.Request().Context(), cfg.client, rec, requestAttributes(c))
					panic(rec) // re-raise to Echo's recovery / the server
				}
			}()

			err := next(c)
			if cfg.captureError && err != nil {
				captureError(c.Request().Context(), cfg.client, err, requestAttributes(c))
			}
			return err
		}
	}
}

func requestAttributes(c echo.Context) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Request().Method,
		"url.path":                  c.Request().URL.Path,
		"http.route":                c.Path(),
		"http.response.status_code": c.Response().Status,
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
