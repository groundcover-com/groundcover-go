// Package groundcover is the groundcover runtime SDK for Go. Its v1 scope is
// error tracking: it captures application errors and panics and ships them to
// groundcover without ever affecting the host application.
//
// # Safety guarantees
//
//   - Every public entry point and background task is panic-guarded; internal
//     faults are swallowed (recorded as a self-metric and a throttled log).
//   - Memory is strictly bounded by a ring buffer with both an item-count and a
//     byte budget; on overflow the oldest events are dropped.
//   - Capturing an error never blocks on I/O: the caller enriches the event and
//     performs a single non-blocking hand-off to a background worker that owns
//     all network traffic.
//
// # Usage
//
// The package exposes a package-level default client configured with Init, plus
// an explicit Client (via New) for tests and multi-config setups.
//
//	if err := groundcover.Init(groundcover.Config{
//		DSN:          "https://<ingestion-origin>",
//		IngestionKey: "<key>",
//	}); err != nil {
//		log.Fatal(err)
//	}
//	defer groundcover.Close(context.Background())
//
//	groundcover.CaptureError(ctx, err)
//
// Errors are submitted as events; that this happens over the RUM ingestion
// endpoint in v1 is an implementation detail that may change without affecting
// callers (the SDK owns the path).
//
// # Logs
//
// The SDK can also deliver application logs directly to groundcover — no
// agent, sidecar, or file tailer. The log signal is opt-in via
// Config.EnableLogs and travels on its own bounded pipeline so log volume
// never evicts or delays error events:
//
//	_ = groundcover.Init(groundcover.Config{DSN: dsn, IngestionKey: key, EnableLogs: true})
//	groundcover.LogInfo(ctx, "order created", groundcover.Attributes{"order_id": id})
//	groundcover.LogErrorf(ctx, "charge %s failed", chargeID)
//
// EmitLog and the Log* helpers are the emission primitives; logging-library
// bridges are built on top of them. The gcslog subpackage bridges the standard
// library's log/slog, and bridges for third-party logging libraries live in
// nested modules under contrib/. Records inherit identity and attributes from
// the context scope, can be scrubbed or sampled with Config.BeforeSendLog, and
// are observable via Client.LogStats / GlobalLogStats.
//
// # Instrumenting an existing service
//
// A step-by-step playbook (for humans and AI coding agents) lives in
// docs/llm-instrumentation-guide.md in the repository. The essentials:
//
//   - Call Init exactly once at startup; defer CloseTimeout (or Close) on shutdown.
//   - Capture at boundaries with CaptureError(ctx, err); keep returning the error
//     as before — the SDK observes, it never alters control flow.
//   - Attach identity/attributes to the request context with SetUser / WithScope.
//   - Wrap HTTP servers with the nethttp middleware, a contrib middleware
//     (gin, echo, fiber, fasthttp, iris, negroni), or the contrib/grpc
//     interceptors, to capture panics and seed a per-request scope automatically.
//   - Pass the real error value (not a formatted string) so the type is extracted
//     and grouping works; always thread the request context.
//   - Scrub PII/secrets in BeforeSend; pseudonymize identity with an IdentityHasher.
package groundcover
