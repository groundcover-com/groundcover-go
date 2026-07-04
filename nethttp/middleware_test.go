package nethttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
	"github.com/groundcover-com/groundcover-go/nethttp"
)

// newDropClient returns a client that drops every event in BeforeSend, so tests
// can assert capture behavior via Stats without performing any network I/O.
func newDropClient(t *testing.T) *groundcover.Client {
	t.Helper()
	c, err := groundcover.New(groundcover.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*groundcover.Event) *groundcover.Event { return nil },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestMiddlewarePassesThrough(t *testing.T) {
	client := newDropClient(t)
	var served bool
	h := nethttp.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}), nethttp.WithClient(client))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if !served || rec.Code != http.StatusOK {
		t.Fatalf("handler not served correctly: served=%v code=%d", served, rec.Code)
	}
}

func TestMiddlewareCapturesAndReRaises(t *testing.T) {
	client := newDropClient(t)
	h := nethttp.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler boom")
	}), nethttp.WithClient(client))

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("middleware must re-raise the panic")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
	}()

	// The panic was captured (then dropped by BeforeSend), proving the path ran.
	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected the panic to be captured once, got %d", got)
	}
}

func TestMiddlewareSkipsAbortHandlerPanic(t *testing.T) {
	client := newDropClient(t)
	h := nethttp.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// httputil.ReverseProxy panics with this sentinel whenever the client
		// disconnects mid-response; it must not be reported.
		panic(http.ErrAbortHandler)
	}), nethttp.WithClient(client))

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("abort panic must still be re-raised")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	}()

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no capture for http.ErrAbortHandler, got %d", got)
	}
}

// recorderClient returns a client whose BeforeSend records the finalized event
// (then drops it, so there is no network I/O).
func recorderClient(t *testing.T, rec *groundcover.Event) *groundcover.Client {
	t.Helper()
	c, err := groundcover.New(groundcover.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend: func(e *groundcover.Event) *groundcover.Event {
			*rec = *e
			return nil
		},
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestMiddlewareCapturesHandlerSetUser(t *testing.T) {
	var rec groundcover.Event
	client := recorderClient(t, &rec)

	// The handler sets the user on the request context WITHOUT threading the
	// returned context back, then panics. The capture must still see the user.
	h := nethttp.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		client.SetUser(r.Context(), groundcover.User{ID: "handler-user", Organization: "acme"})
		panic("boom")
	}), nethttp.WithClient(client))

	func() {
		defer func() { _ = recover() }()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	}()

	if rec.User.ID != "handler-user" || rec.User.Organization != "acme" {
		t.Fatalf("handler-set scope was not visible at capture: %+v", rec.User)
	}
}

func TestMiddlewareSeedsScope(t *testing.T) {
	client := newDropClient(t)
	var hadScope bool
	h := nethttp.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// SetUser on the request context must not affect the parent context;
		// here we simply confirm the context is usable.
		ctx := client.SetUser(r.Context(), groundcover.User{ID: "u-1"})
		hadScope = ctx != nil
	}), nethttp.WithClient(client))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !hadScope {
		t.Fatal("expected a usable scope in the request context")
	}
}
