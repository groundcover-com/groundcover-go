// Command logs shows the standalone logs client: no error-tracking client,
// agent, sidecar, or file tailer. It demonstrates both the direct API and the
// stdlib slog bridge sharing one logs.Client.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
//
// With no DSN configured it runs against a placeholder origin; the SDK never
// blocks the program regardless of whether delivery succeeds.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/groundcover-com/groundcover-go/gcslog"
	"github.com/groundcover-com/groundcover-go/logs"
)

func main() {
	key := os.Getenv("GC_LOGS_INGESTION_KEY")
	if key == "" {
		key = os.Getenv("GC_INGESTION_KEY")
	}
	client, err := logs.New(logs.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: key,
		ServiceName:  "examples-logs",
		Env:          "examples",
		Cluster:      envOr("GC_CLUSTER", "examples"),
	})
	if err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = client.CloseTimeout(5 * time.Second) }()

	ctx := context.Background()

	// Layer 1: push records directly through the standalone client.
	client.Info(ctx, "order created", logs.Attributes{"order_id": "o-9", "amount": 42.5})
	client.Errorf(ctx, "charge %s failed after %d tries", "ch_1", 3)

	// Layer 2: the slog bridge. Existing slog call sites work unchanged; the
	// handler forwards records through the same client. Group/attr structure
	// becomes dotted attribute keys (http.status).
	logger := slog.New(gcslog.NewHandler(gcslog.Options{Client: client}))
	logger.Info("rate limit near", slog.Group("http", slog.Int("status", 429)))

	if err := client.FlushTimeout(5 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "flush: %v\n", err)
	}
	fmt.Printf("log pipeline stats: %+v\n", client.Stats())
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
