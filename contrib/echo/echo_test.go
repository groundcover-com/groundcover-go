package echo_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
	gcecho "github.com/groundcover-com/groundcover-go/contrib/echo"
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

func newEcho(opts gcecho.Options) *echo.Echo {
	e := echo.New()
	e.Use(gcecho.New(opts))
	return e
}

func TestEchoCapturesPanicAndReRaises(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{})
	e.GET("/boom", func(echo.Context) error { panic("echo boom") })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	}()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestEchoCapturesHandlerErrorsWhenEnabled(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{CaptureHandlerErrors: true})
	e.GET("/err", func(echo.Context) error {
		return errors.New("handler error")
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured handler error, got %d", got)
	}
}

func TestEchoHandlerErrorsNotCapturedByDefault(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{})
	e.GET("/err", func(echo.Context) error {
		return errors.New("handler error")
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("handler errors must not be captured by default, got %d", got)
	}
}

func TestEchoSkipsClientErrors(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{CaptureHandlerErrors: true})
	e.GET("/teapot", func(echo.Context) error {
		return echo.NewHTTPError(http.StatusTeapot, "short and stout")
	})

	// Handler-returned 4xx HTTP errors are request outcomes, not faults.
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/teapot", nil))
	// Router 404s flow through the middleware as echo.ErrNotFound and must not
	// be captured either.
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/no-such-route", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for client errors, got %d", got)
	}
}

func TestEchoCapturesHTTP500Errors(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{CaptureHandlerErrors: true})
	e.GET("/ise", func(echo.Context) error {
		return echo.NewHTTPError(http.StatusInternalServerError, "db down")
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ise", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured 5xx error, got %d", got)
	}
}

func TestEchoSkipsAbortHandlerPanic(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{})
	e.GET("/abort", func(echo.Context) error { panic(http.ErrAbortHandler) })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("abort panic must still be re-raised")
			}
		}()
		e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/abort", nil))
	}()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no capture for http.ErrAbortHandler, got %d", got)
	}
}

func TestEchoDisableRepanicSwallowsPanic(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{DisableRepanic: true})
	e.GET("/boom", func(echo.Context) error { panic("echo boom") })

	// Must not panic: the middleware swallows it after capturing.
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestEchoHappyPath(t *testing.T) {
	initDropClient(t)
	e := newEcho(gcecho.Options{CaptureHandlerErrors: true})
	e.GET("/ok", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gc.GlobalStats().Captured != 0 || gc.GlobalStats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", gc.GlobalStats())
	}
}
