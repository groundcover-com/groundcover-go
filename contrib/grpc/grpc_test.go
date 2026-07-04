package grpc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
	gcgrpc "github.com/groundcover-com/groundcover-go/contrib/grpc"
)

// initDropClient installs a package-level client that drops every event in
// BeforeSend, so tests observe captures via GlobalStats without any delivery.
func initDropClient(t *testing.T) {
	t.Helper()
	if err := gc.Init(gc.Config{
		DSN:           "https://example.invalid",
		FlushInterval: time.Hour,
		BeforeSend:    func(*gc.Event) *gc.Event { return nil },
	}); err != nil {
		t.Fatalf("init client: %v", err)
	}
	t.Cleanup(func() { _ = gc.Close(context.Background()) })
}

func TestUnaryCapturesPanicAndReRaises(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{})
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

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestUnaryDisableRepanicSwallowsPanic(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{DisableRepanic: true})
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		panic("grpc boom")
	})

	// Must not panic: the interceptor swallows it after capturing.
	_, _ = interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

func TestUnaryCapturesHandlerErrorsWhenEnabled(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{CaptureRPCErrors: true})
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Internal, "rpc failed")
	})

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	if err == nil {
		t.Fatal("expected handler error")
	}
	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured handler error, got %d", got)
	}
}

func TestUnaryHandlerErrorsNotCapturedByDefault(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{})
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		return nil, status.Error(codes.Internal, "rpc failed")
	})

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	if err == nil {
		t.Fatal("expected handler error")
	}
	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("RPC errors must not be captured by default, got %d", got)
	}
}

func TestUnarySkipsClientErrors(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{CaptureRPCErrors: true})

	for _, code := range []codes.Code{codes.NotFound, codes.InvalidArgument, codes.Unauthenticated, codes.Canceled} {
		handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
			return nil, status.Error(code, "client-side outcome")
		})
		_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
		if err == nil {
			t.Fatal("expected handler error")
		}
	}

	if got := gc.GlobalStats().DroppedBeforeSend; got != 0 {
		t.Fatalf("expected no captures for client-side status codes, got %d", got)
	}
}

func TestUnaryCapturesBareContextDeadline(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.UnaryServerInterceptor(gcgrpc.Options{CaptureRPCErrors: true})
	handler := grpc.UnaryHandler(func(context.Context, any) (any, error) {
		return nil, context.DeadlineExceeded
	})

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}, handler)
	if err == nil {
		t.Fatal("expected handler error")
	}
	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured deadline error, got %d", got)
	}
}

func TestStreamCapturesHandlerErrors(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.StreamServerInterceptor(gcgrpc.Options{CaptureRPCErrors: true})
	handler := grpc.StreamHandler(func(any, grpc.ServerStream) error {
		return status.Error(codes.Internal, "stream failed")
	})

	err := interceptor(nil, &stubServerStream{ctx: context.Background()}, &grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"}, handler)
	if err == nil {
		t.Fatal("expected handler error")
	}
	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured stream error, got %d", got)
	}
}

func TestStreamCapturesPanicAndReRaises(t *testing.T) {
	initDropClient(t)
	interceptor := gcgrpc.StreamServerInterceptor(gcgrpc.Options{})
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

	if got := gc.GlobalStats().DroppedBeforeSend; got != 1 {
		t.Fatalf("expected 1 captured panic, got %d", got)
	}
}

type stubServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *stubServerStream) Context() context.Context { return s.ctx }
