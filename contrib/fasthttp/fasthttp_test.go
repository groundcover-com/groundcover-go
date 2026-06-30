package fasthttp_test

import (
	"context"
	"testing"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
	gcfasthttp "github.com/groundcover-com/groundcover-go/contrib/fasthttp"
	"github.com/valyala/fasthttp"
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

func newRequestCtx(method, uri string) *fasthttp.RequestCtx {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	return &ctx
}

func TestFastHTTPCapturesPanicAndReRaises(t *testing.T) {
	client := newDropClient(t)
	handler := gcfasthttp.Middleware(func(ctx *fasthttp.RequestCtx) {
		panic("fasthttp boom")
	}, gcfasthttp.WithClient(client))

	var panicked bool
	func() {
		defer func() {
			panicked = recover() != nil
		}()
		handler(newRequestCtx("GET", "http://example.com/boom"))
	}()

	if !panicked {
		t.Fatal("panic must be re-raised")
	}
	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestFastHTTPHappyPath(t *testing.T) {
	client := newDropClient(t)
	handler := gcfasthttp.Middleware(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBodyString("ok")
	}, gcfasthttp.WithClient(client))

	ctx := newRequestCtx("GET", "http://example.com/ok")
	handler(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status = %d", ctx.Response.StatusCode())
	}
	if client.Stats().Captured != 0 || client.Stats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", client.Stats())
	}
}
