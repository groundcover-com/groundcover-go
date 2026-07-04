// Command echo shows the Echo middleware: it recovers panics, captures handler
// errors, and seeds a fresh scope per request.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/labstack/echo/v4"

	gc "github.com/groundcover-com/groundcover-go"
	gcecho "github.com/groundcover-com/groundcover-go/contrib/echo"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-echo",
		Env:          "examples",
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	e := echo.New()
	e.Use(gcecho.New(gcecho.Options{CaptureHandlerErrors: true}))
	e.GET("/checkout", func(c echo.Context) error {
		return errors.New("checkout failed: out of stock")
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil))

	fmt.Printf("served /checkout -> %d; captured=%d\n", rec.Code, gc.GlobalStats().Captured)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
