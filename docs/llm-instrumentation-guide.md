# Instrumenting a Go service with groundcover-go — guide for AI coding agents

This guide tells an automated coding agent (or a human in a hurry) exactly how to
add groundcover **error tracking** to an existing Go service using this SDK. It
is prescriptive on purpose: follow the steps and rules below and the result will
be correct, safe, and idiomatic.

The SDK's prime directive is **never affect the host application**: every entry
point is panic-guarded, capture never blocks on I/O, and memory is bounded. You
can therefore call it freely without defensive wrapping.

---

## 0. Mental model (read first)

- One process-wide client, configured **once** at startup with `Init`, flushed on
  shutdown with `Close`. Use the package-level functions everywhere else.
- You **capture errors at boundaries**, you do not replace error handling. After
  `CaptureError`, return the error as you normally would.
- Request-scoped data (user, custom attributes) lives on the **context**. Set it
  with `SetUser` / `WithScope`; `CaptureError(ctx, …)` reads it back.
- Merge precedence is deterministic: **process defaults (Init) < request scope
  (ctx) < per-call options**.

## Guarantees (what you can rely on)

These are the usage-side contracts; each is covered by an integration test:

- **Capture never blocks and never panics the host.** Even against a dead/slow
  backend, `CaptureError`/`CaptureMessage`/`Recover` return immediately.
- **Control flow is unchanged.** Captured errors are still yours to return;
  panics are re-raised (the SDK observes, it never swallows).
- **Request scope flows through.** Identity/attributes set by a handler on the
  request context (directly or via middleware) appear on the captured event —
  no need to thread the returned context back.
- **Per-request isolation.** One request's identity never leaks into another's
  event.
- **Panics are fatal.** A recovered panic is captured `handled=false` at fatal
  severity and a scope level cannot downgrade it.
- **Memory is bounded.** Overflow drops the oldest events (and is counted); it
  never grows unbounded or applies backpressure to the caller.
- **`Disabled: true` does zero I/O.**
- **PII:** only `user.id`/`user.email` are hashed (with a `Hasher`); scrub
  anything else via `BeforeSend`. See the PII surface section below.

---

## 1. Add the dependency

```bash
go get github.com/groundcover-com/groundcover-go
```

Optional integrations (only if the service uses them) are separate modules:

| Need | Import |
| ---- | ------ |
| net/http middleware | `github.com/groundcover-com/groundcover-go/nethttp` |
| Gin middleware | `github.com/groundcover-com/groundcover-go/contrib/gin` |
| Prometheus metrics bridge | `github.com/groundcover-com/groundcover-go/prometheus` |

## 2. Initialize once, at the top of `main`

Add `Init` as early as possible in `main`, and `Close` as a deferred call so
pending events are flushed on shutdown.

```go
import (
	"log"
	"os"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
)

func main() {
	if err := groundcover.Init(groundcover.Config{
		DSN:          os.Getenv("GC_DSN"),           // base ingestion origin; the SDK appends the path
		IngestionKey: os.Getenv("GC_INGESTION_KEY"), // optional; omit when using a local sensor
		// ServiceName/Env/Release are auto-detected from the environment
		// (OTEL_SERVICE_NAME/GC_SERVICE_NAME, GC_ENV/DEPLOYMENT_ENVIRONMENT, GC_RELEASE)
		// and from the k8s Downward API. Set them explicitly only to override.
		// In Kubernetes you can usually omit ServiceName — the groundcover sensor
		// enriches pod -> workload server-side.
	}); err != nil {
		log.Fatalf("groundcover init: %v", err)
	}
	defer groundcover.CloseTimeout(5 * time.Second) // bounded flush on shutdown
	// Or, to compose with an existing shutdown context:
	//   defer groundcover.Close(shutdownCtx)

	// ... start the app ...
}
```

Rules:

- Call `Init` **exactly once**. Never call it per-request or per-goroutine.
- `DSN` is **required** unless `Disabled: true`. If you cannot determine it, set
  `Disabled: true` (a true no-op, ~zero overhead) rather than guessing.
