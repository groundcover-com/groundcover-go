# Examples

Runnable programs demonstrating the groundcover Go SDK. This is a separate Go
module (with a local `replace` onto the SDK) so example/integration dependencies
never enter the core library's `go.sum`.

| Example | What it shows | Run |
| ------- | ------------- | --- |
| [`basic`](basic) | init, user + custom attributes, `CaptureError`, `CaptureMessage`, bounded shutdown | `go run ./basic` |
| [`nethttp`](nethttp) | `net/http` middleware (panic recovery + per-request scope) | `go run ./nethttp` |
| [`gin`](gin) | Gin middleware (panic recovery + `c.Error` capture) | `go run ./gin` |
| [`roundtrip`](roundtrip) | **end-to-end**: submit an error, then query the events API and print what came back (also the CI verifier) | `go run ./roundtrip` |

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
```

For API-level snippets that render on pkg.go.dev, see
[`example_test.go`](../example_test.go) in the repository root.
