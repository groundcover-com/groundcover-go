package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/urfave/negroni/v3"

	gc "github.com/groundcover-com/groundcover-go"
	gcnegroni "github.com/groundcover-com/groundcover-go/contrib/negroni"
)

type recorder struct {
	mu     sync.Mutex
	events []gc.Event
}

func (r *recorder) before(e *gc.Event) *gc.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, *e)
	return nil
}

func TestCheckout_CapturesUnhandledPanic(t *testing.T) {
	rec := &recorder{}
	if err := gc.Init(gc.Config{
		DSN:         "http://127.0.0.1:0",
		ServiceName: "examples-negroni-test",
		BeforeSend:  rec.before,
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.CloseTimeout(0) })

	n := negroni.New()
	n.Use(gcnegroni.New(gcnegroni.Options{}))
	n.UseHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("checkout failed")
	}))

	func() {
		defer func() { _ = recover() }()
		n.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil))
	}()

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	if rec.events[0].ErrorHandled {
		t.Fatalf("want unhandled panic")
	}
}
