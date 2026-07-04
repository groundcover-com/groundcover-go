package main

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gc "github.com/groundcover-com/groundcover-go"
	gcgrpc "github.com/groundcover-com/groundcover-go/contrib/grpc"
)

type recorder struct {
	mu     sync.Mutex
	events []gc.Event
}

func (r *recorder) before(e *gc.Event) *gc.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, *e)
	return nil
}

func TestCheckout_CapturesHandledError(t *testing.T) {
	rec := &recorder{}
	if err := gc.Init(gc.Config{
		DSN:         "http://127.0.0.1:0",
		ServiceName: "examples-grpc-test",
		BeforeSend:  rec.before,
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.CloseTimeout(0) })

	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{CaptureRPCErrors: true})
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Internal, "checkout failed")
	})

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/checkout.Service/Checkout"}, handler)
	if err == nil {
		t.Fatal("expected handler error")
	}

	if len(rec.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(rec.events))
	}
	if !rec.events[0].ErrorHandled {
		t.Fatalf("want handled error")
	}
}
