// Package negroni provides Negroni middleware that recovers panics, captures them
// through groundcover, and seeds a fresh request scope. It is a separate module
// so the negroni dependency never enters the core SDK's go.sum.
package negroni

import (
	"context"
	"net/http"

	gc "github.com/groundcover-com/groundcover-go"
	"github.com/urfave/negroni/v3"
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

type middleware struct {
	cfg config
}

// Middleware returns Negroni middleware. Panics are captured as unhandled errors
// and re-raised (so Negroni's own recovery, if installed, still runs).
func Middleware(opts ...Option) negroni.Handler {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	return &middleware{cfg: cfg}
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	ctx := seedScope(r.Context(), m.cfg.client)
	r = r.WithContext(ctx)

	defer func() {
		if rec := recover(); rec != nil {
			captureRecovered(r.Context(), m.cfg.client, rec, requestAttributes(r))
			panic(rec)
		}
	}()

	next(w, r)
}

func requestAttributes(r *http.Request) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method": r.Method,
		"url.path":            r.URL.Path,
		"server.address":      r.Host,
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
