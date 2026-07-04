package main

import (
	"sync"
	"testing"

	"github.com/valyala/fasthttp"

	gc "github.com/groundcover-com/groundcover-go"
	gcfasthttp "github.com/groundcover-com/groundcover-go/contrib/fasthttp"
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
		ServiceName: "examples-fasthttp-test",
		BeforeSend:  rec.before,
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.CloseTimeout(0) })

	handler := gcfasthttp.New(func(*fasthttp.RequestCtx) {
		panic("checkout failed")
	}, gcfasthttp.Options{})

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
