// Command cli is a batch/worker example: a nightly "order reconciler" that
// processes a queue of orders, captures failures at the boundary, recovers
// panics in worker goroutines, and flushes on exit. It is the non-web companion
// to the basic example and exercises the parts of the SDK a background job needs:
// per-call options, severity levels, request scope, panic recovery, and the
// Logger / OnDrop / Debug configuration hooks.
//
// Run it with no backend — events are printed to stderr via the SDK's built-in
// Debug renderer, so you can see exactly what would be sent:
//
//	go run ./cli
//
// Or point it at groundcover:
//
//	GC_DSN=https://<tenant>.platform.grcv.io GC_INGESTION_KEY=<key> go run ./cli
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
)

// orgID is the tenant all reconciler events are attributed to.
const orgID = "acme"

// order is a unit of work; ownerID lets us attribute failures to a user.
type order struct {
	id      string
	ownerID string
	amount  float64
}

func main() {
	if err := gc.Init(configFromEnv()); err != nil {
		fatalf("groundcover init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }() // bounded flush on shutdown

	fmt.Printf("reconciler starting (groundcover-go v%s)\n", gc.Version)

	// Batch-wide identity and scope live on the context; every capture made with
	// this context inherits them.
	ctx := gc.SetUser(context.Background(), gc.User{
		ID:           "svc-reconciler",
		Organization: orgID,
	})
	ctx = gc.WithScope(ctx, func(s *gc.Scope) {
		s.SetAttributes(gc.Attributes{"batch.kind": "nightly", "region": "us-east-1"})
		s.SetAttribute("batch.id", "2026-06-26T00:00Z")
		s.SetSessionID("batch-2026-06-26")
		s.SetAnonymousID("scheduler-cron") // pre-auth/system identifier
	})

	gc.CaptureMessage(ctx, "reconciliation started", gc.LevelInfo)

	orders := []order{
		{id: "ord-1", ownerID: "u-100", amount: 19.99},
		{id: "ord-2", ownerID: "u-205", amount: 4200.00}, // will fail
		{id: "ord-3", ownerID: "u-100", amount: 0},       // will warn
	}
	failures := 0
	for _, o := range orders {
		if err := reconcile(o); err != nil {
			failures++
			capture(ctx, o, err)
		}
	}

	// A verbose diagnostic in an ISOLATED sub-scope. WithScope mutates the scope in
	// place, so we clone first with WithIsolatedScope; otherwise SetLevel would leak
	// back onto the batch context.
	debugCtx := gc.WithScope(gc.WithIsolatedScope(ctx), func(s *gc.Scope) { s.SetLevel(gc.LevelDebug) })
	gc.CaptureMessage(debugCtx, "cache warm complete", gc.LevelDebug)

	// Pin a recurring, known issue to a single group — also in an isolated scope so
	// the fingerprint doesn't leak onto later captures made with ctx.
	flakyCtx := gc.WithScope(gc.WithIsolatedScope(ctx), func(s *gc.Scope) { s.SetFingerprint("known-flaky-downstream") })
	gc.CaptureMessage(flakyCtx, "downstream timed out, will retry next run", gc.LevelError)

	// Recover a panic in a sub-task without re-raising (we own the outcome).
	settleAccounts(ctx)
	// Recover a panic in a worker goroutine and re-raise it (contained for the demo).
	runWorker(ctx)

	level := gc.LevelInfo
	if failures > 0 {
		level = gc.LevelWarning
	}
	gc.CaptureMessage(ctx, fmt.Sprintf("reconciliation finished: %d/%d failed", failures, len(orders)), level)

	if err := gc.FlushTimeout(5 * time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "flush: %v\n", err)
	}

	s := gc.GlobalStats()
	fmt.Printf("done — captured=%d sent=%d dropped(before_send=%d overflow=%d send=%d)\n",
		s.Captured, s.Sent, s.DroppedBeforeSend, s.DroppedOverflow, s.DroppedSendExhausted)
}

// reconcile simulates work that can fail or degrade.
func reconcile(o order) error {
	switch {
	case o.amount > 1000:
		return fmt.Errorf("settle %s: %w", o.id, errLimitExceeded)
	case o.amount == 0:
		return fmt.Errorf("reconcile %s: %w", o.id, errEmptyOrder)
	default:
		return nil
	}
}

// capture records a failure, choosing severity, grouping, and metadata per error.
// It shows the per-call Options working together.
func capture(ctx context.Context, o order, err error) {
	opts := []gc.Option{
		// Attribute the failure to the order's owner. WithUser replaces the whole
		// User, so carry the tenant over from the batch scope.
		gc.WithUser(gc.User{ID: o.ownerID, Organization: orgID}),
		gc.WithAttributes(gc.Attributes{"order_id": o.id, "amount": o.amount}),
		gc.WithFingerprint("reconcile:" + classify(err)), // group by failure class
		gc.WithTitle("Reconciliation failed: " + classify(err)),
	}
	if errors.Is(err, errEmptyOrder) {
		opts = append(opts, gc.WithLevel(gc.LevelWarning)) // recoverable
	}
	gc.CaptureError(ctx, err, opts...)
}

// settleAccounts recovers a panic without re-raising it.
func settleAccounts(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			gc.CaptureRecovered(ctx, r, gc.WithAttributes(gc.Attributes{"phase": "settle"}))
			fmt.Fprintf(os.Stderr, "settle panicked, recovered: %v\n", r)
		}
	}()
	panic("settlement ledger unavailable")
}

