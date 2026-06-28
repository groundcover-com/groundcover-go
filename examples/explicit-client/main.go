// Command explicit-client shows using an explicit *gc.Client instead of the
// package-level global. This is the right pattern for libraries, multi-tenant
// apps, or anything that needs more than one configuration in a process.
//
// It wires the explicit client into the net/http middleware (via WithClient) so
// handler panics are captured by that client, runs a background worker under an
// isolated scope, and reads back the client's own Stats.
//
// Runnable with no backend:
//
//	go run ./explicit-client
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
	gchttp "github.com/groundcover-com/groundcover-go/nethttp"
)

func main() {
	dsn := os.Getenv("GC_DSN")
	ingestionKey := os.Getenv("GC_INGESTION_KEY")
	offline := false
	switch {
	case dsn == "" && ingestionKey == "":
		dsn = "https://local.invalid"
		offline = true
	case dsn == "":
		fatalf("GC_DSN must be set when GC_INGESTION_KEY is configured")
	case ingestionKey == "" && !isLocalDSN(dsn):
		fatalf("GC_INGESTION_KEY must be set when GC_DSN points to a non-local backend")
	case ingestionKey == "":
		offline = true
	}

	cfg := gc.Config{
		DSN:           dsn,
		IngestionKey:  ingestionKey,
		ServiceName:   "explicit-client-example",
		Env:           "examples",
		Debug:         offline, // print events locally when no backend
		BatchSize:     50,      // a couple of tuning knobs, for illustration
		FlushInterval: 2 * time.Second,
	}
	if offline {
		cfg.HTTPClient = &http.Client{Transport: drop{}} // truly offline: no network attempts
	}

	// Construct an explicit client; nothing touches the package-level global.
	client, err := gc.New(cfg)
	if err != nil {
		fatalf("new client: %v", err)
	}
	defer func() { _ = client.CloseTimeout(5 * time.Second) }()

	// Route handler panics to THIS client via WithClient (not the global).
	mux := http.NewServeMux()
	mux.HandleFunc("/work", func(w http.ResponseWriter, r *http.Request) {
		// Identity/scope on an explicit client mirrors the global API.
		ctx := client.SetUser(r.Context(), gc.User{ID: "tenant-42-user", Organization: "tenant-42"})
		ctx = client.WithScope(ctx, func(s *gc.Scope) { s.SetAttribute("route", "/work") })
		client.CaptureMessage(ctx, "work served", gc.LevelInfo)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/boom", func(http.ResponseWriter, *http.Request) { panic("boom in an embedded handler") })

	handler := gchttp.Middleware(mux, gchttp.WithClient(client))

	// A background worker captures under its own isolated scope, so it neither
	// inherits nor leaks request scope.
	runWorker(client)

	// Drive the service in-process so the example is self-contained.
	fire(handler, "/work")
	fire(handler, "/boom") // panic captured by the middleware -> this client

	_ = client.FlushTimeout(3 * time.Second)

	s := client.Stats()
	fmt.Printf("client stats: captured=%d sent=%d dropped(send=%d)\n", s.Captured, s.Sent, s.DroppedSendExhausted)
}

// runWorker captures a panic from a goroutine under an isolated scope.
func runWorker(client *gc.Client) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		wctx := client.WithIsolatedScope(context.Background())
		defer func() { _ = recover() }() // contain the re-raise for the demo
		defer client.Recover(wctx)       // captures the panic (unhandled), then re-raises
		panic("background worker failed")
	}()
	wg.Wait()
}

// fire drives one request against the handler. A real net/http server recovers
// panics per request; we emulate that with a recover so /boom's re-raised panic
// doesn't abort the example.
func fire(h http.Handler, path string) {
	defer func() { _ = recover() }()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
}

// drop is an HTTP transport that accepts and discards everything, so the no-DSN
// demo is genuinely offline (no network attempts, no shutdown retries).
type drop struct{}

func (drop) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody, Header: make(http.Header)}, nil
}

func isLocalDSN(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "localhost" || host == "local.invalid" || host == "::1" || strings.HasPrefix(host, "127.")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
