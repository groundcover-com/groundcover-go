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
