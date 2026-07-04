// Command negroni shows the Negroni middleware: it recovers panics, captures
// them, and seeds a fresh scope per request.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/urfave/negroni/v3"

	gc "github.com/groundcover-com/groundcover-go"
	gcnegroni "github.com/groundcover-com/groundcover-go/contrib/negroni"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-negroni",
		Env:          "examples",
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	n := negroni.New()
	n.Use(gcnegroni.New(gcnegroni.Options{}))
	n.UseHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("checkout failed: out of stock")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil)
	func() {
		defer func() { _ = recover() }()
		n.ServeHTTP(rec, req)
	}()

	fmt.Printf("served /checkout; captured=%d\n", gc.GlobalStats().Captured)
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
