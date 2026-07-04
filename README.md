# groundcover-go

The official [groundcover](https://groundcover.com) error tracking library for Go.

> **Note:** This library is for instrumenting Go applications with groundcover
> error tracking. For the full groundcover client SDK library, see
> [groundcover-com/groundcover-sdk-go](https://github.com/groundcover-com/groundcover-sdk-go).

[![Go Reference](https://pkg.go.dev/badge/github.com/groundcover-com/groundcover-go.svg)](https://pkg.go.dev/github.com/groundcover-com/groundcover-go)
[![CI](https://github.com/groundcover-com/groundcover-go/actions/workflows/ci.yml/badge.svg)](https://github.com/groundcover-com/groundcover-go/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/groundcover-com/groundcover-go)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

> **v1 scope: error tracking.** Tracing, profiling, logs, and metrics producers
> are planned on top of the same shared core.

`groundcover-go` captures application errors and panics and ships them to
groundcover with a strong safety guarantee: **the library never affects the host
application**. Every entry point and background task is panic-guarded, memory is
strictly bounded, and capturing an error never blocks the caller.

## Install

```bash
go get github.com/groundcover-com/groundcover-go
```

The core library depends on the **standard library only**.

## Quick start

```go
package main

import (
	"context"
	"log"
	"time"

	groundcover "github.com/groundcover-com/groundcover-go"
)

func main() {
	// service.name/env/release/pod are auto-detected from the environment
	// (OTEL_SERVICE_NAME, Downward API). See "Getting your DSN and ingestion key" below.
	if err := groundcover.Init(groundcover.Config{
		DSN:          "https://<tenant>.platform.grcv.io",
		IngestionKey: "<rum-ingestion-key>",
	}); err != nil {
		log.Fatal(err)
	}
	defer groundcover.CloseTimeout(5 * time.Second) // bounded flush on shutdown

	if err := doWork(); err != nil {
		groundcover.CaptureError(context.Background(), err)
	}
}
```

### Getting your DSN and ingestion key

- **`DSN`** — your BYOC ingestion origin, e.g. `https://<tenant>.platform.grcv.io`.
  Find it in the groundcover UI under **Settings → Access → Ingestion Keys**.
- **`IngestionKey`** — a **RUM-type** write key from the same screen
  (**Ingestion Keys** tab → create key). It is **required** when posting to a
  cloud/BYOC origin; capture never errors at the call site, so a missing or wrong
  key shows up as *no data* rather than an exception. It is optional **only** when
  `DSN` points at a local in-cluster sensor (which needs no auth).

### Web frameworks

Middleware is provided for net/http and every framework in the
[Optional integrations](#optional-integrations) table below. All follow the
same shape — a `New` constructor taking an `Options` struct whose zero value
captures panics only; capturing handler errors is opt-in:

```go
import gcgin "github.com/groundcover-com/groundcover-go/contrib/gin"

r := gin.Default() // gin.Recovery() turns re-raised panics into 500s
r.Use(gcgin.New(gcgin.Options{CaptureContextErrors: true}))
```

Each middleware seeds an isolated per-request scope (so handler
`SetUser`/`WithScope` enrichment is reflected in captured errors), re-raises
panics after capture, and skips client-side outcomes (4xx, router 404s,
client gRPC codes) so they never become error events. See
[`examples/`](examples) for a runnable program per framework and
[`docs/llm-instrumentation-guide.md`](docs/llm-instrumentation-guide.md)
for wiring details, including middleware ordering.

### More usage

- **[`examples/`](examples)** — runnable programs: `basic`, `nethttp`, `gin`,
  `echo`, `fiber`, `fasthttp`, `iris`, `negroni`, `grpc`, and two live
  end-to-end verifiers (`roundtrip`, `framework-roundtrip`) that submit errors
  and query them back. Run e.g. `cd examples && go run ./basic`.
- **[`example_test.go`](example_test.go)** — API-level snippets rendered on pkg.go.dev.
- **[`docs/llm-instrumentation-guide.md`](docs/llm-instrumentation-guide.md)** — a
  step-by-step guide for AI coding agents (and humans) instrumenting an existing
  service.

## Design principles

1. **Never affect the host.** All public entry points and goroutines are
   panic-guarded; library-internal faults are swallowed (self-metric + throttled log).
2. **Memory is always bounded.** A ring buffer bounded by both item count and a
   byte budget drops the *oldest* events on overflow.
3. **Capture never blocks.** Callers enrich and perform one non-blocking hand-off.
4. **OTel semantics, not otel-go.** OTel attribute naming on the wire; no
   `opentelemetry-go` dependency in core.
5. **Minimal, vendored dependencies.** stdlib first; optional integrations live
   in nested modules.
6. **Self-observable.** Counters via `Stats()` and an optional Prometheus bridge;
   logs are self-throttling.

## Optional integrations

| Module | Import path | Adds |
| ------ | ----------- | ---- |
| net/http middleware | `github.com/groundcover-com/groundcover-go/nethttp` | stdlib only (part of core) |
| Echo middleware | `github.com/groundcover-com/groundcover-go/contrib/echo` | `github.com/labstack/echo/v4` |
| FastHTTP middleware | `github.com/groundcover-com/groundcover-go/contrib/fasthttp` | `github.com/valyala/fasthttp` |
| Fiber middleware | `github.com/groundcover-com/groundcover-go/contrib/fiber` | `github.com/gofiber/fiber/v2` |
| Gin middleware | `github.com/groundcover-com/groundcover-go/contrib/gin` | `github.com/gin-gonic/gin` |
| gRPC interceptors | `github.com/groundcover-com/groundcover-go/contrib/grpc` | `google.golang.org/grpc` |
| Iris middleware | `github.com/groundcover-com/groundcover-go/contrib/iris` | `github.com/kataras/iris/v12` |
| Negroni middleware | `github.com/groundcover-com/groundcover-go/contrib/negroni` | `github.com/urfave/negroni/v3` |
| Prometheus bridge | `github.com/groundcover-com/groundcover-go/prometheus` | `github.com/VictoriaMetrics/metrics` |


Each optional integration with third-party dependencies is a **separate Go
module**, so the core `go.sum` stays dependency-free.

## Runtime support

The library supports the **two most recent Go majors** (today **1.25** and **1.26**),
matching dd-trace-go / otel-go / sentry-go. The `go.mod` floor is the older of
the two.

| Library version | Supported Go |
| --------------- | ------------ |
| v0.x            | 1.25, 1.26   |

Every released library version keeps working for the runtime it shipped against;
pin an older library release if you run an older Go.

## Development

```bash
make ci          # build + vet + lint + race tests — the gate for every change
make modules     # build + test the nested modules (contrib, prometheus, examples)
make roundtrip             # live end-to-end example against a real backend (requires GC_* env vars)
make roundtrip-frameworks  # live e2e across all framework integrations (requires GC_* env vars)
```

AI agents must never author commits; see [`AGENTS.md`](AGENTS.md).

## License

[Apache 2.0](LICENSE).
