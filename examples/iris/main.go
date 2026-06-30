// Command iris shows the Iris middleware: it recovers panics, captures context
// errors, and seeds a fresh scope per request.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	gc "github.com/groundcover-com/groundcover-go"
	gciris "github.com/groundcover-com/groundcover-go/contrib/iris"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/recover"
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
	app.Use(gciris.Middleware())
	app.Get("/checkout", func(ctx iris.Context) {
		ctx.StopWithError(http.StatusInternalServerError, errors.New("checkout failed: out of stock"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://example.com/checkout", nil)
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
