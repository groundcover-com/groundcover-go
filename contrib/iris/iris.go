// Package iris provides Iris middleware that recovers panics, captures them (and
// any errors recorded on the Iris context) through groundcover, and seeds a fresh
// request scope. It is a separate module so the iris dependency never enters
// the core SDK's go.sum.
package iris

import (
	"context"

	"github.com/kataras/iris/v12"

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

// WithErrorCapture toggles capturing errors recorded on the Iris context (via
// StopWithError and similar) as handled errors. Enabled by default.
func WithErrorCapture(enabled bool) Option {
	return func(cfg *config) { cfg.captureError = enabled }
}

// Middleware returns Iris middleware. Panics are captured as unhandled errors and
// re-raised (so Iris's own recovery, if installed, still runs); context errors
// are captured as handled errors.
func Middleware(opts ...Option) iris.Handler {
	cfg := config{captureError: true}
	for _, o := range opts {
		o(&cfg)
	}

	return func(ctx iris.Context) {
		req := ctx.Request()
		seeded := seedScope(req.Context(), cfg.client)
		req = req.WithContext(seeded)
		ctx.ResetRequest(req)

		defer func() {
			if rec := recover(); rec != nil {
				captureRecovered(ctx.Request().Context(), cfg.client, rec, requestAttributes(ctx))
				panic(rec)
			}
		}()

		ctx.Next()

		if cfg.captureError {
			if err := ctx.GetErr(); err != nil {
				captureError(ctx.Request().Context(), cfg.client, err, requestAttributes(ctx))
			}
		}
	}
}

func requestAttributes(ctx iris.Context) gc.Option {
	route := ctx.Path()
	if r := ctx.GetCurrentRoute(); r != nil {
		route = r.Path()
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       ctx.Method(),
		"url.path":                  ctx.Path(),
		"http.route":                route,
		"http.response.status_code": ctx.GetStatusCode(),
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
