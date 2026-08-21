// Command logs shows direct log delivery: no agent, sidecar, or file tailer.
// It demonstrates both layers of the log signal — the emission API
// (LogInfo/LogErrorf) and the stdlib slog bridge (gcslog) — sharing one
// client, one pipeline, and one request scope.
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

	gc "github.com/groundcover-com/groundcover-go"
	"github.com/groundcover-com/groundcover-go/gcslog"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:              envOr("GC_DSN", "https://example.invalid"),
		IngestionKey:     os.Getenv("GC_INGESTION_KEY"),
		LogsIngestionKey: os.Getenv("GC_LOGS_INGESTION_KEY"),
		ServiceName:      "examples-logs",
		Env:              "examples",
		Cluster:          envOr("GC_CLUSTER", "examples"),
		EnableLogs:       true, // the log signal is opt-in
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	// Identity and attributes set on the context scope are inherited by every
	// log record captured with that context — same as error events.
	ctx := gc.SetUser(context.Background(), gc.User{ID: "u-123", Organization: "acme"})

	// Layer 1: the emission API. Push records directly.
	gc.LogInfo(ctx, "order created", gc.Attributes{"order_id": "o-9", "amount": 42.5})
	gc.LogErrorf(ctx, "charge %s failed after %d tries", "ch_1", 3)

	// Layer 2: the slog bridge. Existing slog call sites work unchanged; the
	// handler forwards records through the same client. Group/attr structure
	// becomes dotted attribute keys (http.status).
	logger := slog.New(gcslog.NewHandler(gcslog.Options{}))
	logger.Info("rate limit near", slog.Group("http", slog.Int("status", 429)))

	if err := gc.FlushTimeout(5 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "flush: %v\n", err)
	}
	fmt.Printf("log pipeline stats: %+v\n", gc.GlobalLogStats())
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
