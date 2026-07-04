// Command iris shows the Iris middleware: it recovers panics, captures context
// errors, and seeds a fresh scope per request.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/recover"

	gc "github.com/groundcover-com/groundcover-go"
	gciris "github.com/groundcover-com/groundcover-go/contrib/iris"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-iris",
		Env:          "examples",
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	app := iris.New()
	app.Use(recover.New())
	app.Use(gciris.New(gciris.Options{CaptureContextErrors: true}))
	app.Get("/checkout", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusInternalServerError, errors.New("checkout failed: out of stock"))
	})
	// Iris requires building the router before serving without app.Run/Listen.
	if err := app.Build(); err != nil {
		fatalf("build: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.com/checkout", nil)
	app.ServeHTTP(rec, req)

	fmt.Printf("served /checkout -> %d; captured=%d\n", rec.Code, gc.GlobalStats().Captured)
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
