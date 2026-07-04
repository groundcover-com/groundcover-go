package negroni_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/urfave/negroni/v3"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
	gcnegroni "github.com/groundcover-com/groundcover-go/contrib/negroni"
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

func newApp(opts gcnegroni.Options, handler http.HandlerFunc) *negroni.Negroni {
	n := negroni.New()
	n.Use(gcnegroni.New(opts))
	n.UseHandler(handler)
	return n
}

func TestNegroniCapturesPanicAndReRaises(t *testing.T) {
	initDropClient(t)
	n := newApp(gcnegroni.Options{}, func(http.ResponseWriter, *http.Request) { panic("negroni boom") })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		n.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	}()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestNegroniSkipsAbortHandlerPanic(t *testing.T) {
	initDropClient(t)
	n := newApp(gcnegroni.Options{}, func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("abort panic must still be re-raised")
			}
		}()
		n.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/abort", nil))
	}()

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no capture for http.ErrAbortHandler, got %d", got)
	}
}

func TestNegroniDisableRepanicSwallowsPanic(t *testing.T) {
	initDropClient(t)
	n := newApp(gcnegroni.Options{DisableRepanic: true}, func(http.ResponseWriter, *http.Request) { panic("negroni boom") })

	// Must not panic: the middleware swallows it after capturing.
	n.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestNegroniHappyPath(t *testing.T) {
	initDropClient(t)
	n := newApp(gcnegroni.Options{}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	n.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if gc.GlobalStats().Captured != 0 || gc.GlobalStats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", gc.GlobalStats())
	}
}
