package groundcover

import (
	"context"
	"sync/atomic"
	"time"
)

// globalRecloseTimeout bounds the background teardown of a previous global
// client when Init is called more than once.
const globalRecloseTimeout = 5 * time.Second

// global holds the package-level default client used by the top-level
// functions. It is the single intentional package-level mutable global in
// the SDK; all other state is per-Client. It starts as a no-op client so the
// package functions are safe to call before Init.
//
//nolint:gochecknoglobals // intentional package-level default client
var global atomic.Pointer[Client]

// disabledClient is a shared no-op client used until Init succeeds.
//
//nolint:gochecknoglobals // immutable sentinel for the uninitialized global
var disabledClient = func() *Client {
	c, _ := newClient(Config{Disabled: true}, nil)
	return c
}()

// currentGlobal returns the active global client, or the no-op client if Init
// has not been called.
func currentGlobal() *Client {
	if c := global.Load(); c != nil {
		return c
	}
	return disabledClient
}

// Init configures the package-level default client. Calling it again replaces
// the previous default and tears the old one down in the background (a bounded,
// best-effort Close) so its worker goroutine does not leak. Init never blocks on
// that teardown.
func Init(cfg Config) error {
	c, err := New(cfg)
	if err != nil {
		return err
	}
	prev := global.Swap(c)
	if prev != nil && prev != c {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), globalRecloseTimeout)
			defer cancel()
			_ = prev.Close(ctx)
		}()
	}
	return nil
}

// CaptureError captures err using the package-level client.
func CaptureError(ctx context.Context, err error, opts ...Option) {
	currentGlobal().CaptureError(ctx, err, opts...)
}

// CaptureMessage captures a non-error notice using the package-level client.
func CaptureMessage(ctx context.Context, msg string, level Level, opts ...Option) {
	currentGlobal().CaptureMessage(ctx, msg, level, opts...)
}

// Recover captures a panic (then re-raises it) using the package-level client.
// Use it deferred: defer groundcover.Recover(ctx).
func Recover(ctx context.Context) {
	r := recover()
	if r == nil {
		return
	}
	c := currentGlobal()
	if !c.disabled {
		c.CaptureRecovered(ctx, r)
		// Detached best-effort flush before re-raising the panic.
		flushCtx, cancel := context.WithTimeout(context.Background(), panicFlushTimeout)
		_ = c.Flush(flushCtx) //nolint:contextcheck // deliberate detached best-effort flush before re-raise
		cancel()
	}
	panic(r)
}

// CaptureRecovered captures an already-recovered panic value without re-raising,
// using the package-level client.
func CaptureRecovered(ctx context.Context, recovered any, opts ...Option) {
	currentGlobal().CaptureRecovered(ctx, recovered, opts...)
}

// SetUser returns a context with the identity set, using the package-level client.
func SetUser(ctx context.Context, u User) context.Context {
	return currentGlobal().SetUser(ctx, u)
}

// WithScope applies fn to the request scope (mutating an existing scope in
// place), using the package-level client.
func WithScope(ctx context.Context, fn func(*Scope)) context.Context {
	return currentGlobal().WithScope(ctx, fn)
}

// WithIsolatedScope returns a context with a fresh, isolated copy of the current
// scope, using the package-level client. Middleware uses it at request boundaries.
func WithIsolatedScope(ctx context.Context) context.Context {
	return currentGlobal().WithIsolatedScope(ctx)
}

// Flush flushes the package-level client.
func Flush(ctx context.Context) error { return currentGlobal().Flush(ctx) }

// Close closes the package-level client.
func Close(ctx context.Context) error { return currentGlobal().Close(ctx) }

// FlushTimeout flushes the package-level client with a context bounded by d.
func FlushTimeout(d time.Duration) error { return currentGlobal().FlushTimeout(d) }

// CloseTimeout closes the package-level client with a context bounded by d.
func CloseTimeout(d time.Duration) error { return currentGlobal().CloseTimeout(d) }

// GlobalStats returns the package-level client's self-metrics snapshot. (The
// per-client accessor is the Client.Stats method; this avoids colliding with
// the Stats type at package scope.)
func GlobalStats() Stats { return currentGlobal().Stats() }
