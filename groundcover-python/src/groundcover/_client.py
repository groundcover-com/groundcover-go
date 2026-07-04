"""The explicit SDK client."""

from __future__ import annotations

import sys
import time
from typing import Any, Callable, ContextManager, Optional, TextIO, Union

from ._attributes import sanitize_attributes
from ._config import Config
from ._debug import render_debug
from ._errors import error_type
from ._event import EVENT_TYPE, Attributes, Event, Service
from ._fingerprint import fingerprint as _compute_fingerprint
from ._internal import safeguard
from ._internal.logthrottle import Throttler
from ._internal.ringbuf import RingBuffer
from ._internal.selfmetrics import DropReason, Metrics
from ._internal.transport import HTTPSender, Sender, Worker, WorkerConfig
from ._level import Level, coerce_level
from ._logger import resolve_logger
from ._resource import Resource, detect_resource
from ._scope import Scope, current_scope, ensure_scope, isolated_scope
from ._stacktrace import capture_stack, frames_from_traceback
from ._stats import Stats, stats_from_snapshot
from ._title import MESSAGE_ERROR_TYPE, title_for
from ._user import User
from ._uuid import new_uuid
from ._wire import encode_batch, estimate_size, user_agent

PANIC_FLUSH_TIMEOUT = 2.0
"""Bounds the best-effort flush performed by recover() before re-raising an
uncaught exception."""


