// Command fasthttp shows the fasthttp middleware: it recovers panics, captures
// them, and seeds a fresh scope per request.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
package main

import (
	"fmt"
	"os"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
	gcfasthttp "github.com/groundcover-com/groundcover-go/contrib/fasthttp"
	"github.com/valyala/fasthttp"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-fasthttp",
		Env:          "examples",
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	handler := gcfasthttp.Middleware(func(ctx *fasthttp.RequestCtx) {
		panic("checkout failed: out of stock")
	})

	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI("http://example.com/checkout")
	func() {
		defer func() { _ = recover() }()
		handler(&ctx)
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
