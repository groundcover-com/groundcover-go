// Package nethttp provides net/http middleware that recovers panics, captures
// them through groundcover, and seeds a fresh request scope into the context.
// It depends only on the standard library and the core SDK.
package nethttp

import (
	"context"
	"net/http"

	groundcover "github.com/groundcover-com/groundcover-go"
)

// config holds middleware options.
type config struct {
	client *groundcover.Client
}

// Option configures the middleware.
type Option func(*config)

// WithClient routes captures to an explicit client instead of the package-level
// global one.
func WithClient(c *groundcover.Client) Option {
	return func(cfg *config) { cfg.client = c }
}

// Middleware wraps next so that each request gets an isolated scope and any
// panic is captured (as an unhandled error) and then re-raised, leaving the
// host's control flow unchanged.
func Middleware(next http.Handler, opts ...Option) http.Handler {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Seed a fresh, isolated scope for this request. Because the scope is
		// mutable and shared, a handler's SetUser/WithScope on r.Context() is
		// visible to the capture below without threading a new context back.
		ctx := seedScope(r.Context(), cfg.client)
		r = r.WithContext(ctx)

		//nolint:contextcheck // capture must read the request context at panic time, not entry time
		defer func() {
			if rec := recover(); rec != nil {
				captureRecovered(r.Context(), cfg.client, rec, requestAttributes(r))
				panic(rec) // re-raise: net/http handles handler panics per request
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// requestAttributes attaches OTel-style HTTP attributes to the captured event.
func requestAttributes(r *http.Request) groundcover.Option {
	return groundcover.WithAttributes(groundcover.Attributes{
		"http.request.method": r.Method,
		"url.path":            r.URL.Path,
		"server.address":      r.Host,
	})
}

// seedScope installs a fresh, isolated request scope using the chosen client
// (or the global one).
func seedScope(ctx context.Context, client *groundcover.Client) context.Context {
	if client != nil {
		return client.WithIsolatedScope(ctx)
	}
	return groundcover.WithIsolatedScope(ctx)
}

// captureRecovered dispatches a recovered panic to the chosen client (or global).
func captureRecovered(ctx context.Context, client *groundcover.Client, rec any, opts ...groundcover.Option) {
	if client != nil {
		client.CaptureRecovered(ctx, rec, opts...)
		return
	}
	groundcover.CaptureRecovered(ctx, rec, opts...)
}