class Client:
    """An explicit SDK client. Most callers use the module-level API
    (init + capture_error); constructing a Client directly is for tests and
    multi-config setups.

    A disabled config yields a no-op client. An enabled config without a DSN
    raises MissingDSNError.
    """

    def __init__(self, config: Optional[Config] = None, *, _sender: Optional[Sender] = None):
        """Construct a Client from config (``_sender`` is a test seam that
        replaces the HTTP transport)."""
        cfg = config if config is not None else Config()
        cfg.validate()
        resolved = cfg.with_defaults()

        self._metrics = Metrics()
        self._config = resolved
        self._disabled = resolved.disabled
        self._debug = False
        self._debug_out: Optional[TextIO] = None
        self._resource = Resource()
        self._throttle: Optional[Throttler] = None
        self._ring: Optional[RingBuffer] = None
        self._worker: Optional[Worker] = None
        if self._disabled:
            return

        self._resource = detect_resource(resolved)
        logger = resolve_logger(resolved.logger)
        self._throttle = Throttler(sink=logger.log, window=5.0, global_window=1.0, global_cap=20)
        self._debug = resolved.debug
        self._debug_out = sys.stderr

        self._ring = RingBuffer(resolved.max_queue, resolved.max_bytes, estimate_size)

        if _sender is None:
            _sender = HTTPSender(
                endpoint=resolved.endpoint(),
                ingestion_key=resolved.ingestion_key,
                user_agent=user_agent(),
                timeout=resolved.http_timeout,
            )

        resource = self._resource
        self._worker = Worker(
            ring=self._ring,
            sender=_sender,
            encode=lambda items: encode_batch(items, resource),
            observer=_MetricsObserver(self._metrics),
            log=self._throttle,
            on_panic=self._on_panic,
            cfg=WorkerConfig(
                batch_size=resolved.batch_size,
                max_batch_bytes=resolved.max_batch_bytes,
                flush_interval=resolved.flush_interval,
                max_retries=resolved.max_retries,
                retry_max=resolved.retry_max,
                rate_limit_backoff=resolved.rate_limit_backoff,
            ),
        )
        self._worker.start()

    @property
    def disabled(self) -> bool:
        """Report whether this client is a no-op."""
        return self._disabled

    def _on_panic(self, info: safeguard.PanicInfo) -> None:
        self._metrics.inc_panics_recovered()
        # Log the fault's type, not its value, to avoid leaking data into
        # SDK-internal logs.
        if self._throttle is not None:
            self._throttle.log(
                Level.ERROR,
                f"contained SDK-internal error ({type(info.value).__name__})",
            )

    # ------------------------------------------------------------- capture

    def capture_error(
        self,
        error: Optional[BaseException],
        *,
        attributes: Optional[Attributes] = None,
        user: Optional[User] = None,
        level: Union[Level, str, None] = None,
        fingerprint: Optional[str] = None,
        title: Optional[str] = None,
    ) -> None:
        """Capture error (handled by default) and enqueue it for delivery. It
        never blocks on I/O and never affects control flow.

        Per-call keyword options take precedence over the request scope, which
        takes precedence over process defaults.
        """
        if self._disabled or error is None:
            return
        safeguard.do(
            lambda: self._finish_and_enqueue(
                self._new_error_event(error),
                attributes=attributes,
                user=user,
                level=level,
                fingerprint=fingerprint,
                title=title,
            ),
            self._on_panic,
        )

    def capture_message(
        self,
        message: str,
        level: Union[Level, str] = Level.INFO,
        *,
        attributes: Optional[Attributes] = None,
        user: Optional[User] = None,
        fingerprint: Optional[str] = None,
        title: Optional[str] = None,
    ) -> None:
        """Capture a non-error notice at the given level. The per-call level
        wins over the request scope (global < scope < per-call)."""
        if self._disabled:
            return

        def _do() -> None:
            e = Event(
                id=new_uuid(),
                timestamp_ns=time.time_ns(),
                type=EVENT_TYPE,
                level=Level.INFO,  # default; the per-call level is applied below
                error_handled=True,
                error_type=MESSAGE_ERROR_TYPE,
                error_message=message,
                service=Service(name=self._resource.service_name, version=self._resource.release),
            )
            self._finish_and_enqueue(
                e,
                attributes=attributes,
                user=user,
                level=level,
                fingerprint=fingerprint,
                title=title,
            )

        safeguard.do(_do, self._on_panic)

    def capture_recovered(
        self,
        recovered: Any,
        *,
        attributes: Optional[Attributes] = None,
        user: Optional[User] = None,
        level: Union[Level, str, None] = None,
        fingerprint: Optional[str] = None,
        title: Optional[str] = None,
    ) -> None:
        """Capture an already-caught exception as an unhandled error without
        re-raising. It is used by middleware that owns the response
        lifecycle."""
        if self._disabled or recovered is None:
            return
        safeguard.do(
            lambda: self._finish_and_enqueue(
                self._new_panic_event(recovered),
                attributes=attributes,
                user=user,
                level=level,
                fingerprint=fingerprint,
                title=title,
            ),
            self._on_panic,
        )

    def recover(self) -> ContextManager[None]:
        """Return a context manager that captures an exception escaping its
        body (as an unhandled, fatal error), performs a short best-effort
        flush, and re-raises. Use it around code you do not want to alter:

            with client.recover():
                do_risky_work()

        Only ``Exception`` subclasses are captured; ``KeyboardInterrupt``,
        ``SystemExit`` and other ``BaseException``s propagate untouched.
        """
        return _RecoverContext(self)

    def _new_error_event(self, error: BaseException) -> Event:
        """Build a handled-error event from an exception. Frames come from the
        exception's traceback when present (the raise site), falling back to
        the current call stack."""
        cfg = self._config
        return Event(
            id=new_uuid(),
            timestamp_ns=time.time_ns(),
            type=EVENT_TYPE,
            level=Level.ERROR,
            error_handled=True,
            error_type=error_type(error),
            error_message=_safe_str(error),
            stacktrace=self._frames_for(error, cfg.stack_depth_max),
            service=Service(name=self._resource.service_name, version=self._resource.release),
        )

    def _new_panic_event(self, recovered: Any) -> Event:
        """Build an unhandled-error event from a caught exception (or an
        arbitrary recovered value)."""
        cfg = self._config
        if isinstance(recovered, BaseException):
            etype = error_type(recovered)
            emsg = _safe_str(recovered)
            frames = self._frames_for(recovered, cfg.stack_depth_max)
        else:
            etype = "panic"
            emsg = _safe_str(recovered)
            frames = capture_stack(cfg.stack_depth_max, self._resource.in_app_root)
        return Event(
            id=new_uuid(),
            timestamp_ns=time.time_ns(),
            type=EVENT_TYPE,
            level=Level.FATAL,
            _level_locked=True,  # an uncaught exception is fatal; scope must not downgrade it
            error_handled=False,
            error_type=etype,
            error_message=emsg,
            stacktrace=frames,
            service=Service(name=self._resource.service_name, version=self._resource.release),
        )

    def _frames_for(self, error: BaseException, max_depth: int) -> list:
        tb = getattr(error, "__traceback__", None)
        if tb is not None:
            return frames_from_traceback(tb, max_depth, self._resource.in_app_root)
        return capture_stack(max_depth, self._resource.in_app_root)

    def _finish_and_enqueue(
        self,
        e: Event,
        *,
        attributes: Optional[Attributes],
        user: Optional[User],
        level: Union[Level, str, None],
        fingerprint: Optional[str] = None,
        title: Optional[str] = None,
    ) -> None:
        """Apply scope and per-call options, pseudonymize identity, compute
        the fingerprint, run before_send, and enqueue the event."""
        cfg = self._config

        scope = current_scope()
        if scope is not None:
            scope.apply_to(e)

        # Per-call options are applied last and therefore win over the scope.
        if attributes:
            e.attributes.update(attributes)
        if user is not None:
            e.user = user.copy()
        lvl = coerce_level(level)
        if lvl is not None:
            e.level = lvl
        if fingerprint:
            e.fingerprint = fingerprint
        if title:
            e.title = title

        if cfg.before_send is not None:
            out = self._run_before_send(cfg.before_send, e)
            if out is None:
                self._metrics.add_dropped(DropReason.BEFORE_SEND, 1)
                return
            e = out

        # Pseudonymize identity as the final step before enqueue so nothing —
        # scope, per-call options, or before_send — can introduce a raw
        # user.id/email onto the wire after the hash would otherwise have run.
        if cfg.hasher is not None:
            e.user.id = cfg.hasher.hash_identity(e.user.id)
            e.user.email = cfg.hasher.hash_identity(e.user.email)

        # Compute the grouping key and display title on the final,
        # post-before_send event so a scrubber that rewrites the
        # message/type/stack is reflected and no pre-scrub data leaks via the
        # title or fingerprint. Explicit overrides (per-call fingerprint/title,
        # or values set by before_send) are preserved.
        if not e.fingerprint:
            e.fingerprint = _compute_fingerprint(e)
        if not e.title:
            e.title = title_for(e)

        # Snapshot the attributes: a deep, JSON-coerced copy so later caller
        # mutation of nested values cannot change the queued event, and so the
        # byte budget is estimated on fully-expanded values.
        e.attributes = sanitize_attributes(e.attributes)

        if self._debug:
            self._write_debug(e)

        self._metrics.inc_captured()
        self._enqueue(e)

    def _run_before_send(self, fn: Callable[[Event], Optional[Event]], e: Event) -> Optional[Event]:
        """Invoke the user callback inside an exception guard. A raised
        exception is treated as "keep the event unmodified"."""
        result: list = [e]
        safeguard.do(lambda: result.__setitem__(0, fn(e)), self._on_panic)
        return result[0]

    def _write_debug(self, e: Event) -> None:
        """Render the finalized event to the debug writer, guarded so it can
        never affect capture."""
        out = self._debug_out
        if out is None:
            return
        safeguard.do(lambda: out.write(render_debug(e)), self._on_panic)

    def _enqueue(self, e: Event) -> None:
        """Perform the bounded, non-blocking hand-off to the pipeline."""
        assert self._ring is not None and self._worker is not None
        dropped = self._ring.push(e)
        if dropped > 0:
            self._metrics.add_dropped(DropReason.OVERFLOW, dropped)
            self._fire_on_drop(self._config.on_drop, dropped)
        self._metrics.set_queue_pending(len(self._ring), self._ring.pending_bytes())
        if len(self._ring) >= self._config.batch_size:
            self._worker.notify()

    def _fire_on_drop(self, fn: Optional[Callable[[int], None]], n: int) -> None:
        if fn is None:
            return
        safeguard.do(lambda: fn(n), self._on_panic)

    # --------------------------------------------------------------- scope

    def set_user(self, user: User) -> Scope:
        """Set the identity on the current request scope (creating and
        attaching one to the current context if none exists) and return the
        scope. Mutations of an existing scope (e.g. one installed by
        middleware) are visible to everything sharing that context."""
        sc = ensure_scope()
        sc.set_user(user)
        return sc

    def with_scope(self, fn: Optional[Callable[[Scope], None]]) -> Scope:
        """Apply fn to the current request scope (creating one if needed) and
        return the scope. As with set_user, an existing scope is mutated in
        place."""
        sc = ensure_scope()
        if fn is not None:
            safeguard.do(lambda: fn(sc), self._on_panic)
        return sc

    def isolated_scope(self) -> ContextManager[Scope]:
        """Return a context manager installing a fresh, isolated copy of the
        current scope. Middleware uses it at the start of each request so
        per-request identity/attributes set by handlers never leak across
        requests."""
        return isolated_scope()

    # ----------------------------------------------------------- lifecycle

    def flush(self, timeout: Optional[float] = None) -> bool:
        """Block until pending events are delivered or timeout (seconds)
        expires. Return True when everything completed within the bound."""
        if self._disabled:
            return True
        assert self._worker is not None
        return self._worker.flush(timeout)

    def close(self, timeout: Optional[float] = None) -> bool:
        """Flush and stop the client. It is idempotent and bounded by timeout
        (seconds). Return True when shutdown completed within the bound."""
        if self._disabled:
            return True
        assert self._worker is not None
        return self._worker.close(timeout)

    def stats(self) -> Stats:
        """Return a snapshot of the SDK's self-observability counters."""
        return stats_from_snapshot(self._metrics.snapshot())


