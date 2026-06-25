// Command nethttp shows the net/http middleware: it recovers panics, captures
// them, and seeds a fresh scope into each request's context. The example wires
// the middleware around a handler that panics, fires one request at it, and
// prints the resulting capture count.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
	gchttp "github.com/groundcover-com/groundcover-go/nethttp"
)

func main() {
	if err := groundcover.Init(groundcover.Config{
		DSN:      envOr("GC_DSN", "https://example.invalid"),
		Workload: "examples-nethttp",
		Env:      "examples",
	}); err != nil {
		log.Fatalf("init: %v", err)
	}
	defer func() { _ = groundcover.CloseTimeout(5 * time.Second) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom in handler")
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	handler := gchttp.Middleware(mux)

	// Fire one request at the panicking route. The middleware captures the
	// panic and re-raises it; net/http contains the re-raised panic per request.
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/panic", nil)
	func() {
		defer func() { _ = recover() }() // swallow the re-raised panic in this demo
		handler.ServeHTTP(rec, req)
	}()

	fmt.Printf("served /panic; captured=%d\n", groundcover.GlobalStats().Captured)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
