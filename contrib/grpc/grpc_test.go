package grpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gc "github.com/groundcover-com/groundcover-go"
	gcgrpc "github.com/groundcover-com/groundcover-go/contrib/grpc"
)

func newDropClient(t *testing.T) *gc.Client {
	t.Helper()
	c, err := gc.New(gc.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*gc.Event) *gc.Event { return nil },
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

func TestUnaryCapturesPanicAndReRaises(t *testing.T) {
	client := newDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.WithClient(client))
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		panic("grpc boom")
	})

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		_, _ = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	}()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestUnaryCapturesHandlerErrors(t *testing.T) {
	client := newDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.WithClient(client))
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Internal, "rpc failed")
	})

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	if err == nil {
		t.Fatal("expected handler error")
	}
	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured handler error, got %d", got)
	}
}

func TestStreamCapturesPanicAndReRaises(t *testing.T) {
	client := newDropClient(t)
	interceptor := gcgrpc.StreamServerInterceptor(gcgrpc.WithClient(client))
	handler := grpc.StreamHandler(func(any, grpc.ServerStream) error {
		panic("stream boom")
	})

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("panic must be re-raised")
			}
		}()
		_ = interceptor(nil, &stubServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}, handler)
	}()

	if got := client.Stats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

type stubServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *stubServerStream) Context() context.Context { return s.ctx }
