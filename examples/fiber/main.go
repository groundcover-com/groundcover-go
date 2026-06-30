// Command fiber shows the Fiber middleware: it recovers panics, captures handler
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

	"github.com/gofiber/fiber/v2"

	gc "github.com/groundcover-com/groundcover-go"
	gcfiber "github.com/groundcover-com/groundcover-go/contrib/fiber"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-fiber",
		Env:          "examples",
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	app := fiber.New()
	app.Use(gcfiber.Middleware())
	app.Get("/checkout", func(c *fiber.Ctx) error {
		return errors.New("checkout failed: out of stock")
	})

	resp, err := app.Test(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil))
	if err != nil {
		fatalf("request: %v", err)
	}
	_ = resp.Body.Close()

	fmt.Printf("served /checkout -> %d; captured=%d\n", resp.StatusCode, gc.GlobalStats().Captured)
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
