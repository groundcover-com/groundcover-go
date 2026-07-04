package echo_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	gc "github.com/groundcover-com/groundcover-go"
	gcecho "github.com/groundcover-com/groundcover-go/contrib/echo"
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

func newEcho(client *gc.Client) *echo.Echo {
	e := echo.New()
	e.Use(gcecho.Middleware(gcecho.WithClient(client)))
	return e
}

func TestEchoCapturesPanicAndReRaises(t *testing.T) {
	client := newDropClient(t)
	e := newEcho(client)
	e.GET("/boom", func(echo.Context) error { panic("echo boom") })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	}()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestEchoCapturesHandlerErrors(t *testing.T) {
	client := newDropClient(t)
	e := newEcho(client)
	e.GET("/err", func(c echo.Context) error {
		return errors.New("handler error")
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured handler error, got %d", got)
	}
}

func TestEchoSkipsClientErrors(t *testing.T) {
	client := newDropClient(t)
	e := newEcho(client)
	e.GET("/teapot", func(echo.Context) error {
		return echo.NewHTTPError(http.StatusTeapot, "short and stout")
	})

	// Handler-returned 4xx HTTP errors are request outcomes, not faults.
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/teapot", nil))
	// Router 404s flow through the middleware as echo.ErrNotFound and must not
	// be captured either.
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/no-such-route", nil))

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for client errors, got %d", got)
	}
}

func TestEchoCapturesHTTP500Errors(t *testing.T) {
	client := newDropClient(t)
	e := newEcho(client)
	e.GET("/ise", func(echo.Context) error {
		return echo.NewHTTPError(http.StatusInternalServerError, "db down")
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ise", nil))

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured 5xx error, got %d", got)
	}
}

func TestEchoErrorCaptureDisabled(t *testing.T) {
	client := newDropClient(t)
	e := echo.New()
	e.Use(gcecho.Middleware(gcecho.WithClient(client), gcecho.WithErrorCapture(false)))
	e.GET("/err", func(echo.Context) error { return errors.New("handler error") })

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures with error capture disabled, got %d", got)
	}
}

func TestEchoSkipsAbortHandlerPanic(t *testing.T) {
	client := newDropClient(t)
	e := newEcho(client)
	e.GET("/abort", func(echo.Context) error { panic(http.ErrAbortHandler) })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("abort panic must still be re-raised")
			}
		}()
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/abort", nil))
	}()

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no capture for http.ErrAbortHandler, got %d", got)
	}
}

func TestEchoHappyPath(t *testing.T) {
	client := newDropClient(t)
	e := newEcho(client)
	e.GET("/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if client.Stats().Captured != 0 || client.Stats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", client.Stats())
	}
}
