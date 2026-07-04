// Package grpc provides gRPC server interceptors that recover panics, capture
// them (and server-fault RPC errors) through the SDK, and seed a fresh
// request scope. It is a separate module so the gRPC dependency never enters
// the core SDK's go.sum.
//
// gRPC servers have no built-in panic recovery: the interceptors re-raise
// captured panics, which terminates the process unless something above them
// recovers. Because the crash would also take the SDK's async queue down, the
// interceptors perform a bounded, best-effort flush before re-raising so the
// panic event survives. If the process must survive handler panics, install a
// recovery interceptor ahead of these in the chain:
//
//	grpc.ChainUnaryInterceptor(recoveryInterceptor, gcgrpc.UnaryServerInterceptor())
package grpc

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gc "github.com/groundcover-com/groundcover-go"
)

type config struct {
	client       *gc.Client
	captureError bool
}

// Option configures the interceptors.
type Option func(*config)

// WithClient routes captures to an explicit client instead of the global one.
func WithClient(c *gc.Client) Option {
	return func(cfg *config) { cfg.client = c }
}

// WithErrorCapture toggles capturing RPC handler errors as handled errors.
// Enabled by default. Only server-fault status codes are captured (Unknown,
// DeadlineExceeded, Unimplemented, Internal, Unavailable, DataLoss); client
// and flow-control codes such as NotFound, InvalidArgument, or Unauthenticated
// are never captured: they are request outcomes, not application faults.
func WithErrorCapture(enabled bool) Option {
	return func(cfg *config) { cfg.captureError = enabled }
}

// UnaryServerInterceptor returns a gRPC unary server interceptor.
func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	cfg := config{captureError: true}
	for _, o := range opts {
		o(&cfg)
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		ctx = seedScope(ctx, cfg.client)

		defer func() {
			if rec := recover(); rec != nil {
				captureRecovered(ctx, cfg.client, rec, panicAttributes(info.FullMethod))
				// gRPC servers have no built-in recovery: unless an outer
				// interceptor recovers, the re-raised panic kills the process
				// and the async queue with it. Flush (bounded, best-effort) so
				// the event survives the crash.
				flushBestEffort(cfg.client) //nolint:contextcheck // deliberate detached flush before re-raise
				panic(rec)
			}
		}()

		resp, err = handler(ctx, req)
		maybeCaptureRPCError(ctx, cfg, err, info.FullMethod)
		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor.
func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	cfg := config{captureError: true}
	for _, o := range opts {
		o(&cfg)
	}

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		ctx := seedScope(ss.Context(), cfg.client)
		wrapped := &wrappedServerStream{ServerStream: ss, ctx: ctx}

		defer func() {
			if rec := recover(); rec != nil {
				captureRecovered(ctx, cfg.client, rec, panicAttributes(info.FullMethod))
				// See UnaryServerInterceptor: flush so the event survives a
				// process-killing re-raise.
				flushBestEffort(cfg.client) //nolint:contextcheck // deliberate detached flush before re-raise
				panic(rec)
			}
		}()

		err = handler(srv, wrapped)
		maybeCaptureRPCError(ctx, cfg, err, info.FullMethod)
		return err
	}
}

type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

func maybeCaptureRPCError(ctx context.Context, cfg config, err error, fullMethod string) {
	if !cfg.captureError || err == nil {
		return
	}
	code := rpcCode(err)
	if !isServerFault(code) {
		return
	}
	captureError(ctx, cfg.client, err, errorAttributes(fullMethod, code))
}

// rpcCode resolves the gRPC status code for err, mapping bare context errors
// (context.Canceled, context.DeadlineExceeded) to their canonical codes.
func rpcCode(err error) codes.Code {
	if s, ok := status.FromError(err); ok {
		return s.Code()
	}
	return status.FromContextError(err).Code()
}

// isServerFault reports whether the status code indicates a server-side fault
// worth capturing, per OTel RPC server span-status conventions.
func isServerFault(code codes.Code) bool {
	switch code {
	case codes.Unknown, codes.DeadlineExceeded, codes.Unimplemented,
		codes.Internal, codes.Unavailable, codes.DataLoss:
		return true
	default:
		return false
	}
}

func panicAttributes(fullMethod string) gc.Option {
	return gc.WithAttributes(baseAttributes(fullMethod))
}

func errorAttributes(fullMethod string, code codes.Code) gc.Option {
	attrs := baseAttributes(fullMethod)
	attrs["rpc.grpc.status_code"] = int(code)
	return gc.WithAttributes(attrs)
}

func baseAttributes(fullMethod string) gc.Attributes {
	service, method := parseFullMethod(fullMethod)
	attrs := gc.Attributes{
		"rpc.system": "grpc",
	}
	if service != "" {
		attrs["rpc.service"] = service
	}
	if method != "" {
		attrs["rpc.method"] = method
	}
	return attrs
}

func parseFullMethod(fullMethod string) (service, method string) {
	if !strings.HasPrefix(fullMethod, "/") {
		return "", ""
	}
	name := fullMethod[1:]
	pos := strings.Index(name, "/")
	if pos < 0 {
		return name, ""
	}
	return name[:pos], name[pos+1:]
}

func seedScope(ctx context.Context, client *gc.Client) context.Context {
	if client != nil {
		return client.WithIsolatedScope(ctx)
	}
	return gc.WithIsolatedScope(ctx)
}

func captureRecovered(ctx context.Context, client *gc.Client, rec any, opts ...gc.Option) {
	if client != nil {
		client.CaptureRecovered(ctx, rec, opts...)
		return
	}
	gc.CaptureRecovered(ctx, rec, opts...)
}

func captureError(ctx context.Context, client *gc.Client, err error, opts ...gc.Option) {
	if client != nil {
		client.CaptureError(ctx, err, opts...)
		return
	}
	gc.CaptureError(ctx, err, opts...)
}

// panicFlushTimeout bounds the best-effort flush performed before re-raising a
// panic that may take the process down (mirrors the core SDK's Recover).
const panicFlushTimeout = 2 * time.Second

func flushBestEffort(client *gc.Client) {
	ctx, cancel := context.WithTimeout(context.Background(), panicFlushTimeout)
	defer cancel()
	if client != nil {
		_ = client.Flush(ctx)
		return
	}
	_ = gc.Flush(ctx)
}
