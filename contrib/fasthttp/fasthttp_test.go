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

// TestFastHTTPInertWhenSDKDisabled proves the middleware never affects the
// host when the SDK is disabled (equivalent to never calling Init): requests
// flow unchanged, panics still re-raise, and nothing is captured.
func TestFastHTTPInertWhenSDKDisabled(t *testing.T) {
	if err := gc.Init(gc.Config{Disabled: true}); err != nil {
		t.Fatalf("init disabled client: %v", err)
	}
	t.Cleanup(func() { _ = gc.Close(context.Background()) })
	okHandler := gcfasthttp.New(func(ctx *fasthttp.RequestCtx) {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetBodyString("ok")
	}, gcfasthttp.Options{})

	ctx := newRequestCtx("GET", "http://example.com/ok")
	okHandler(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK || string(ctx.Response.Body()) != "ok" {
		t.Fatalf("response altered: code=%d body=%q", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	panicHandler := gcfasthttp.New(func(*fasthttp.RequestCtx) {
		panic("fasthttp boom")
	}, gcfasthttp.Options{})
	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must still be re-raised with a disabled SDK")
			}
		}()
		panicHandler(newRequestCtx("GET", "http://example.com/boom"))
	}()

	if s := gc.GlobalStats(); s.Captured != 0 || s.DroppedBeforeSend != 0 {
		t.Fatalf("disabled SDK must capture nothing, stats=%+v", s)
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
