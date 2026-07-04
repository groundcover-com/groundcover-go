# Examples

Runnable programs demonstrating the groundcover Go SDK. This is a separate Go
module (with a local `replace` onto the SDK) so example/integration dependencies
never enter the core library's `go.sum`.

| Example | What it shows | Run |
| ------- | ------------- | --- |
| [`basic`](basic) | init, user + custom attributes, `CaptureError`, `CaptureMessage`, bounded shutdown | `go run ./basic` |
| [`cli`](cli) | batch/worker: capture at boundaries with per-call options, every severity level, request scope, `Recover` + `CaptureRecovered`, and the `Logger`/`OnDrop`/`Debug` hooks; prints events locally with no backend | `go run ./cli` |
| [`before-send`](before-send) | keep PII out and control volume: `BeforeSend` redacts emails/secrets and drops noisy events, `Hasher` pseudonymizes identity | `go run ./before-send` |
| [`explicit-client`](explicit-client) | use an explicit `*Client` (libraries/multi-tenant) instead of the global; wire it into the net/http middleware, run a worker under an isolated scope, and read back `Stats` | `go run ./explicit-client` |
| [`nethttp`](nethttp) | `net/http` middleware (panic recovery + per-request scope) | `go run ./nethttp` |
| [`gin`](gin) | Gin middleware (panic recovery + `c.Error` capture) | `go run ./gin` |
| [`echo`](echo) | Echo middleware (panic recovery + handler error capture) | `go run ./echo` |
| [`fiber`](fiber) | Fiber middleware (panic recovery + handler error capture) | `go run ./fiber` |
| [`fasthttp`](fasthttp) | fasthttp middleware (panic recovery + per-request scope) | `go run ./fasthttp` |
| [`iris`](iris) | Iris middleware (panic recovery + context error capture) | `go run ./iris` |
| [`negroni`](negroni) | Negroni middleware (panic recovery + per-request scope) | `go run ./negroni` |
| [`grpc`](grpc) | gRPC server interceptors (panic recovery + RPC error capture) | `go run ./grpc` |
| [`roundtrip`](roundtrip) | **end-to-end**: submit an error, then query the events API and print what came back (also the CI verifier) | `go run ./roundtrip` |
| [`framework-roundtrip`](framework-roundtrip) | **end-to-end, per framework**: drive one failing request through every integration (net/http, Gin, Echo, Fiber, fasthttp, Iris, Negroni, gRPC), then verify each event via the events API (also the CI verifier) | `go run ./framework-roundtrip` |

Most examples run against a placeholder DSN and never block, so they work without
credentials. To send real data, set the standard environment variables:

```bash
export GC_DSN=https://<ingestion-origin>      # BYOC ingestion origin
export GC_INGESTION_KEY=<write-key>           # RUM-type ingestion key
# roundtrip also needs the read API:
export GC_API_KEY=<read-key>
export GC_API_URL=https://api.groundcover.com
export GC_BACKEND_ID=<backend-id>             # only if multi-backend

go run ./roundtrip
go run ./framework-roundtrip
```

## Testing instrumented code

The framework examples (`gin`, `echo`, `fiber`, `fasthttp`, `iris`, `negroni`, and
`grpc`) include `_test.go` files showing how to test instrumented code without a
live backend. The `cli` example is another reference for hermetic testing patterns.

- a **`BeforeSend` recorder** that snapshots events in-process for synchronous
  assertions (see `gin/gin_test.go`), and
- a custom **`HTTPClient` transport** that captures the final wire payload to assert
  on PII redaction (see `before-send/before_send_test.go`).

```bash
go test ./...
```

For API-level snippets that render on pkg.go.dev, see
[`example_test.go`](../example_test.go) in the repository root.
