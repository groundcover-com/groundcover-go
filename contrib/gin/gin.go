// Package gin provides Gin middleware that recovers panics, captures them (and
// any errors collected on the Gin context) through groundcover, and seeds a
// fresh request scope. It is a separate module so the gin dependency never
// enters the core SDK's go.sum.
package gin

import (
	"context"

	"github.com/gin-gonic/gin"

	groundcover "github.com/groundcover-com/groundcover-go"
)

type config struct {
	client       *groundcover.Client
	captureError bool
}

// Option configures the middleware.
type Option func(*config)

// WithClient routes captures to an explicit client instead of the global one.
func WithClient(c *groundcover.Client) Option {
	return func(cfg *config) { cfg.client = c }
}

// WithErrorCapture toggles capturing errors collected on the Gin context
// (c.Error(...)/c.Errors) as handled errors. Enabled by default.
func WithErrorCapture(enabled bool) Option {
	return func(cfg *config) { cfg.captureError = enabled }
}

// Middleware returns Gin middleware. Panics are captured as unhandled errors and
// re-raised (so Gin's own recovery, if installed, still runs); collected
// c.Errors are captured as handled errors.
func Middleware(opts ...Option) gin.HandlerFunc {
	cfg := config{captureError: true}
	for _, o := range opts {
		o(&cfg)
	}

	return func(c *gin.Context) {
		ctx := withScope(c.Request.Context(), cfg.client)
		c.Request = c.Request.WithContext(ctx)

		defer func() {
			if rec := recover(); rec != nil {
				captureRecovered(ctx, cfg.client, rec, requestAttributes(c))
				panic(rec) // re-raise to Gin's recovery / the server
			}
		}()

		c.Next()

		if cfg.captureError {
			for _, e := range c.Errors {
				captureError(ctx, cfg.client, e.Err, requestAttributes(c))
			}
		}
	}
}

func requestAttributes(c *gin.Context) groundcover.Option {
	return groundcover.WithAttributes(groundcover.Attributes{
		"http.request.method":       c.Request.Method,
		"url.path":                  c.Request.URL.Path,
		"http.route":                c.FullPath(),
		"http.response.status_code": c.Writer.Status(),
	})
}

func withScope(ctx context.Context, client *groundcover.Client) context.Context {
	noop := func(*groundcover.Scope) {}
	if client != nil {
		return client.WithScope(ctx, noop)
	}
	return groundcover.WithScope(ctx, noop)
}

func captureRecovered(ctx context.Context, client *groundcover.Client, rec any, opts ...groundcover.Option) {
	if client != nil {
		client.CaptureRecovered(ctx, rec, opts...)
		return
	}
	groundcover.CaptureRecovered(ctx, rec, opts...)
}

func captureError(ctx context.Context, client *groundcover.Client, err error, opts ...groundcover.Option) {
	if client != nil {
		client.CaptureError(ctx, err, opts...)
		return
	}
	groundcover.CaptureError(ctx, err, opts...)
}
