package gin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	groundcover "github.com/groundcover-com/groundcover-go"
	gcgin "github.com/groundcover-com/groundcover-go/contrib/gin"
)

func newDropClient(t *testing.T) *groundcover.Client {
	t.Helper()
	c, err := groundcover.New(groundcover.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*groundcover.Event) *groundcover.Event { return nil },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func newEngine(client *groundcover.Client) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gcgin.Middleware(gcgin.WithClient(client)))
	return r
}

func TestGinCapturesPanicAndReRaises(t *testing.T) {
	client := newDropClient(t)
	r := newEngine(client)
	r.GET("/boom", func(*gin.Context) { panic("gin boom") })

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/boom", nil))
	}()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestGinCapturesContextErrors(t *testing.T) {
	client := newDropClient(t)
	r := newEngine(client)
	r.GET("/err", func(c *gin.Context) {
		_ = c.Error(errors.New("handler error"))
		c.Status(http.StatusInternalServerError)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/err", nil))

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured context error, got %d", got)
	}
}

func TestGinHappyPath(t *testing.T) {
	client := newDropClient(t)
	r := newEngine(client)
	r.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ok", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if client.Stats().Captured != 0 || client.Stats().DroppedBeforeSend != 0 {
		t.Fatalf("no capture expected on happy path, stats=%+v", client.Stats())
	}
}
