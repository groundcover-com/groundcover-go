package main

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"

	gc "github.com/groundcover-com/groundcover-go"
	gcecho "github.com/groundcover-com/groundcover-go/contrib/echo"
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
	if err := gc.Init(gc.Config{
		DSN:         "http://127.0.0.1:0",
		ServiceName: "examples-echo-test",
		BeforeSend:  rec.before,
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.CloseTimeout(0) })

	e := echo.New()
	e.Use(gcecho.New(gcecho.Options{CaptureHandlerErrors: true}))
	e.GET("/checkout", func(c echo.Context) error {
		return &checkoutError{}
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/checkout", nil))

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	if !rec.events[0].ErrorHandled {
		t.Fatalf("want handled error")
	}
}

type checkoutError struct{}

func (*checkoutError) Error() string { return "checkout failed" }
