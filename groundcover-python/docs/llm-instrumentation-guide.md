# Instrumenting a Python service with groundcover-python — guide for AI coding agents

This guide tells an automated coding agent (or a human in a hurry) exactly how to
add groundcover **error tracking** to an existing Python service using this SDK.
It is prescriptive on purpose: follow the steps and rules below and the result
will be correct, safe, and idiomatic.

The SDK's prime directive is **never affect the host application**: every entry
point is exception-guarded, capture never blocks on I/O, and memory is bounded.
You can therefore call it freely without defensive wrapping.

---

## 0. Mental model (read first)

- One process-wide client, configured **once** at startup with `init()`, flushed
  on shutdown with `close()`. Use the module-level functions everywhere else.
- You **capture errors at boundaries**, you do not replace error handling. After
  `capture_error`, re-raise or return as you normally would.
- Request-scoped data (user, custom attributes) lives on **contextvars**. Set it
  with `set_user` / `with_scope`; `capture_error(exc)` reads it back from the
  current context.
- Merge precedence is deterministic: **process defaults (init) < request scope
  (contextvars) < per-call keyword options**.

## Guarantees (what you can rely on)

These are the usage-side contracts; each is covered by a test:

- **Capture never blocks and never raises into the host.** Even against a
  dead/slow backend, `capture_error`/`capture_message`/`recover` return
  immediately.
- **Control flow is unchanged.** Captured errors are still yours to re-raise;
  `recover()` re-raises (the SDK observes, it never swallows).
- **Request scope flows through.** Identity/attributes set by a handler in the
  request context (directly or via middleware) appear on the captured event.
- **Per-request isolation.** One request's identity never leaks into another's
  event (middleware seeds an isolated scope per request).
- **Uncaught exceptions are fatal.** An exception captured via `recover()` /
  `capture_recovered` / middleware is `handled=false` at fatal severity and a
  scope level cannot downgrade it.
- **Memory is bounded.** Overflow drops the oldest events (and is counted); it
  never grows unbounded or applies backpressure to the caller.
- **`disabled=True` does zero I/O.**
- **PII:** only `user.id`/`user.email` are hashed (with a `hasher`); scrub
  anything else via `before_send`. See the PII surface section below.

---

## 1. Add the dependency

```bash
uv add groundcover
```

Optional integrations (only if the service uses them):

| Need | Import |
| ---- | ------ |
| WSGI middleware | `groundcover.wsgi.GroundcoverMiddleware` |
| ASGI middleware | `groundcover.asgi.GroundcoverASGIMiddleware` |
| Prometheus metrics bridge | `groundcover.prometheus.Collector` (`uv add "groundcover[prometheus]"`) |

## 2. Initialize once, at the top of `main`

Add `init()` as early as possible at startup, and `close()` on shutdown so
pending events are flushed.

```python
import os

import groundcover

def main() -> None:
    groundcover.init(
        dsn=os.environ.get("GC_DSN", ""),                     # base ingestion origin; the SDK appends the path
        ingestion_key=os.environ.get("GC_INGESTION_KEY", ""), # optional; omit when using a local sensor
        # service_name/env/release are auto-detected from the environment
        # (OTEL_SERVICE_NAME/GC_SERVICE_NAME, GC_ENV/DEPLOYMENT_ENVIRONMENT, GC_RELEASE)
        # and from the k8s Downward API. Set them explicitly only to override.
        # In Kubernetes you can usually omit service_name — the groundcover
        # sensor enriches pod -> workload server-side.
    )
    try:
        run_app()
    finally:
        groundcover.close(timeout=5.0)  # bounded flush on shutdown
```

Rules:

- Call `init()` **exactly once**. Never call it per-request or per-thread.
- `dsn` is **required** unless `disabled=True` (`init()` raises
  `MissingDSNError` otherwise). If you cannot determine it, set
  `disabled=True` (a true no-op, ~zero overhead) rather than guessing.
- For tests / on-prem builds, `groundcover.init(disabled=True)` is the switch.

## 3. Capture errors at boundaries (do not change control flow)

Capture where an error is first observed and is meaningful — typically the
outermost place that handles it. Then keep handling it as before.

```python
try:
    charge(order_id)
except ChargeError as exc:
    groundcover.capture_error(exc)
    raise  # unchanged control flow
```

Attach per-call context with keyword options:

```python
groundcover.capture_error(
    exc,
    attributes={
        "order_id": order_id,  # string
        "amount": 42.5,        # number
        "is_retry": True,      # bool
    },
)
```

