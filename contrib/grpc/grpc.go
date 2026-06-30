// Package grpc provides gRPC server interceptors that recover panics, capture
// them (and optionally RPC errors) through groundcover, and seed a fresh request
// scope. It is a separate module so the gRPC dependency never enters the core
// SDK's go.sum.
package grpc

import (
	"context"
	"strings"

	gc "github.com/groundcover-com/groundcover-go"
	"google.golang.org/grpc"
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
// Enabled by default.
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
				captureRecovered(ctx, cfg.client, rec, rpcAttributes(info.FullMethod))
				panic(rec)
			}
		}()

		resp, err = handler(ctx, req)
		if cfg.captureError && err != nil {
			captureError(ctx, cfg.client, err, rpcAttributes(info.FullMethod))
		}
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
				captureRecovered(ctx, cfg.client, rec, rpcAttributes(info.FullMethod))
				panic(rec)
			}
		}()

		err = handler(srv, wrapped)
		if cfg.captureError && err != nil {
			captureError(ctx, cfg.client, err, rpcAttributes(info.FullMethod))
		}
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

func rpcAttributes(fullMethod string) gc.Option {
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
	return gc.WithAttributes(attrs)
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
