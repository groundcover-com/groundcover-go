// Package fiber provides Fiber middleware that recovers panics, captures them (and
// any errors returned from handlers) through groundcover, and seeds a fresh
// request scope. It is a separate module so the fiber dependency never enters
// the core SDK's go.sum.
package fiber

import (
	"context"

	gc "github.com/groundcover-com/groundcover-go"
	"github.com/gofiber/fiber/v2"
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

// Middleware returns Fiber middleware. Panics are captured as unhandled errors and
// re-raised; returned handler errors are captured as handled errors.
func Middleware(opts ...Option) fiber.Handler {
	cfg := config{captureError: true}
	for _, o := range opts {
		o(&cfg)
	}

	return func(c *fiber.Ctx) error {
		ctx := seedScope(c.UserContext(), cfg.client)
		c.SetUserContext(ctx)

		defer func() {
			if rec := recover(); rec != nil {
				captureRecovered(c.UserContext(), cfg.client, rec, requestAttributes(c))
				panic(rec)
			}
		}()

		err := c.Next()
		if cfg.captureError && err != nil {
			captureError(c.UserContext(), cfg.client, err, requestAttributes(c))
		}
		return err
	}
}

func requestAttributes(c *fiber.Ctx) gc.Option {
	route := c.Route().Path
	if route == "" {
		route = c.Path()
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Method(),
		"url.path":                  c.Path(),
		"http.route":                route,
		"http.response.status_code": c.Response().StatusCode(),
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
