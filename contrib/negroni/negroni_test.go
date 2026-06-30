package negroni_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/urfave/negroni/v3"

	gc "github.com/groundcover-com/groundcover-go"
	gcnegroni "github.com/groundcover-com/groundcover-go/contrib/negroni"
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

func TestNegroniCapturesPanicAndReRaises(t *testing.T) {
	client := newDropClient(t)
	n := negroni.New()
	n.Use(gcnegroni.Middleware(gcnegroni.WithClient(client)))
	n.UseHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("negroni boom") }))

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		n.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	}()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestNegroniHappyPath(t *testing.T) {
	client := newDropClient(t)
	n := negroni.New()
	n.Use(gcnegroni.Middleware(gcnegroni.WithClient(client)))
	n.UseHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	n.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if client.Stats().Captured != 0 || client.Stats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", client.Stats())
	}
}
