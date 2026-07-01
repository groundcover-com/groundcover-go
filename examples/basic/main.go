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

	gc "github.com/groundcover-com/groundcover-go"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-basic",
		Env:          "examples",
		Release:      gc.Version(),
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	// Request-scoped identity + attributes live on the context.
	ctx := gc.SetUser(context.Background(), gc.User{
		ID:           "u-123",
		Organization: "acme",
	})

	if err := charge(ctx); err != nil {
		gc.CaptureError(ctx, err, gc.WithAttributes(gc.Attributes{
			"order_id": "o-9",
			"amount":   42.5,
			"is_retry": true,
		}))
	}

	gc.CaptureMessage(ctx, "falling back to stale cache", gc.LevelWarning)

	fmt.Printf("captured; stats=%+v\n", gc.GlobalStats())
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