class _RecoverContext:
    """Context manager backing Client.recover()."""

    def __init__(self, client: Client) -> None:
        self._client = client

    def __enter__(self) -> None:
        return None

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> bool:
        if exc is None or not isinstance(exc, Exception):
            return False  # nothing to capture; never swallow
        client = self._client
        if not client.disabled:

            def _do() -> None:
                client.capture_recovered(exc)
                # A short best-effort flush: the exception is about to unwind
                # the caller, which may terminate the process.
                client.flush(PANIC_FLUSH_TIMEOUT)

            safeguard.do(_do, client._on_panic)
        return False  # re-raise: we observe, never alter control flow


class _MetricsObserver:
    """Adapts the worker's Observer to the self-metrics counters."""

    def __init__(self, metrics: Metrics) -> None:
        self._m = metrics

    def on_sent(self, n: int) -> None:
        self._m.add_sent(n)

    def on_retry(self) -> None:
        self._m.inc_retries()

    def on_rate_limited(self) -> None:
        self._m.inc_rate_limited()

    def on_send_exhausted(self, n: int) -> None:
        self._m.add_dropped(DropReason.SEND_EXHAUSTED, n)

    def on_subsystem_disabled(self) -> None:
        self._m.inc_subsystems_disabled()

    def on_queue(self, items: int, size_bytes: int) -> None:
        self._m.set_queue_pending(items, size_bytes)


def _safe_str(value: Any) -> str:
    try:
        return str(value)
    except Exception:
        return f"<unprintable {type(value).__name__}>"
