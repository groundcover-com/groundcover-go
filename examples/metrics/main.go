// Command metrics shows the standalone metrics push client: no agent,
// sidecar, or scrape endpoint. It registers a few instruments, starts the
// background pusher (runtime metrics are collected by default), and stops it
// on shutdown.
//
//	GC_DSN=https://<ingestion-origin> GC_METRICS_INGESTION_KEY=<key> go run .
//
// With no DSN configured it runs against a placeholder origin; the SDK never
// blocks or crashes the program regardless of whether delivery succeeds.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/groundcover-com/groundcover-go/metrics"
)

func main() {
	key := os.Getenv("GC_METRICS_INGESTION_KEY")
	if key == "" {
		key = os.Getenv("GC_INGESTION_KEY")
	}
	reg := metrics.NewRegistry()
	client, err := metrics.New(metrics.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: key,
		Job:          "examples-metrics",
		OnError:      func(err error) { fmt.Fprintf(os.Stderr, "push: %v\n", err) },
	}, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		os.Exit(1)
	}
	stop, err := client.Start(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "start: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = stop(context.Background()) }()

	// Instruments are get-or-create: the same (name, labels) tuple always
	// returns the same instrument. The lookup takes a lock and builds a label
	// signature, so on a hot path hold the instrument in a variable instead of
	// looking it up per event.
	reg.Counter("example_orders_total", map[string]string{"status": "ok"}).Inc()
	reg.Gauge("example_queue_depth", nil).Set(17)
	reg.Histogram("example_request_seconds", nil, nil).Observe(0.042)

	// One synchronous push so the example delivers before exiting; long-lived
	// services just rely on the background interval.
	if err := client.Push(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "push: %v\n", err)
	}
	fmt.Printf("metrics pipeline stats: %+v\n", client.Stats())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
