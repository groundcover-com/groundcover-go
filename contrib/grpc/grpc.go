// Package grpc provides gRPC server interceptors that recover panics, capture
// them (and optionally server-fault RPC errors) through the core SDK, and seed
// a fresh request scope. It is a separate module so the gRPC dependency never
// enters the core SDK's go.sum.
//
// gRPC servers have no built-in panic recovery: the interceptors re-raise
// captured panics (unless Options.DisableRepanic is set), which terminates the
// process unless something above them recovers. Because the crash would also
// take the SDK's async queue down, the interceptors perform a bounded,
// best-effort flush before re-raising so the panic event survives. If the
// process must survive handler panics, install a recovery interceptor ahead of
// these in the chain:
//
//	grpc.ChainUnaryInterceptor(recoveryInterceptor, gcgrpc.UnaryServerInterceptor(gcgrpc.Options{}))
package grpc

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gc "github.com/groundcover-com/groundcover-go" // pragma: allowlist secret
)

// Options configures the interceptors. The zero value is valid and captures
// panics only: the panic is captured as an unhandled error and re-raised so
// the server behaves exactly as it would without the interceptor.
type Options struct {
	// CaptureRPCErrors turns ON capturing RPC handler errors as handled
	// errors. Off by default: only panics are captured unless this is set.
	// Only server-fault status codes are captured (Unknown, DeadlineExceeded,
	// Unimplemented, Internal, Unavailable, DataLoss); client and flow-control
	// codes such as NotFound, InvalidArgument, or Unauthenticated are never
	// captured: they are request outcomes, not application faults.
	CaptureRPCErrors bool

	// DisableRepanic turns OFF re-raising the panic after capture, so the
	// interceptor swallows it instead (the RPC then completes without a
	// response; prefer an outer recovery interceptor that converts panics to
	// codes.Internal). Leave this off when such a recovery interceptor is
	// installed, or when the process should crash on panics as it would
	// without the interceptor.
	DisableRepanic bool
}

// UnaryServerInterceptor returns a gRPC unary server interceptor that reports
// to the package-level default client configured with the core SDK's Init.
func UnaryServerInterceptor(opts Options) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		ctx = gc.WithIsolatedScope(ctx)

		defer func() {
			if rec := recover(); rec != nil {
				gc.CaptureRecovered(ctx, rec, panicAttributes(info.FullMethod))
				if !opts.DisableRepanic {
					// gRPC servers have no built-in recovery: unless an outer
					// interceptor recovers, the re-raised panic kills the
					// process and the async queue with it. Flush (bounded,
					// best-effort) so the event survives the crash.
					flushBestEffort() //nolint:contextcheck // deliberate detached flush before re-raise
					panic(rec)
				}
			}
		}()

		resp, err = handler(ctx, req)
		maybeCaptureRPCError(ctx, opts, err, info.FullMethod)
		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that
// reports to the package-level default client configured with the core SDK's
// Init.
func StreamServerInterceptor(opts Options) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		ctx := gc.WithIsolatedScope(ss.Context())
		wrapped := &wrappedServerStream{ServerStream: ss, ctx: ctx}

		defer func() {
			if rec := recover(); rec != nil {
				gc.CaptureRecovered(ctx, rec, panicAttributes(info.FullMethod))
				if !opts.DisableRepanic {
					// See UnaryServerInterceptor: flush so the event survives a
					// process-killing re-raise.
					flushBestEffort() //nolint:contextcheck // deliberate detached flush before re-raise
					panic(rec)
				}
			}
		}()

		err = handler(srv, wrapped)
		maybeCaptureRPCError(ctx, opts, err, info.FullMethod)
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

func maybeCaptureRPCError(ctx context.Context, opts Options, err error, fullMethod string) {
	if !opts.CaptureRPCErrors || err == nil {
		return
	}
	code := rpcCode(err)
	if !isServerFault(code) {
		return
	}
	gc.CaptureError(ctx, err, errorAttributes(fullMethod, code))
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

// panicFlushTimeout bounds the best-effort flush performed before re-raising a
// panic that may take the process down (mirrors the core SDK's Recover).
const panicFlushTimeout = 2 * time.Second

func flushBestEffort() {
	ctx, cancel := context.WithTimeout(context.Background(), panicFlushTimeout)
	defer cancel()
	_ = gc.Flush(ctx)
}
