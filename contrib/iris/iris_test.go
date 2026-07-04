package iris_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/kataras/iris/v12"
	irishttptest "github.com/kataras/iris/v12/httptest"
	"github.com/kataras/iris/v12/middleware/recover"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
	gciris "github.com/groundcover-com/groundcover-go/contrib/iris"
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

func newApp(opts gciris.Options) *iris.Application {
	app := iris.New()
	app.Use(recover.New())
	app.Use(gciris.New(opts))
	return app
}

func TestIrisCapturesPanic(t *testing.T) {
	initDropClient(t)
	app := newApp(gciris.Options{})
	app.Get("/boom", func(iris.Context) { panic("iris boom") })

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/boom").Expect().Status(http.StatusInternalServerError)

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestIrisCapturesContextErrorsWhenEnabled(t *testing.T) {
	initDropClient(t)
	app := newApp(gciris.Options{CaptureContextErrors: true})
	app.Get("/err", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusInternalServerError, errors.New("handler error"))
	})

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/err").Expect().Status(http.StatusInternalServerError)

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured context error, got %d", got)
	}
}

func TestIrisContextErrorsNotCapturedByDefault(t *testing.T) {
	initDropClient(t)
	app := newApp(gciris.Options{})
	app.Get("/err", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusInternalServerError, errors.New("handler error"))
	})

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/err").Expect().Status(http.StatusInternalServerError)

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("context errors must not be captured by default, got %d", got)
	}
}

func TestIrisSkipsClientErrors(t *testing.T) {
	initDropClient(t)
	app := newApp(gciris.Options{CaptureContextErrors: true})
	app.Get("/teapot", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusTeapot, errors.New("short and stout"))
	})

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/teapot").Expect().Status(http.StatusTeapot)

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for client errors, got %d", got)
	}
}

func TestIrisSkipsAbortHandlerPanic(t *testing.T) {
	initDropClient(t)
	app := newApp(gciris.Options{})
	app.Get("/abort", func(iris.Context) { panic(http.ErrAbortHandler) })

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/abort").Expect().Status(http.StatusInternalServerError)

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no capture for http.ErrAbortHandler, got %d", got)
	}
}

func TestIrisDisableRepanicSwallowsPanic(t *testing.T) {
	initDropClient(t)
	app := iris.New()
	app.Use(gciris.New(gciris.Options{DisableRepanic: true}))
	app.Get("/boom", func(iris.Context) { panic("iris boom") })

	// No recover middleware installed: the middleware itself swallows the
	// panic after capturing.
	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/boom").Expect().Status(http.StatusOK)

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

// TestIrisInertWhenSDKDisabled proves the middleware never affects the host
// when the SDK is disabled (equivalent to never calling Init): requests flow
// unchanged, panics still reach the recover middleware, and nothing is
// captured.
func TestIrisInertWhenSDKDisabled(t *testing.T) {
	if err := gc.Init(gc.Config{Disabled: true}); err != nil {
		t.Fatalf("init disabled client: %v", err)
	}
	app := newApp(gciris.Options{CaptureContextErrors: true})
	app.Get("/ok", func(ctx iris.Context) { _, _ = ctx.WriteString("ok") })
	app.Get("/boom", func(iris.Context) { panic("iris boom") })

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/ok").Expect().Status(http.StatusOK).Body().IsEqual("ok")
	e.GET("/boom").Expect().Status(http.StatusInternalServerError)

	if s := gc.GlobalStats(); s.Captured != 0 || s.DroppedBeforeSend != 0 {
		t.Fatalf("disabled SDK must capture nothing, stats=%+v", s)
	}
}

func TestIrisHappyPath(t *testing.T) {
	initDropClient(t)
	app := newApp(gciris.Options{CaptureContextErrors: true})
	app.Get("/ok", func(ctx iris.Context) { ctx.StatusCode(http.StatusOK) })

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/ok").Expect().Status(http.StatusOK)

	if gc.GlobalStats().Captured != 0 || gc.GlobalStats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", gc.GlobalStats())
	}
}
