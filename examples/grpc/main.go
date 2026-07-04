// Command grpc shows gRPC server interceptors: they recover panics, capture RPC
// errors, and seed a fresh scope per call.
//
//	GC_DSN=https://<ingestion-origin> GC_INGESTION_KEY=<key> go run .
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gc "github.com/groundcover-com/groundcover-go"
	gcgrpc "github.com/groundcover-com/groundcover-go/contrib/grpc"
)

func main() {
	if err := gc.Init(gc.Config{
		DSN:          envOr("GC_DSN", "https://example.invalid"),
		IngestionKey: os.Getenv("GC_INGESTION_KEY"),
		ServiceName:  "examples-grpc",
		Env:          "examples",
	}); err != nil {
		fatalf("init: %v", err)
	}
	defer func() { _ = gc.CloseTimeout(5 * time.Second) }()

	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{CaptureRPCErrors: true})
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Internal, "checkout failed: out of stock")
	})

	_, _ = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/checkout.Service/Checkout"}, handler)

	fmt.Printf("handled /checkout.Service/Checkout; captured=%d\n", gc.GlobalStats().Captured)
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