- For tests / on-prem builds, `groundcover.Config{Disabled: true}` is the switch.

## 3. Capture errors at boundaries (do not change control flow)

Capture where an error is first observed and is meaningful — typically the
outermost place that handles it. Then keep handling it as before.

```go
if err := charge(ctx, orderID); err != nil {
	groundcover.CaptureError(ctx, err)
	return err // unchanged control flow
}
```

Attach per-call context with options:

```go
groundcover.CaptureError(ctx, err,
	groundcover.WithAttributes(groundcover.Attributes{
		"order_id": orderID, // string
		"amount":   42.5,    // number
		"is_retry": true,    // bool
	}),
)
```

Available options: `WithAttributes`, `WithUser`, `WithLevel`, `WithFingerprint`
(overrides the opaque grouping key), `WithTitle` (overrides the human-readable
display label; by default it's derived as `errorType: message`).

Do **not**:

- capture the same error at every layer of the stack — you'll create duplicates.
  Capture once, at the boundary.
- build error strings just to capture them; pass the `error` value so the SDK can
  extract the type, unwrap `%w`, and group correctly.

## 4. Attach identity and request scope via context

`SetUser` and `WithScope` return a **new context** carrying request-scoped data.
Thread that context through the request; every `CaptureError(ctx, …)` then
includes it automatically.

```go
ctx = groundcover.SetUser(ctx, groundcover.User{
	ID:           user.ID,
	Email:        user.Email,
	Organization: user.TenantID, // B2B group key
})

ctx = groundcover.WithScope(ctx, func(s *groundcover.Scope) {
	s.SetAttribute("feature", "checkout")
	s.SetSessionID(sessionID)
})
```

## 5. Recover panics

### In a goroutine you own

```go
go func() {
	defer groundcover.Recover(ctx) // captures the panic, then re-raises it
	doRiskyWork()
}()
```

`Recover` re-raises by default (it observes, it does not swallow). If you own the
response lifecycle and do **not** want re-raise, use `CaptureRecovered`:

```go
defer func() {
	if r := recover(); r != nil {
		groundcover.CaptureRecovered(ctx, r)
		// ... write a 500, etc. ...
	}
}()
```

### Behind HTTP middleware (preferred for servers)

net/http:

```go
import gchttp "github.com/groundcover-com/groundcover-go/nethttp"

mux := http.NewServeMux()
// ... register handlers ...
srv := &http.Server{Handler: gchttp.Middleware(mux)}
```

Gin:

```go
import gcgin "github.com/groundcover-com/groundcover-go/contrib/gin"

r := gin.New()
r.Use(gcgin.Middleware()) // recovers panics, captures c.Errors, seeds a scope
```

The middleware seeds a fresh, isolated scope into each request's context, so
handler code can call `SetUser`/`WithScope` on `r.Context()` and the captured
error sees it — no need to thread the returned context back, and nothing leaks
across requests.

### Middleware composition (order matters)

- **Gin:** register a recovery middleware **before** `gcgin.Middleware()`, or use
  `gin.Default()` (which includes `gin.Recovery()`). Our middleware re-raises the
  panic after capturing; if nothing downstream recovers it, Gin aborts the
  connection instead of returning 500.

  ```go
  r := gin.New()
  r.Use(gin.Recovery())      // must be registered before ours
  r.Use(gcgin.Middleware())
  ```

- **Don't double-wrap:** wrapping a Gin engine in `nethttp.Middleware` *and* using
  `gcgin.Middleware()` captures the same panic twice unless a terminating
  `gin.Recovery()` sits between the layers. Pick one middleware per server.

## 6. Non-error notices

```go
groundcover.CaptureMessage(ctx, "falling back to stale cache", groundcover.LevelWarning)
```

Levels: `LevelDebug`, `LevelInfo`, `LevelWarning`, `LevelError`, `LevelFatal`.

## 7. Scrub PII / secrets (when handling sensitive data)

`BeforeSend` is the single chokepoint. Return `nil` to drop an event; mutate and
return it to scrub. It is panic-sandboxed.

```go
groundcover.Config{
	BeforeSend: func(e *groundcover.Event) *groundcover.Event {
		e.ErrorMessage = redactSecrets(e.ErrorMessage)
		delete(e.Attributes, "authorization")
		return e
	},
	Hasher: groundcover.NewHMACHasher([]byte(os.Getenv("GC_PII_KEY"))), // pseudonymize user.id/email
}
```

## 8. Short-lived jobs / serverless

There is no background time to flush, so flush explicitly before exit:

```go
defer groundcover.FlushTimeout(2 * time.Second)
```

`FlushTimeout`/`CloseTimeout` are convenience wrappers; `Flush(ctx)`/`Close(ctx)`
remain the primitives when you need cancellation or to compose with an existing
context.

## 9. Local debugging — see captured events

Set `Debug: true` to print each captured event to stderr in a compact, readable
form. It runs *after* scrubbing/hashing, so it honors `BeforeSend` and `Hasher`,
and it does not affect delivery.

```go
groundcover.Init(groundcover.Config{DSN: dsn, Debug: true})
// [groundcover] error exception  *net.OpError: connection refused
//   fingerprint=836b… handled=true
//   user: id=u-123 org=acme
//   attrs: amount=42.5 order_id=o-9
//   stack:
//     main.charge (/app/checkout.go:42)
```

## 10. Testing your instrumentation

`BeforeSend` is the blessed in-process test seam: it receives the finalized
`*Event`, so you can assert on captures without any network. Record and drop:

```go
var got []*groundcover.Event
groundcover.Init(groundcover.Config{
	DSN:        "https://example.invalid",
	BeforeSend: func(e *groundcover.Event) *groundcover.Event { got = append(got, e); return nil },
})
// ... exercise code, then assert on got ...
```

For wire-level assertions, inject a custom `Config.HTTPClient` and inspect the
gzipped JSON body.

## PII surface (know what leaves the process)

The SDK does not block PII by default (matching Sentry). What it does:

- **`Hasher`** pseudonymizes **only `user.id` and `user.email`**. It does **not**
  touch `user.name`, `user.organization`, custom attributes, or error messages.
- **`BeforeSend`** is the one place to scrub everything else — `ErrorMessage`,
  `Attributes`, `error_stacktrace`, and any identity field. Return `nil` to drop.
- Raw client IPs are never sent (geo/IP is derived server-side).
- SDK-internal logs record the **type** of a recovered internal panic, not its
  value, to avoid leaking data.

If your errors or attributes may carry PII, write a `BeforeSend` scrubber.

---

## Decision checklist for an agent

1. Is there a `main`? → add `Init` + deferred `Close` there. If multiple binaries,
   instrument each `main`.
2. Is it an HTTP server (net/http or gin)? → add the matching middleware; that
   covers panics and request scope for free.
3. For each place that currently logs or returns an error that matters
   (handlers, background workers, scheduled jobs), add a single
   `groundcover.CaptureError(ctx, err, …)` at the boundary.
4. Is there auth/user context? → `SetUser` once per request (or in middleware).
5. Does the code handle PII/secrets? → add a `BeforeSend` scrubber and/or `Hasher`.
6. Goroutines spawned by the app? → `defer groundcover.Recover(ctx)` at the top of
   each.

## Hard rules (do not violate)

- Never call `Init` more than once; never `Close` mid-request.
- Never let instrumentation change behavior: after capturing, return/propagate the
  error exactly as before.
- Never pass secrets in `DSN`/attributes; use env vars and `BeforeSend`.
- Prefer passing the real `error` (not a formatted string) to `CaptureError`.
- Always thread the request `context.Context` so scope is attached.
- `CaptureError`, `CaptureMessage`, `Recover` never block and never panic — do not
  wrap them in your own recover/timeout logic.

## Verifying the instrumentation

- Build still compiles and `go vet ./...` is clean.
- Capture sites pass a `context.Context` and the original `error`.
- `Init` is reachable on startup; `Close`/`Flush` runs on shutdown.
- For a live check, see [`examples/roundtrip`](../examples/roundtrip): it submits a
  synthetic error and polls the events API until it reads the event back.
