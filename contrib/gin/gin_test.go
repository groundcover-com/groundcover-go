package gin_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
	gcgin "github.com/groundcover-com/groundcover-go/contrib/gin"
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

func newEngine(opts gcgin.Options) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gcgin.New(opts))
	return r
}

func TestGinCapturesPanicAndReRaises(t *testing.T) {
	initDropClient(t)
	r := newEngine(gcgin.Options{})
	r.GET("/boom", func(*gin.Context) { panic("gin boom") })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	}()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestGinCapturesContextErrorsWhenEnabled(t *testing.T) {
	initDropClient(t)
	r := newEngine(gcgin.Options{CaptureContextErrors: true})
	r.GET("/err", func(c *gin.Context) {
		_ = c.Error(errors.New("handler error"))
		c.Status(http.StatusInternalServerError)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured context error, got %d", got)
	}
}

func TestGinContextErrorsNotCapturedByDefault(t *testing.T) {
	initDropClient(t)
	r := newEngine(gcgin.Options{})
	r.GET("/err", func(c *gin.Context) {
		_ = c.Error(errors.New("handler error"))
		c.Status(http.StatusInternalServerError)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("context errors must not be captured by default, got %d", got)
	}
}

func TestGinSkipsClientErrors(t *testing.T) {
	initDropClient(t)
	r := newEngine(gcgin.Options{CaptureContextErrors: true})
	r.GET("/bad", func(c *gin.Context) {
		_ = c.Error(errors.New("binding failed"))
		c.Status(http.StatusBadRequest)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/bad", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for 4xx responses, got %d", got)
	}
}

func TestGinSkipsAbortAndBrokenPipePanics(t *testing.T) {
	initDropClient(t)
	r := newEngine(gcgin.Options{})
	r.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })
	r.GET("/pipe", func(*gin.Context) {
		panic(&net.OpError{Op: "write", Err: os.NewSyscallError("write", errors.New("broken pipe"))})
	})

	for _, path := range []string{"/abort", "/pipe"} {
		func() {
			defer func() {
				if rec := recover(); rec == nil {
					t.Fatalf("%s: panic must still be re-raised", path)
				}
			}()
			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		}()
	}

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for abort/broken-pipe panics, got %d", got)
	}
}

func TestGinDisableRepanicSwallowsPanic(t *testing.T) {
	initDropClient(t)
	r := newEngine(gcgin.Options{DisableRepanic: true})
	r.GET("/boom", func(*gin.Context) { panic("gin boom") })

	// Must not panic: the middleware swallows it after capturing.
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestGinCapturesHandlerSetUser(t *testing.T) {
	var rec gc.Event
	if err := gc.Init(gc.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend: func(e *gc.Event) *gc.Event {
			rec = *e
			return nil
		},
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.Close(context.Background()) })

	r := newEngine(gcgin.Options{CaptureContextErrors: true})
	r.GET("/err", func(c *gin.Context) {
		// Handler sets the user on the request context, then records an error.
		gc.SetUser(c.Request.Context(), gc.User{ID: "gin-user"})
		_ = c.Error(errors.New("handler error"))
		c.Status(http.StatusInternalServerError)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if rec.User.ID != "gin-user" {
		t.Fatalf("handler-set scope was not visible at capture: %+v", rec.User)
	}
}

// TestGinInertWhenSDKDisabled proves the middleware never affects the host
// when the SDK is disabled (equivalent to never calling Init): requests flow
// unchanged, panics still re-raise, and nothing is captured.
func TestGinInertWhenSDKDisabled(t *testing.T) {
	if err := gc.Init(gc.Config{Disabled: true}); err != nil {
		t.Fatalf("init disabled client: %v", err)
	}
	r := newEngine(gcgin.Options{CaptureContextErrors: true})
	r.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/boom", func(*gin.Context) { panic("gin boom") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("response altered: code=%d body=%q", rec.Code, rec.Body.String())
	}

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must still be re-raised with a disabled SDK")
			}
		}()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	}()

	if s := gc.GlobalStats(); s.Captured != 0 || s.DroppedBeforeSend != 0 {
		t.Fatalf("disabled SDK must capture nothing, stats=%+v", s)
	}
}

func TestGinHappyPath(t *testing.T) {
	initDropClient(t)
	r := newEngine(gcgin.Options{})
	r.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gc.GlobalStats().Captured != 0 || gc.GlobalStats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", gc.GlobalStats())
	}
}
