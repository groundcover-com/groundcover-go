package fiber_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
	gcfiber "github.com/groundcover-com/groundcover-go/contrib/fiber"
)

// initDropClient installs a package-level client that drops every event in
// BeforeSend, so tests observe captures via GlobalStats without any delivery.
func initDropClient(t *testing.T) {
	t.Helper()
	if err := gc.Init(gc.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*gc.Event) *gc.Event { return nil },
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.Close(context.Background()) })
}

func newApp(opts gcfiber.Options) *fiber.App {
	app := fiber.New()
	app.Use(gcfiber.New(opts))
	return app
}

func doRequest(t *testing.T, app *fiber.App, path string) *http.Response {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	return resp
}

func TestFiberCapturesPanicAndReRaises(t *testing.T) {
	initDropClient(t)
	app := fiber.New()
	app.Use(recover.New())
	app.Use(gcfiber.New(gcfiber.Options{}))
	app.Get("/boom", func(*fiber.Ctx) error { panic("fiber boom") })

	resp := doRequest(t, app, "/boom")
	_ = resp.Body.Close()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestFiberCapturesHandlerErrorsWhenEnabled(t *testing.T) {
	initDropClient(t)
	app := newApp(gcfiber.Options{CaptureHandlerErrors: true})
	app.Get("/err", func(*fiber.Ctx) error { return errors.New("handler error") })

	resp := doRequest(t, app, "/err")
	_ = resp.Body.Close()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured handler error, got %d", got)
	}
}

func TestFiberHandlerErrorsNotCapturedByDefault(t *testing.T) {
	initDropClient(t)
	app := newApp(gcfiber.Options{})
	app.Get("/err", func(*fiber.Ctx) error { return errors.New("handler error") })

	resp := doRequest(t, app, "/err")
	_ = resp.Body.Close()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("handler errors must not be captured by default, got %d", got)
	}
}

func TestFiberSkipsClientErrors(t *testing.T) {
	initDropClient(t)
	app := newApp(gcfiber.Options{CaptureHandlerErrors: true})
	app.Get("/teapot", func(*fiber.Ctx) error {
		return fiber.NewError(fiber.StatusTeapot, "short and stout")
	})

	// Handler-returned 4xx HTTP errors are request outcomes, not faults.
	resp := doRequest(t, app, "/teapot")
	_ = resp.Body.Close()

	// Router 404s flow through the middleware as fiber.ErrNotFound and must not
	// be captured either.
	resp = doRequest(t, app, "/no-such-route")
	_ = resp.Body.Close()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for client errors, got %d", got)
	}
}

func TestFiberCapturesHTTP500Errors(t *testing.T) {
	initDropClient(t)
	app := newApp(gcfiber.Options{CaptureHandlerErrors: true})
	app.Get("/ise", func(*fiber.Ctx) error {
		return fiber.NewError(fiber.StatusInternalServerError, "db down")
	})

	resp := doRequest(t, app, "/ise")
	_ = resp.Body.Close()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured 5xx error, got %d", got)
	}
}

func TestFiberDisableRepanicSwallowsPanic(t *testing.T) {
	initDropClient(t)
	app := newApp(gcfiber.Options{DisableRepanic: true})
	app.Get("/boom", func(*fiber.Ctx) error { panic("fiber boom") })

	// Must not error: the middleware swallows the panic after capturing.
	resp := doRequest(t, app, "/boom")
	_ = resp.Body.Close()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestFiberHappyPath(t *testing.T) {
	initDropClient(t)
	app := newApp(gcfiber.Options{CaptureHandlerErrors: true})
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendString("ok") })

	resp := doRequest(t, app, "/ok")
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if gc.GlobalStats().Captured != 0 || gc.GlobalStats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", gc.GlobalStats())
	}
}
