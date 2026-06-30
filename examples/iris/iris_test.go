package main

import (
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/kataras/iris/v12"
	irishttptest "github.com/kataras/iris/v12/httptest"
	"github.com/kataras/iris/v12/middleware/recover"

	gc "github.com/groundcover-com/groundcover-go"
	gciris "github.com/groundcover-com/groundcover-go/contrib/iris"
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

func TestCheckout_CapturesHandledError(t *testing.T) {
	rec := &recorder{}
	client, err := gc.New(gc.Config{
		DSN:         "http://127.0.0.1:0",
		ServiceName: "examples-iris-test",
		BeforeSend:  rec.before,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.CloseTimeout(0) })

	app := iris.New()
	app.Use(recover.New())
	app.Use(gciris.Middleware(gciris.WithClient(client)))
	app.Get("/checkout", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusInternalServerError, errors.New("checkout failed"))
	})

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/checkout").Expect().Status(http.StatusInternalServerError)

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	if !rec.events[0].ErrorHandled {
		t.Fatalf("want handled error")
	}
}