Available options: `attributes`, `user`, `level`, `fingerprint` (overrides the
opaque grouping key), `title` (overrides the human-readable display label; by
default it's derived as `error_type: message`).

Do **not**:

- capture the same error at every layer of the stack — you'll create duplicates.
  Capture once, at the boundary.
- build error strings just to capture them; pass the exception object so the SDK
  can extract the type, follow `raise ... from ...` chains, and group correctly.

## 4. Attach identity and request scope via contextvars

`set_user` and `with_scope` attach request-scoped data to the current context.
Every `capture_error` in that context then includes it automatically.

```python
groundcover.set_user(groundcover.User(
    id=user.id,
    email=user.email,
    organization=user.tenant_id,  # B2B group key
))

groundcover.with_scope(lambda s: (
    s.set_attribute("feature", "checkout"),
    s.set_session_id(session_id),
))
```

## 5. Capture uncaught exceptions

### In a thread or task you own

```python
def worker() -> None:
    with groundcover.recover():  # captures an escaping exception, then re-raises it
        do_risky_work()
```

`recover()` re-raises by default (it observes, it does not swallow). If you own
the response lifecycle and do **not** want re-raise, use `capture_recovered`:

```python
try:
    handle()
except Exception as exc:
    groundcover.capture_recovered(exc)
    # ... write a 500, etc. ...
```

Note: `recover()` captures `Exception` subclasses only; `KeyboardInterrupt` and
`SystemExit` propagate untouched.

### Behind HTTP middleware (preferred for servers)

WSGI (Flask, Django with WSGI, plain wsgiref):

```python
from groundcover.wsgi import GroundcoverMiddleware

app.wsgi_app = GroundcoverMiddleware(app.wsgi_app)  # Flask
# or: application = GroundcoverMiddleware(application)
```

ASGI (FastAPI, Starlette, Django with ASGI):

```python
from groundcover.asgi import GroundcoverASGIMiddleware

app = GroundcoverASGIMiddleware(app)
```

The middleware seeds a fresh, isolated scope into each request's context, so
handler code can call `set_user`/`with_scope` and the captured error sees it —
and nothing leaks across requests. It re-raises after capturing, so the host
framework's own error handling (500 pages, debug mode) is unchanged.

### Middleware composition (order matters)

- Register framework error handlers (which typically catch exceptions and
  render a 500) **inside** the groundcover middleware, i.e. wrap the outermost
  app object. If a framework handler swallows the exception before it reaches
  the middleware, capture it there explicitly with `capture_recovered`.
- **Don't double-wrap:** wrapping both the WSGI and ASGI layers of the same
  app captures the same exception twice. Pick one middleware per server.

## 6. Non-error notices

```python
groundcover.capture_message("falling back to stale cache", groundcover.Level.WARNING)
```

Levels: `Level.DEBUG`, `Level.INFO`, `Level.WARNING`, `Level.ERROR`,
`Level.FATAL` (plain strings like `"warning"` are accepted too).

## 7. Scrub PII / secrets (when handling sensitive data)

`before_send` is the single chokepoint. Return `None` to drop an event; mutate
and return it to scrub. It is exception-sandboxed.

```python
def scrub(event: groundcover.Event) -> groundcover.Event | None:
    event.error_message = redact_secrets(event.error_message)
    event.attributes.pop("authorization", None)
    return event

groundcover.init(
    dsn=dsn,
    before_send=scrub,
    hasher=groundcover.HMACHasher(os.environb.get(b"GC_PII_KEY", b"")),  # pseudonymize user.id/email
)
```

## 8. Short-lived jobs / serverless

There is no background time to flush, so flush explicitly before exit:

```python
groundcover.flush(timeout=2.0)
```

`flush(timeout=...)`/`close(timeout=...)` return `False` when the bound expired
before delivery finished.

## 9. Local debugging — see captured events

Set `debug=True` to print each captured event to stderr in a compact, readable
form. It runs *after* scrubbing/hashing, so it honors `before_send` and
`hasher`, and it does not affect delivery.

```python
groundcover.init(dsn=dsn, debug=True)
# [groundcover] error exception  ConnectionError: connection refused
#   fingerprint=836b… handled=true
#   user: id=u-123 org=acme
#   attrs: amount=42.5 order_id=o-9
#   stack:
#     app.checkout.charge (/app/checkout.py:42)
```

## 10. Testing your instrumentation

`before_send` is the blessed in-process test seam: it receives the finalized
`Event`, so you can assert on captures without any network. Record and drop:

```python
captured: list[groundcover.Event] = []
groundcover.init(
    dsn="https://example.invalid",
    before_send=lambda e: (captured.append(e), None)[1],
)
# ... exercise code, then assert on captured ...
```

For wire-level assertions, construct an explicit `Client` with the `_sender`
test seam and inspect the JSON bodies it records.

## PII surface (know what leaves the process)

The SDK does not block PII by default (matching Sentry). What it does:

- **`hasher`** pseudonymizes **only `user.id` and `user.email`**. It does **not**
  touch `user.name`, `user.organization`, custom attributes, or error messages.
- **`before_send`** is the one place to scrub everything else — `error_message`,
  `attributes`, the stacktrace, and any identity field. Return `None` to drop.
- Raw client IPs are never sent (geo/IP is derived server-side).
- SDK-internal logs record the **type** of a contained internal fault, not its
  value, to avoid leaking data.

If your errors or attributes may carry PII, write a `before_send` scrubber.

---

## Decision checklist for an agent

1. Is there a `main`/entry point? → add `init()` + `close()` there. If multiple
   entry points, instrument each.
2. Is it an HTTP server (WSGI or ASGI)? → add the matching middleware; that
   covers uncaught exceptions and request scope for free.
3. For each place that currently logs or swallows an error that matters
   (handlers, background workers, scheduled jobs), add a single
   `groundcover.capture_error(exc, …)` at the boundary.
4. Is there auth/user context? → `set_user` once per request (or in middleware).
5. Does the code handle PII/secrets? → add a `before_send` scrubber and/or
   `hasher`.
6. Threads/tasks spawned by the app? → `with groundcover.recover():` at the top
   of each.

## Hard rules (do not violate)

- Never call `init()` more than once; never `close()` mid-request.
- Never let instrumentation change behavior: after capturing, re-raise/propagate
  the error exactly as before.
- Never pass secrets in `dsn`/attributes; use env vars and `before_send`.
- Prefer passing the exception object (not a formatted string) to
  `capture_error`.
- `capture_error`, `capture_message`, `recover` never block and never raise — do
  not wrap them in your own try/except or timeout logic.

## Verifying the instrumentation

- Imports resolve and `uv run ruff check` is clean.
- Capture sites pass the original exception object.
- `init()` is reachable on startup; `close()`/`flush()` runs on shutdown.
- For a local check, set `debug=True` and confirm captured events print to
  stderr.
