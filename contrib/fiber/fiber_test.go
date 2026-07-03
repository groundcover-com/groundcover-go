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

	gc "github.com/groundcover-com/groundcover-go"
	gcfiber "github.com/groundcover-com/groundcover-go/contrib/fiber"
)

func newDropClient(t *testing.T) *gc.Client {
	t.Helper()
	c, err := gc.New(gc.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*gc.Event) *gc.Event { return nil },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func newApp(client *gc.Client) *fiber.App {
	app := fiber.New()
	app.Use(gcfiber.Middleware(gcfiber.WithClient(client)))
	return app
}

func TestFiberCapturesPanicAndReRaises(t *testing.T) {
	client := newDropClient(t)
	app := fiber.New()
	app.Use(recover.New())
	app.Use(gcfiber.Middleware(gcfiber.WithClient(client)))
	app.Get("/boom", func(*fiber.Ctx) error { panic("fiber boom") })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/boom", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestFiberCapturesHandlerErrors(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/err", func(*fiber.Ctx) error { return errors.New("handler error") })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/err", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured handler error, got %d", got)
	}
}

func TestFiberSkipsClientErrors(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/teapot", func(*fiber.Ctx) error {
		return fiber.NewError(fiber.StatusTeapot, "short and stout")
	})

	// Handler-returned 4xx HTTP errors are request outcomes, not faults.
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/teapot", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	// Router 404s flow through the middleware as fiber.ErrNotFound and must not
	// be captured either.
	resp, err = app.Test(httptest.NewRequest(http.MethodGet, "/no-such-route", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for client errors, got %d", got)
	}
}

func TestFiberCapturesHTTP500Errors(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/ise", func(*fiber.Ctx) error {
		return fiber.NewError(fiber.StatusInternalServerError, "db down")
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/ise", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured 5xx error, got %d", got)
	}
}

func TestFiberErrorCaptureDisabled(t *testing.T) {
	client := newDropClient(t)
	app := fiber.New()
	app.Use(gcfiber.Middleware(gcfiber.WithClient(client), gcfiber.WithErrorCapture(false)))
	app.Get("/err", func(*fiber.Ctx) error { return errors.New("handler error") })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/err", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures with error capture disabled, got %d", got)
	}
}

func TestFiberHappyPath(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/ok", func(c *fiber.Ctx) error { return c.SendString("ok") })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/ok", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
	if client.Stats().Captured != 0 || client.Stats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", client.Stats())
	}
}
