package main

import (
	"errors"
	"net/http/httptest"
	"sync"
	"testing"

	gc "github.com/groundcover-com/groundcover-go"
	gcfiber "github.com/groundcover-com/groundcover-go/contrib/fiber"
	"github.com/gofiber/fiber/v2"
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
		ServiceName: "examples-fiber-test",
		BeforeSend:  rec.before,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = client.CloseTimeout(0) })

	app := fiber.New()
	app.Use(gcfiber.Middleware(gcfiber.WithClient(client)))
	app.Get("/checkout", func(*fiber.Ctx) error { return errors.New("checkout failed") })

	resp, err := app.Test(httptest.NewRequest("GET", "/checkout", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	if !rec.events[0].ErrorHandled {
		t.Fatalf("want handled error")
	}
}
