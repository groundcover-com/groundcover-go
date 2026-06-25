package nethttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
	gchttp "github.com/groundcover-com/groundcover-go/nethttp"
)

// recorder collects finalized events via BeforeSend (then drops them, so there
// is no network I/O). It is the consumer-facing test seam.
type recorder struct {
	mu     sync.Mutex
	events []groundcover.Event
}

func (r *recorder) record(e *groundcover.Event) *groundcover.Event {
	r.mu.Lock()
	r.events = append(r.events, *e)
	r.mu.Unlock()
	return nil
}

func (r *recorder) all() []groundcover.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]groundcover.Event(nil), r.events...)
}

func (r *recorder) find(t *testing.T, pred func(groundcover.Event) bool) groundcover.Event {
	t.Helper()
	for _, e := range r.all() {
		if pred(e) {
			return e
		}
	}
	t.Fatalf("no recorded event matched; recorded %d", len(r.all()))
	return groundcover.Event{}
}

// TestNetHTTPServerIntegration exercises the SDK the way a real consumer does:
// a live HTTP server fronted by the middleware, real requests, handlers that set
// identity on the request context, and assertions on the captured event content
// (not just counts). This is the topology that surfaced the scope bug.
func TestNetHTTPServerIntegration(t *testing.T) {
	rec := &recorder{}
	client, err := groundcover.New(groundcover.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    rec.record,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/handled", func(w http.ResponseWriter, r *http.Request) {
		// Handler threads the returned context (the explicit pattern).
		ctx := client.SetUser(r.Context(), groundcover.User{ID: "u-handled"})
		client.CaptureError(ctx, errors.New("handled failure"),
			groundcover.WithAttributes(groundcover.Attributes{"route": "/handled"}))
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/panic", func(_ http.ResponseWriter, r *http.Request) {
		// Handler mutates the shared scope WITHOUT threading it back, then panics.
		client.SetUser(r.Context(), groundcover.User{ID: "u-panic"})
		panic("kaboom")
	})

	srv := httptest.NewServer(gchttp.Middleware(mux, gchttp.WithClient(client)))
	defer srv.Close()

	get := func(path string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			return // /panic closes the connection after re-raise; that's expected
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	get("/ok")
	get("/handled")
	get("/panic")

	// /ok must not capture anything.
	for _, e := range rec.all() {
		if e.Attributes["route"] == "/ok" {
			t.Fatal("/ok should not capture")
		}
	}

	handled := rec.find(t, func(e groundcover.Event) bool { return e.ErrorMessage == "handled failure" })
	if handled.User.ID != "u-handled" {
		t.Fatalf("handled event lost user: %+v", handled.User)
	}
	if !handled.ErrorHandled {
		t.Fatal("explicit CaptureError must be handled=true")
	}
	if handled.Attributes["route"] != "/handled" {
		t.Fatalf("handled event lost attribute: %+v", handled.Attributes)
	}

	panicked := rec.find(t, func(e groundcover.Event) bool { return !e.ErrorHandled })
	if panicked.User.ID != "u-panic" {
		t.Fatalf("panic event lost handler-set user (shared-scope bug): %+v", panicked.User)
	}
	if panicked.Level != groundcover.LevelFatal {
		t.Fatalf("panic should be fatal, got %q", panicked.Level)
	}
}

// TestNetHTTPCrossRequestScopeIsolation verifies one request's identity never
// leaks into another's captured event.
func TestNetHTTPCrossRequestScopeIsolation(t *testing.T) {
	rec := &recorder{}
	client, err := groundcover.New(groundcover.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    rec.record,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })

	mux := http.NewServeMux()
	mux.HandleFunc("/u", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		ctx := client.SetUser(r.Context(), groundcover.User{ID: id})
		client.CaptureError(ctx, errors.New("err-"+id))
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(gchttp.Middleware(mux, gchttp.WithClient(client)))
	defer srv.Close()

	for _, id := range []string{"alice", "bob", "carol"} {
		resp, err := http.Get(srv.URL + "/u?id=" + id)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		_ = resp.Body.Close()
	}

	for _, e := range rec.all() {
		want := "err-" + e.User.ID
		if e.ErrorMessage != want {
			t.Fatalf("scope leaked across requests: user=%q message=%q", e.User.ID, e.ErrorMessage)
		}
	}
	if len(rec.all()) != 3 {
		t.Fatalf("expected 3 captures, got %d", len(rec.all()))
	}
}
