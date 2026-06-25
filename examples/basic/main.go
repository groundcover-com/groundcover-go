// Command basic shows the minimal groundcover setup: initialize once, attach a
// user and custom attributes, capture an error, and flush on shutdown.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
//
// With no DSN configured it runs against a placeholder origin; the SDK never
// blocks the program regardless of whether delivery succeeds.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
)

func main() {
	if err := groundcover.Init(groundcover.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-basic",
		Env:          "examples",
		Release:      groundcover.Version,
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = groundcover.CloseTimeout(5 * time.Second) }()

	// Request-scoped identity + attributes live on the context.
	ctx := groundcover.SetUser(context.Background(), groundcover.User{
		ID:           "u-123",
		Organization: "acme",
	})

	if err := charge(ctx); err != nil {
		groundcover.CaptureError(ctx, err, groundcover.WithAttributes(groundcover.Attributes{
			"order_id": "o-9",
			"amount":   42.5,
			"is_retry": true,
		}))
	}

	groundcover.CaptureMessage(ctx, "falling back to stale cache", groundcover.LevelWarning)

	fmt.Printf("captured; stats=%+v\n", groundcover.GlobalStats())
}

func charge(context.Context) error {
	return errors.New("charge failed: card declined")
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
