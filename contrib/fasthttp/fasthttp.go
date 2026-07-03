// Package fasthttp provides fasthttp middleware that recovers panics, captures
// them through groundcover, and seeds a fresh request scope. It is a separate
// module so the fasthttp dependency never enters the core SDK's go.sum.
//
// fasthttp has no built-in panic recovery: the middleware re-raises captured
// panics, which terminates the process unless the handler chain recovers them.
// Wrap the middleware with your own recovery handler if the process must
// survive handler panics.
//
// Handlers reach the request scope through ScopeContext:
//
//	func handle(ctx *fasthttp.RequestCtx) {
//		gcctx := gcfasthttp.ScopeContext(ctx)
//		gc.SetUser(gcctx, gc.User{ID: "u-1"})
//		if err := charge(ctx); err != nil {
//			gc.CaptureError(gcctx, err)
//		}
//	}
package fasthttp

import (
	"context"

	"github.com/valyala/fasthttp"

	gc "github.com/groundcover-com/groundcover-go"
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
// errors and re-raised. Each request gets an isolated scope, reachable from
// handlers via ScopeContext.
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
				captureRecovered(ScopeContext(ctx), cfg.client, rec, requestAttributes(ctx))
				panic(rec)
			}
		}()

		handler(ctx)
	}
}

// ScopeContext returns the isolated request context seeded by Middleware.
// Handlers use it to enrich the scope (SetUser, WithScope) or capture errors
// with request scope. It returns context.Background() when the middleware did
// not run for this request.
func ScopeContext(ctx *fasthttp.RequestCtx) context.Context {
	if c, ok := ctx.UserValue(scopeKey{}).(context.Context); ok {
		return c
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
