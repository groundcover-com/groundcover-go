package main

import (
	"sync"
	"testing"

	gc "github.com/groundcover-com/groundcover-go"
	gcfasthttp "github.com/groundcover-com/groundcover-go/contrib/fasthttp"
	"github.com/valyala/fasthttp"
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
	client, err := gc.New(gc.Config{
		DSN:         "http://127.0.0.1:0",
		ServiceName: "examples-fasthttp-test",
		BeforeSend:  rec.before,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.CloseTimeout(0) })

	handler := gcfasthttp.Middleware(func(*fasthttp.RequestCtx) {
		panic("checkout failed")
	}, gcfasthttp.WithClient(client))

	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("http://example.com/checkout")
	func() {
		defer func() { _ = recover() }()
		handler(&ctx)
	}()

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	if rec.events[0].ErrorHandled {
		t.Fatalf("want unhandled panic")
	}
}
