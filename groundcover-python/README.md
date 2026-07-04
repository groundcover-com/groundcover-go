# groundcover-python

The official [groundcover](https://groundcover.com) error tracking library for Python.

> **v1 scope: error tracking.** Tracing, profiling, logs, and metrics producers
> are planned on top of the same shared core.

`groundcover-python` captures application errors and uncaught exceptions and
ships them to groundcover with a strong safety guarantee: **the library never
affects the host application**. Every entry point and background task is
exception-guarded, memory is strictly bounded, and capturing an error never
blocks the caller.

This is the Python translation of
[groundcover-com/groundcover-go](https://github.com/groundcover-com/groundcover-go);
the wire format, grouping fingerprints, and safety guarantees are shared
across both SDKs.

## Install

The project uses [uv](https://docs.astral.sh/uv/):

```bash
uv add groundcover
```

The core library depends on the **standard library only**.

## Quick start

```python
import groundcover

def main() -> None:
    # service.name/env/release/pod are auto-detected from the environment
    # (OTEL_SERVICE_NAME, Downward API). See "Getting your DSN and ingestion key" below.
    groundcover.init(
        dsn="https://<tenant>.platform.grcv.io",
        ingestion_key="<rum-ingestion-key>",
    )
    try:
        try:
            do_work()
        except Exception as exc:
            groundcover.capture_error(exc)
            raise  # unchanged control flow
    finally:
        groundcover.close(timeout=5.0)  # bounded flush on shutdown
```

### Getting your DSN and ingestion key

- **`dsn`** — your BYOC ingestion origin, e.g. `https://<tenant>.platform.grcv.io`.
  Find it in the groundcover UI under **Settings → Access → Ingestion Keys**.
- **`ingestion_key`** — a **RUM-type** write key from the same screen
  (**Ingestion Keys** tab → create key). It is **required** when posting to a
  cloud/BYOC origin; capture never raises at the call site, so a missing or wrong
  key shows up as *no data* rather than an exception. It is optional **only** when
  `dsn` points at a local in-cluster sensor (which needs no auth).

### More usage

- **[`examples/`](examples)** — runnable programs: `basic`, `wsgi_app`, and
  `before_send`. Run e.g. `uv run examples/basic.py`.
- **[`docs/llm-instrumentation-guide.md`](docs/llm-instrumentation-guide.md)** — a
  step-by-step guide for AI coding agents (and humans) instrumenting an existing
  service.

## Design principles

1. **Never affect the host.** All public entry points and background threads are
   exception-guarded; library-internal faults are swallowed (self-metric + throttled log).
2. **Memory is always bounded.** A buffer bounded by both item count and a
   byte budget drops the *oldest* events on overflow.
3. **Capture never blocks.** Callers enrich and perform one non-blocking hand-off
   to a background worker thread that owns all network traffic.
4. **OTel semantics, no OTel dependency.** OTel attribute naming on the wire; no
   `opentelemetry` dependency in core.
5. **Stdlib-only core.** Optional integrations with third-party dependencies are
   declared as extras and imported lazily.
6. **Self-observable.** Counters via `Client.stats()` / `groundcover.global_stats()`
   and an optional Prometheus bridge; logs are self-throttling.

## Optional integrations

| Integration | Import | Adds |
| ----------- | ------ | ---- |
| WSGI middleware | `groundcover.wsgi.GroundcoverMiddleware` | stdlib only (part of core) |
| ASGI middleware | `groundcover.asgi.GroundcoverASGIMiddleware` | stdlib only (part of core) |
| Prometheus bridge | `groundcover.prometheus.Collector` | `prometheus-client` (extra: `groundcover[prometheus]`) |

```bash
uv add "groundcover[prometheus]"
```

## Runtime support

The library supports **Python 3.9+** (CPython). Every released library version
keeps working for the runtime it shipped against; pin an older library release
if you run an older Python.

## Development

```bash
make ci       # sync + lint + tests — the gate for every change
make sync     # uv sync --all-extras
make lint     # ruff check + format check
make test     # pytest
```

AI agents must never author commits; see [`AGENTS.md`](AGENTS.md).

## License

[Apache 2.0](LICENSE).
