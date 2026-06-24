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
// The package exposes a Sentry-style global client configured with Init, plus an
// explicit Client (via New) for tests and multi-config setups.
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
package groundcover
