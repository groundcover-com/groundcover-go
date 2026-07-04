// Package fiber provides Fiber middleware that recovers panics, captures them (and
// server-fault errors returned from handlers) through the SDK, and seeds a
// fresh request scope. It is a separate module so the fiber dependency never
// enters the core SDK's go.sum.
//
// Register Fiber's own recover middleware before this one so re-raised panics
// are turned into 500 responses instead of crashing the process (Fiber, unlike
// net/http, does not recover handler panics by itself). If no recovery is
// installed, the middleware still performs a bounded, best-effort flush before
// re-raising so the panic event survives the crash:
//
//	app.Use(recover.New()) // github.com/gofiber/fiber/v2/middleware/recover
//	app.Use(gcfiber.Middleware())
package fiber

import (
	"context"
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

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
// errors. Enabled by default. *fiber.Error values with a status code below 500
// (404s, validation failures, and other client errors) are never captured: they
// are request outcomes, not application faults.
func WithErrorCapture(enabled bool) Option {
	return func(cfg *config) { cfg.captureError = enabled }
}

// Middleware returns Fiber middleware. Panics are captured as unhandled errors
// and re-raised; returned handler errors are captured as handled errors unless
// they are client-side HTTP errors (see WithErrorCapture).
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
				// Fiber has no built-in recovery: unless recover.New() (or
				// similar) is installed above us, the re-raised panic kills the
				// process and the async queue with it. Flush (bounded,
				// best-effort) so the event survives the crash.
				flushBestEffort(cfg.client)
				panic(rec)
			}
		}()

		err := c.Next()
		if cfg.captureError && err != nil && isServerError(err) {
			captureError(c.UserContext(), cfg.client, err, errorAttributes(c, err))
		}
		return err
	}
}

// isServerError reports whether err represents an application fault worth
// capturing. HTTP errors below 500 are normal request outcomes (not found,
// bad input, auth failures) and are skipped.
func isServerError(err error) bool {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Code >= fiber.StatusInternalServerError
	}
	return true
}

func requestAttributes(c *fiber.Ctx) gc.Option {
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Method(),
		"url.path":                  c.Path(),
		"http.route":                routePath(c),
		"http.response.status_code": c.Response().StatusCode(),
	})
}

// errorAttributes derives the response status from the returned error: Fiber's
// error handler has not written the response yet at capture time, so the
// in-flight response status would be misleading.
func errorAttributes(c *fiber.Ctx, err error) gc.Option {
	status := fiber.StatusInternalServerError
	var fe *fiber.Error
	if errors.As(err, &fe) {
		status = fe.Code
	}
	return gc.WithAttributes(gc.Attributes{
		"http.request.method":       c.Method(),
		"url.path":                  c.Path(),
		"http.route":                routePath(c),
		"http.response.status_code": status,
	})
}

func routePath(c *fiber.Ctx) string {
	if r := c.Route(); r != nil && r.Path != "" {
		return r.Path
	}
	return c.Path()
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

// panicFlushTimeout bounds the best-effort flush performed before re-raising a
// panic that may take the process down (mirrors the core SDK's Recover).
const panicFlushTimeout = 2 * time.Second

func flushBestEffort(client *gc.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), panicFlushTimeout)
	defer cancel()
	if client != nil {
		_ = client.Flush(ctx)
		return
	}
	_ = gc.Flush(ctx)
}
