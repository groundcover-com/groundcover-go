package fasthttp_test

import (
	"context"
	"testing"
	"time"

	"github.com/valyala/fasthttp"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
	gcfasthttp "github.com/groundcover-com/groundcover-go/contrib/fasthttp"
)

// initDropClient installs a package-level client that drops every event in
// BeforeSend, so tests observe captures via GlobalStats without any delivery.
func initDropClient(t *testing.T) {
	t.Helper()
	if err := gc.Init(gc.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*gc.Event) *gc.Event { return nil },
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.Close(context.Background()) })
}

func newRequestCtx(method, uri string) *fasthttp.RequestCtx {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	return &ctx
}

func TestFastHTTPCapturesPanicAndReRaises(t *testing.T) {
	initDropClient(t)
	handler := gcfasthttp.New(func(*fasthttp.RequestCtx) {
		panic("fasthttp boom")
	}, gcfasthttp.Options{})

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
	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestFastHTTPDisableRepanicSwallowsPanic(t *testing.T) {
	initDropClient(t)
	handler := gcfasthttp.New(func(*fasthttp.RequestCtx) {
		panic("fasthttp boom")
	}, gcfasthttp.Options{DisableRepanic: true})

	// Must not panic: the middleware swallows it after capturing.
	handler(newRequestCtx("GET", "http://example.com/boom"))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestFastHTTPScopeContext(t *testing.T) {
	initDropClient(t)
	handler := gcfasthttp.New(func(ctx *fasthttp.RequestCtx) {
		gcctx := gcfasthttp.ScopeContext(ctx)
		if gcctx == context.Background() {
			t.Fatal("expected a seeded scope context, got context.Background()")
		}
		gc.CaptureMessage(gcctx, "from handler", gc.LevelError)
	}, gcfasthttp.Options{})

	handler(newRequestCtx("GET", "http://example.com/scoped"))

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured message via scope context, got %d", got)
	}
}

func TestFastHTTPScopeContextWithoutMiddleware(t *testing.T) {
	ctx := newRequestCtx("GET", "http://example.com/raw")
	if got := gcfasthttp.ScopeContext(ctx); got != context.Background() {
		t.Fatalf("expected context.Background() fallback, got %v", got)
	}
}

func TestFastHTTPHappyPath(t *testing.T) {
	initDropClient(t)
	handler := gcfasthttp.New(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBodyString("ok")
	}, gcfasthttp.Options{})

	ctx := newRequestCtx("GET", "http://example.com/ok")
	handler(ctx)

	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status = %d", ctx.Response.StatusCode())
	}
	if gc.GlobalStats().Captured != 0 || gc.GlobalStats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", gc.GlobalStats())
	}
}
