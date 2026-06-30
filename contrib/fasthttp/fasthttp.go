// Package fasthttp provides fasthttp middleware that recovers panics, captures
// them through groundcover, and seeds a fresh request scope. It is a separate
// module so the fasthttp dependency never enters the core SDK's go.sum.
package fasthttp

import (
	"context"

	gc "github.com/groundcover-com/groundcover-go"
	"github.com/valyala/fasthttp"
)

type config struct {
	client *gc.Client
}

// Option configures the middleware.
type Option func(*config)

// WithClient routes captures to an explicit client instead of the global one.
func WithClient(c *gc.Client) Option {
	return func(cfg *config) { cfg.client = c }
}

type scopeKey struct{}

// Middleware wraps a fasthttp.RequestHandler. Panics are captured as unhandled
// errors and re-raised.
func Middleware(handler fasthttp.RequestHandler, opts ...Option) fasthttp.RequestHandler {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}

	return func(ctx *fasthttp.RequestCtx) {
		seeded := seedScope(context.Background(), cfg.client)
		ctx.SetUserValue(scopeKey{}, seeded)

		defer func() {
			if rec := recover(); rec != nil {
				captureRecovered(scopeContext(ctx), cfg.client, rec, requestAttributes(ctx))
				panic(rec)
			}
		}()

		handler(ctx)
	}
}

func scopeContext(ctx *fasthttp.RequestCtx) context.Context {
	if v := ctx.UserValue(scopeKey{}); v != nil {
		if c, ok := v.(context.Context); ok {
			return c
		}
	}
	return context.Background()
}

func requestAttributes(ctx *fasthttp.RequestCtx) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       string(ctx.Method()),
		"url.path":                  string(ctx.Path()),
		"http.response.status_code": ctx.Response.StatusCode(),
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
