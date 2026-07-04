// Package fasthttp provides fasthttp middleware that recovers panics, captures
// them through the core SDK, and seeds a fresh request scope. It is a separate
// module so the fasthttp dependency never enters the core SDK's go.sum.
//
// fasthttp has no built-in panic recovery: the middleware re-raises captured
// panics (unless Options.DisableRepanic is set), which terminates the process
// unless the handler chain recovers them. Because the crash would also take
// the SDK's async queue down, the middleware performs a bounded, best-effort
// flush before re-raising so the panic event survives.
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
	"time"

	"github.com/valyala/fasthttp"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the middleware. The zero value is valid and captures
// panics, re-raising them after capture.
type Options struct {
	// DisableRepanic turns OFF re-raising the panic after capture, so the
	// middleware swallows it instead and fasthttp finalizes the response as-is
	// (an empty 200 when nothing was written). Leave this off when an outer
	// recovery handler is installed, or when the process should crash on
	// panics as it would without the middleware.
	DisableRepanic bool
}

type scopeKey struct{}

// New wraps a fasthttp.RequestHandler with middleware that reports to the
// package-level default client configured with the core SDK's Init. Panics are
// captured as unhandled errors and re-raised (unless Options.DisableRepanic is
// set). Each request gets an isolated scope, reachable from handlers via
// ScopeContext.
func New(handler fasthttp.RequestHandler, opts Options) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(scopeKey{}, gc.WithIsolatedScope(context.Background()))

		defer func() {
			if rec := recover(); rec != nil {
				gc.CaptureRecovered(ScopeContext(ctx), rec, requestAttributes(ctx))
				if !opts.DisableRepanic {
					// fasthttp has no built-in recovery: unless something above
					// us recovers, the re-raised panic kills the process and
					// the async queue with it. Flush (bounded, best-effort) so
					// the event survives the crash.
					flushBestEffort()
					panic(rec)
				}
			}
		}()

		handler(ctx)
	}
}

// ScopeContext returns the isolated request context seeded by New. Handlers
// use it to enrich the scope (SetUser, WithScope) or capture errors with
// request scope. It returns context.Background() when the middleware did not
// run for this request.
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

// panicFlushTimeout bounds the best-effort flush performed before re-raising a
// panic that may take the process down (mirrors the core SDK's Recover).
const panicFlushTimeout = 2 * time.Second

func flushBestEffort() {
	ctx, cancel := context.WithTimeout(context.Background(), panicFlushTimeout)
	defer cancel()
	_ = gc.Flush(ctx)
}
