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

	gc "github.com/groundcover-com/groundcover-go"
	gciris "github.com/groundcover-com/groundcover-go/contrib/iris"
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

func newApp(client *gc.Client) *iris.Application {
	app := iris.New()
	app.Use(recover.New())
	app.Use(gciris.Middleware(gciris.WithClient(client)))
	return app
}

func TestIrisCapturesPanic(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/boom", func(iris.Context) { panic("iris boom") })

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/boom").Expect().Status(http.StatusInternalServerError)

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestIrisCapturesContextErrors(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/err", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusInternalServerError, errors.New("handler error"))
	})

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/err").Expect().Status(http.StatusInternalServerError)

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured context error, got %d", got)
	}
}

func TestIrisSkipsClientErrors(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/teapot", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusTeapot, errors.New("short and stout"))
	})

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/teapot").Expect().Status(http.StatusTeapot)

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for client errors, got %d", got)
	}
}

func TestIrisErrorCaptureDisabled(t *testing.T) {
	client := newDropClient(t)
	app := iris.New()
	app.Use(recover.New())
	app.Use(gciris.Middleware(gciris.WithClient(client), gciris.WithErrorCapture(false)))
	app.Get("/err", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusInternalServerError, errors.New("handler error"))
	})

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/err").Expect().Status(http.StatusInternalServerError)

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures with error capture disabled, got %d", got)
	}
}

func TestIrisSkipsAbortHandlerPanic(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/abort", func(iris.Context) { panic(http.ErrAbortHandler) })

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/abort").Expect().Status(http.StatusInternalServerError)

	if got := client.Stats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no capture for http.ErrAbortHandler, got %d", got)
	}
}

func TestIrisHappyPath(t *testing.T) {
	client := newDropClient(t)
	app := newApp(client)
	app.Get("/ok", func(ctx iris.Context) { ctx.StatusCode(http.StatusOK) })

	e := irishttptest.New(t, app, irishttptest.URL("http://example.com"))
	e.GET("/ok").Expect().Status(http.StatusOK)

	if client.Stats().Captured != 0 || client.Stats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", client.Stats())
	}
}
