# groundcover-go

The official [groundcover](https://groundcover.com) error tracking library for Go.

> **Note:** This library is for instrumenting Go applications with groundcover
> error tracking. For the full groundcover client SDK library, see
> [groundcover-com/groundcover-sdk-go](https://github.com/groundcover-com/groundcover-sdk-go).

[![Go Reference](https://pkg.go.dev/badge/github.com/groundcover-com/groundcover-go.svg)](https://pkg.go.dev/github.com/groundcover-com/groundcover-go)
[![CI](https://github.com/groundcover-com/groundcover-go/actions/workflows/ci.yml/badge.svg)](https://github.com/groundcover-com/groundcover-go/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/groundcover-com/groundcover-go)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

> **Scope: error tracking, plus direct log delivery (opt-in).** Tracing,
> profiling, and metrics producers are planned on top of the same shared core.

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

### Logs — no agent, no tailer

Use the public `logs` package when the program needs only log delivery:

```go
import "github.com/groundcover-com/groundcover-go/logs"

client, err := logs.New(logs.Config{
	DSN:          dsn,
	IngestionKey: logsKey,
	ServiceName:  "checkout",
	Env:          "production",
	Cluster:      "cluster-a",
})
if err != nil {
	return err
}
defer client.CloseTimeout(5 * time.Second)

client.Info(ctx, "order created", logs.Attributes{"order_id": "o-9"})
```

`logs.Client` independently owns its queue, batching, retries, statistics, and
shutdown. It does not initialize error tracking or any other signal.

For programs already using the aggregate SDK, `EnableLogs: true` composes the
same standalone client behind the root convenience API:

With `EnableLogs: true`, the SDK delivers application logs straight to
groundcover's dedicated `/json/logs` intake as NDJSON — no local agent or
tailer. Logs travel on their **own bounded queue**, so log volume never evicts
or delays error events.

```go
groundcover.Config{
	EnableLogs:       true,
	LogsIngestionKey: "<logs-ingestion-key>", // omit when IngestionKey works for both intakes
	LogSource:        "my-source",             // defaults to "go"
}
```

Each NDJSON record includes `service`, `env`, `cluster`, and `source`, populated
from `ServiceName`, `Env`, `Cluster`, and `LogSource`. It also uses an RFC 3339
Nano `timestamp`, `msg`, and `level`; all other fields are retained as log
attributes. Resource metadata is deliberately per-record rather than encoded
in HTTP headers.

There are two layers, by design:

1. **The emission API** — push a log record (and, later, other signals)
   directly from code. This is also what auto-collection and third-party
   integrations build on:

```go
groundcover.LogInfo(ctx, "order created", groundcover.Attributes{"order_id": id})
groundcover.LogErrorf(ctx, "charge %s failed after %d tries", chargeID, tries)
```

2. **Bridges** — adapters from existing logging libraries onto the emission
   API, Sentry-style. The stdlib `log/slog` bridge ships in core (zero extra
   dependencies); bridges for third-party loggers (logrus, zap, ...) will live
   in `contrib/` nested modules:

```go
import "github.com/groundcover-com/groundcover-go/gcslog"

logger := slog.New(gcslog.NewHandler(gcslog.Options{})) // uses the Init'd client
logger.Info("order created", "order_id", "o-9")         // delivered to groundcover
```

With a standalone client, pass it explicitly:

```go
logger := slog.New(gcslog.NewHandler(gcslog.Options{Client: client}))
```

Records inherit identity/attributes from the request scope (`SetUser` /
`WithScope`), can be scrubbed or sampled with `BeforeSendLog`, and are
observable via `LogStats()` and the Prometheus bridge
(`groundcover_sdk_logs_*`).

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
| slog bridge (logs) | `github.com/groundcover-com/groundcover-go/gcslog` | stdlib only (part of core) |
| standalone logs client | `github.com/groundcover-com/groundcover-go/logs` | stdlib only (part of core) |
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

The contrib modules declare these minimum framework versions — the oldest
releases the middleware is built and tested against. They never sit below a
release with a known published vulnerability fix, which is why Fiber requires
`v2.52.13`: every earlier `v2.52.x` patch has CVE fixes above it.

| Framework | Minimum version |
| --------- | --------------- |
| Echo (`v4`) | `v4.10.0` |
| FastHTTP | `v1.52.0` |
| Fiber (`v2`) | `v2.52.13` |
| Gin | `v1.9.1` |
| gRPC | `v1.80.0` |
| Iris (`v12`) | `v12.2.0` |
| Negroni (`v3`) | `v3.1.1` |

Projects on newer framework versions are unaffected: Go's minimal version
selection keeps whichever version your own `go.mod` requires.

## Runtime support

The library supports the **two most recent Go majors** (today **1.25** and **1.26**),
matching the major Go observability SDKs (dd-trace-go, otel-go). The `go.mod`
floor is the older of the two.

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