// runWorker recovers a goroutine panic and re-raises it; contained for the demo.
func runWorker(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { _ = recover() }() // swallow the re-raise so the example finishes
		defer gc.Recover(ctx)            // captures the panic (unhandled), then re-raises
		panic("worker: nil pointer dereference")
	}()
	wg.Wait()
}

var (
	errLimitExceeded = errors.New("transaction limit exceeded")
	errEmptyOrder    = errors.New("order has no line items")
)

func classify(err error) string {
	switch {
	case errors.Is(err, errLimitExceeded):
		return "limit_exceeded"
	case errors.Is(err, errEmptyOrder):
		return "empty_order"
	default:
		return "unknown"
	}
}

// configFromEnv builds the SDK config. With no GC_DSN, it enables Debug so events
// are printed to stderr by the SDK (and never delivered), making the example
// runnable with zero infrastructure.
func configFromEnv() gc.Config {
	cfg := gc.Config{
		DSN:          os.Getenv("GC_DSN"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  envOr("GC_SERVICE_NAME", "order-reconciler"),
		Env:          envOr("GC_ENV", "local"),
		Release:      "1.0.0",
		// Operational hooks. PII scrubbing and identity pseudonymization are shown
		// in the before-send example.
		Logger: gc.LoggerFunc(func(l gc.Level, msg string, suppressed int) {
			fmt.Fprintf(os.Stderr, "[groundcover-sdk %s] %s (suppressed=%d)\n", l, msg, suppressed)
		}),
		OnDrop: func(n int) { fmt.Fprintf(os.Stderr, "[groundcover] dropped %d event(s) under load\n", n) },
	}
	if cfg.DSN == "" {
		cfg.DSN = "https://local.invalid"                // never contacted
		cfg.Debug = true                                 // SDK prints each event to stderr, readably
		cfg.HTTPClient = &http.Client{Transport: drop{}} // truly offline: no network attempts
		fmt.Fprintln(os.Stderr, "no GC_DSN set: running in Debug mode (events printed to stderr, not delivered)")
	}
	return cfg
}

// drop is an HTTP transport that accepts and discards everything, so the no-DSN
// demo is genuinely offline (no network attempts, no shutdown retries).
type drop struct{}

func (drop) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody, Header: make(http.Header)}, nil
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
