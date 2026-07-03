// Command gin shows the Gin middleware: it recovers panics, captures errors
// added to the Gin context, and seeds a fresh scope per request. The example
// wires the middleware into an engine, fires a request at a route that records
// an error, and prints the resulting capture count.
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

	"github.com/gin-gonic/gin"

	gc "github.com/groundcover-com/groundcover-go"
	gcgin "github.com/groundcover-com/groundcover-go/contrib/gin"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-gin",
		Env:          "examples",
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gcgin.New(gcgin.Options{}))
	r.GET("/checkout", func(c *gin.Context) {
		_ = c.Error(errors.New("checkout failed: out of stock"))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "checkout failed"})
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/checkout", nil)
	r.ServeHTTP(rec, req)

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
