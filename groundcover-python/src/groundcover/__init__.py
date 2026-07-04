"""groundcover is the groundcover runtime SDK for Python. Its v1 scope is
error tracking: it captures application errors and uncaught exceptions and
ships them to groundcover without ever affecting the host application.

Safety guarantees
-----------------

- Every public entry point and background task is exception-guarded; internal
  faults are swallowed (recorded as a self-metric and a throttled log).
- Memory is strictly bounded by a buffer with both an item-count and a byte
  budget; on overflow the oldest events are dropped.
- Capturing an error never blocks on I/O: the caller enriches the event and
  performs a single non-blocking hand-off to a background worker thread that
  owns all network traffic.

Usage
-----

The module exposes a Sentry-style global client configured with init(), plus
an explicit Client for tests and multi-config setups::

    import groundcover

    groundcover.init(
        dsn="https://<ingestion-origin>",
        ingestion_key="<key>",
    )

    try:
        do_work()
    except Exception as exc:
        groundcover.capture_error(exc)
        raise  # unchanged control flow

    groundcover.close(timeout=5.0)  # bounded flush on shutdown

Errors are submitted as events; that this happens over the RUM ingestion
endpoint in v1 is an implementation detail that may change without affecting
callers (the SDK owns the path).

Instrumenting an existing service
---------------------------------

A step-by-step playbook (for humans and AI coding agents) lives in
docs/llm-instrumentation-guide.md in the repository. The essentials:

- Call init() exactly once at startup; call close() on shutdown.
- Capture at boundaries with capture_error(exc); keep raising/returning the
  error as before — the SDK observes, it never alters control flow.
- Attach identity/attributes to the request context with set_user /
  with_scope (scope travels on contextvars).
- Wrap WSGI/ASGI servers with groundcover.wsgi / groundcover.asgi middleware
  to capture unhandled exceptions automatically.
- Pass the real exception object (not a formatted string) so the type is
  extracted and grouping works.
- Scrub PII/secrets in before_send; pseudonymize identity with an
  IdentityHasher.
"""

from __future__ import annotations

from ._client import PANIC_FLUSH_TIMEOUT, Client
from ._client_global import (
    capture_error,
    capture_message,
    capture_recovered,
    close,
    current_global,
    flush,
    global_stats,
    init,
    recover,
    set_user,
    with_scope,
)
from ._config import Config, MissingDSNError
from ._event import Attributes, Event, Frame, Service
from ._hasher import HMACHasher, IdentityHasher
from ._level import Level
from ._logger import Logger
from ._scope import Scope, isolated_scope
from ._stats import Stats
from ._user import User
from ._version import VERSION

__version__ = VERSION

__all__ = [
    "PANIC_FLUSH_TIMEOUT",
    "VERSION",
    "Attributes",
    "Client",
    "Config",
    "Event",
    "Frame",
    "HMACHasher",
    "IdentityHasher",
    "Level",
    "Logger",
    "MissingDSNError",
    "Scope",
    "Service",
    "Stats",
    "User",
    "capture_error",
    "capture_message",
    "capture_recovered",
    "close",
    "current_global",
    "flush",
    "global_stats",
    "init",
    "isolated_scope",
    "recover",
    "set_user",
    "with_scope",
]
