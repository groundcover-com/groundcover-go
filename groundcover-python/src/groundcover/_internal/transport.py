"""All network I/O for the SDK: a single HTTP sender that POSTs gzipped JSON
batches, and a single background worker thread that batches, retries, and
flushes from the bounded buffer. It is the sole network owner; no other part
of the SDK performs I/O."""

from __future__ import annotations

import concurrent.futures
import dataclasses
import datetime
import email.utils
import gzip
import random
import threading
import time
import urllib.error
import urllib.request
from typing import Any, Callable, List, Optional, Protocol

from .._level import Level
from . import safeguard
from .logthrottle import Throttler
from .ringbuf import RingBuffer

MAX_ERROR_BODY_BYTES = 4 * 1024
"""Bounds how much of an error response body we read for diagnostics,
preventing a hostile or buggy server from forcing a large read."""


class SendError(Exception):
    """Categorizes a delivery failure so the worker can apply the right retry
    policy."""

    def __init__(
        self,
        message: str = "",
        *,
        status_code: int = 0,
        retryable: bool = False,
        rate_limited: bool = False,
        retry_after: float = 0.0,
        cause: Optional[BaseException] = None,
    ) -> None:
        self.status_code = status_code
        """The HTTP status, or 0 for a transport-level error."""
        self.retryable = retryable
        """True for transient failures (network errors, 5xx)."""
        self.rate_limited = rate_limited
        """True for HTTP 429."""
        self.retry_after = retry_after
        """The server-requested backoff in seconds parsed from the
        Retry-After header, if any."""
        self.cause = cause
        """The underlying cause, if any."""
        super().__init__(self._render(message))

    def _render(self, message: str) -> str:
        if self.cause is not None and self.status_code:
            return f"transport: status {self.status_code}: {message or self.cause}"
        if self.status_code:
            return f"transport: status {self.status_code}" + (f": {message}" if message else "")
        if message:
            return f"transport: {message}"
        if self.cause is not None:
            return f"transport: {self.cause}"
        return "transport: unknown error"


class Sender(Protocol):
    """Delivers an encoded (uncompressed JSON) batch body to the backend."""

    def send(self, body: bytes) -> None:
        """Deliver body. Returning means success. Failures are raised as
        SendError so the worker can decide whether and how to retry."""
        ...


class HTTPSender:
    """The default Sender. It gzips the body and POSTs it as JSON using only
    the standard library (urllib)."""

    def __init__(
        self,
        endpoint: str,
        ingestion_key: str = "",
        user_agent: str = "",
        timeout: float = 30.0,
    ) -> None:
        """Create an HTTPSender POSTing to the fully-qualified endpoint (the
        SDK owns the path, e.g. ``<DSN>/json/rum``). ingestion_key, when
        non-empty, is sent as ``Authorization: Bearer <key>``."""
        self._endpoint = endpoint
        self._ingestion_key = ingestion_key
        self._user_agent = user_agent
        self._timeout = timeout if timeout > 0 else 30.0

    def send(self, body: bytes) -> None:
        """Gzip and POST body to the configured endpoint, classifying the
        result."""
        try:
            gzipped = gzip.compress(body)
        except Exception as exc:
            raise SendError("gzip", retryable=False, cause=exc) from exc

        headers = {
            "Content-Type": "application/json",
            "Content-Encoding": "gzip",
        }
        if self._user_agent:
            headers["User-Agent"] = self._user_agent
        if self._ingestion_key:
            headers["Authorization"] = "Bearer " + self._ingestion_key

        # The endpoint is SDK-owned configuration, not attacker-controlled input.
        request = urllib.request.Request(
            self._endpoint, data=gzipped, headers=headers, method="POST"
        )
        try:
            with urllib.request.urlopen(request, timeout=self._timeout) as resp:
                # urlopen only returns for success statuses; drain a bounded
                # amount so the connection can be reused.
                resp.read(MAX_ERROR_BODY_BYTES)
        except urllib.error.HTTPError as exc:
            try:
                exc.read(MAX_ERROR_BODY_BYTES)
            except Exception:
                pass
            retry_after = parse_retry_after(exc.headers.get("Retry-After", "") or "")
            exc.close()
            raise classify_status(exc.code, retry_after) from exc
        except (urllib.error.URLError, OSError, TimeoutError) as exc:
            # Network/transport errors are transient.
            raise SendError("network error", retryable=True, cause=exc) from exc


def classify_status(status_code: int, retry_after: float = 0.0) -> SendError:
    """Map a non-success HTTP status to a SendError."""
    if status_code == 429:
        return SendError(
            "rate limited",
            status_code=status_code,
            rate_limited=True,
            retryable=True,
            retry_after=retry_after,
        )
    if status_code >= 500:
        return SendError("server error", status_code=status_code, retryable=True)
    # 4xx (other than 429): permanent, do not retry.
    return SendError("client error", status_code=status_code, retryable=False)


def parse_retry_after(value: str) -> float:
    """Parse a Retry-After header value in delta-seconds or HTTP date form.
    Return zero when the value is missing or unparseable."""
    if not value:
        return 0.0
    try:
        secs = int(value)
        return float(secs) if secs >= 0 else 0.0
    except ValueError:
        pass
    try:
        when = email.utils.parsedate_to_datetime(value)
    except (TypeError, ValueError):
        return 0.0
    if when is None:
        return 0.0
    if when.tzinfo is None:
        when = when.replace(tzinfo=datetime.timezone.utc)
    delta = (when - datetime.datetime.now(datetime.timezone.utc)).total_seconds()
    return delta if delta > 0 else 0.0


Encoder = Callable[[List[Any]], bytes]
"""Turns a batch of items into an uncompressed JSON request body."""


class Observer(Protocol):
    """Receives delivery outcomes for self-metrics. All methods must be
    non-blocking and exception-free."""

    def on_sent(self, n: int) -> None: ...

    def on_retry(self) -> None: ...

    def on_rate_limited(self) -> None: ...

    def on_send_exhausted(self, n: int) -> None: ...

    def on_queue(self, items: int, size_bytes: int) -> None: ...

    def on_subsystem_disabled(self) -> None: ...


@dataclasses.dataclass
class WorkerConfig:
    """Configures a Worker. Zero fields fall back to defaults. Durations are
    seconds."""

    batch_size: int = 0
    """The maximum number of items per request."""
    max_batch_bytes: int = 0
    """The maximum estimated size per request."""
    flush_interval: float = 0.0
    """The periodic flush cadence."""
    max_retries: int = -1
    """The maximum number of retry attempts after the first try."""
    base_backoff: float = 0.0
    """The initial backoff used for exponential backoff."""
    retry_max: float = 0.0
    """Caps the exponential backoff."""
    rate_limit_backoff: float = 0.0
    """The minimum backoff applied to a 429 response."""
    max_concurrent: int = 0
    """Caps concurrent outbound requests."""
    sleep: Optional[Callable[[float], None]] = None
    """Overrides the cancelable retry sleep (tests)."""

    def with_defaults(self) -> WorkerConfig:
        """Return a copy with zero fields replaced by defaults."""
        c = dataclasses.replace(self)
        if c.batch_size <= 0:
            c.batch_size = 250
        if c.max_batch_bytes <= 0:
            c.max_batch_bytes = 512 * 1024
        if c.flush_interval <= 0:
            c.flush_interval = 5.0
        if c.max_retries < 0:
            c.max_retries = 3
        if c.base_backoff <= 0:
            c.base_backoff = 0.2
        if c.retry_max <= 0:
            c.retry_max = 30.0
        if c.rate_limit_backoff <= 0:
            c.rate_limit_backoff = 30.0
        if c.max_concurrent <= 0:
            c.max_concurrent = 4
        return c


class Worker:
    """Batches items from a bounded buffer and delivers them via a Sender. A
    single background thread owns the loop; sends may run concurrently up to a
    configured limit. It is the sole network owner."""

    def __init__(
        self,
        ring: RingBuffer,
        sender: Sender,
        encode: Encoder,
        observer: Optional[Observer],
        log: Optional[Throttler],
        on_panic: Optional[safeguard.Handler],
        cfg: WorkerConfig,
    ) -> None:
        """Construct a Worker. ring, sender and encode are required; observer
        and log may be None."""
        self._ring = ring
        self._sender = sender
        self._encode = encode
        self._obs = observer
        self._log = log
        self._on_panic = on_panic
        self._cfg = cfg.with_defaults()

        self._trigger = threading.Event()
        self._stop = threading.Event()
        self._cancel = threading.Event()

        self._sem = threading.Semaphore(self._cfg.max_concurrent)
        self._inflight = 0
        self._inflight_cond = threading.Condition()
        self._executor = concurrent.futures.ThreadPoolExecutor(
            max_workers=self._cfg.max_concurrent, thread_name_prefix="groundcover-send"
        )

        self._rng = random.Random()
        self._thread: Optional[threading.Thread] = None
        self._lifecycle_lock = threading.Lock()
        self._started = False
        self._closed = False

    def start(self) -> None:
        """Launch the worker loop. Safe to call once; subsequent calls are
        no-ops."""
        with self._lifecycle_lock:
            if self._started:
                return
            self._started = True
        self._thread = threading.Thread(
            target=self._loop_guarded, daemon=True, name="groundcover-worker"
        )
        self._thread.start()

    def notify(self) -> None:
        """Hint that items are pending so the worker can flush before the next
        interval tick. It never blocks."""
        self._trigger.set()

    def _loop_guarded(self) -> None:
        try:
            self._loop()
        except Exception as exc:
            # A failing loop self-disables rather than crash-looping.
            # flush/close still drain the ring caller-side, so pending events
            # are not lost.
            if self._obs is not None:
                safeguard.do(self._obs.on_subsystem_disabled, None)
            if self._on_panic is not None:
                safeguard._report(self._on_panic, exc)

    def _loop(self) -> None:
        while True:
            self._trigger.wait(self._cfg.flush_interval)
            self._trigger.clear()
            if self._stop.is_set():
                self._dispatch_ready()  # drain during shutdown
                return
            self._dispatch_ready()
            self._report_queue()

    def _dispatch_ready(self) -> None:
        """Pop and dispatch all currently-buffered batches."""
        while True:
            batch = self._ring.pop_batch(self._cfg.batch_size, self._cfg.max_batch_bytes)
            if not batch:
                return
            self._dispatch(batch)

    def _dispatch(self, batch: List[Any]) -> None:
        """Send a batch, bounded by the concurrency semaphore. If the
        semaphore is full it sends synchronously to apply natural backpressure
        on the loop (never on the caller of capture)."""
        if not self._sem.acquire(blocking=False):
            self._send_with_retry(batch)
            return
        with self._inflight_cond:
            self._inflight += 1
        try:
            self._executor.submit(self._send_async, batch)
        except RuntimeError:
            # Executor already shut down: send inline instead.
            self._finish_inflight()
            self._sem.release()
            self._send_with_retry(batch)

    def _send_async(self, batch: List[Any]) -> None:
        try:
            self._send_with_retry(batch)
        finally:
            self._sem.release()
            self._finish_inflight()

    def _finish_inflight(self) -> None:
        with self._inflight_cond:
            self._inflight -= 1
            self._inflight_cond.notify_all()

    def _send_with_retry(self, batch: List[Any]) -> None:
        """Deliver a batch, guarded so an unexpected exception in encoding or
        in the sender is contained, counted as an exhausted drop, and never
        propagates to the worker loop (where it would self-disable the whole
        subsystem over one batch)."""
        if not safeguard.do(lambda: self._deliver_batch(batch), self._on_panic):
            self._observe_exhausted(len(batch))

    def _deliver_batch(self, batch: List[Any]) -> None:
        """Encode and deliver a batch, applying the retry/backoff policy."""
        try:
            body = self._encode(batch)
        except Exception:
            self._logf(Level.ERROR, "encode failed, dropping batch")
            self._observe_exhausted(len(batch))
            return

        attempt = 0
        while True:
            try:
                self._sender.send(body)
            except SendError as send_err:
                backoff, give_up = self._classify_retry(send_err, attempt)
                if give_up:
                    self._observe_exhausted(len(batch))
                    return
                if self._obs is not None:
                    self._obs.on_retry()
                self._sleep(backoff)
                if self._cancel.is_set():
                    # Shutting down: drop rather than hang.
                    self._observe_exhausted(len(batch))
                    return
                attempt += 1
                continue
            if self._obs is not None:
                self._obs.on_sent(len(batch))
            return

    def _classify_retry(self, err: SendError, attempt: int) -> tuple[float, bool]:
        """Decide the backoff for the next attempt and whether to give up."""
        if err.rate_limited:
            if self._obs is not None:
                self._obs.on_rate_limited()
            if attempt >= self._cfg.max_retries:
                return 0.0, True
            backoff = self._cfg.rate_limit_backoff
            if err.retry_after > backoff:
                backoff = err.retry_after
            return backoff, False
        if not err.retryable:
            return 0.0, True  # permanent error
        if attempt >= self._cfg.max_retries:
            return 0.0, True
        return self._exp_backoff(attempt), False

    def _exp_backoff(self, attempt: int) -> float:
        """Compute a full-jittered exponential backoff capped at retry_max."""
        d = self._cfg.base_backoff
        for _ in range(attempt):
            d *= 2
            if d >= self._cfg.retry_max:
                d = self._cfg.retry_max
                break
        # Full jitter: pseudo-random in [0, d]. Not security-sensitive.
        return self._rng.uniform(0.0, d)

    def _sleep(self, seconds: float) -> None:
        if self._cfg.sleep is not None:
            self._cfg.sleep(seconds)
            return
        if seconds > 0:
            self._cancel.wait(seconds)

    def flush(self, timeout: Optional[float] = None) -> bool:
        """Drain the buffer and wait for in-flight sends, bounded by timeout
        (seconds; None blocks until done). It drains even if the worker loop
        has self-disabled — a caller-driven dispatch — so pending events are
        never silently left behind on a "successful" flush. Return True when
        everything completed within the bound."""
        self._dispatch_ready()
        # report_queue is guarded here because flush runs on the caller's
        # thread; a misbehaving Observer must not raise into the caller.
        safeguard.do(self._report_queue, self._on_panic)
        return self._wait_inflight(timeout)

    def close(self, timeout: Optional[float] = None) -> bool:
        """Stop the worker, drain remaining items, and wait for in-flight
        sends, all bounded by timeout. It is idempotent. The drain is
        caller-driven so it also covers the case where the loop already exited
        (e.g. after self-disable). Return True when shutdown completed within
        the bound."""
        with self._lifecycle_lock:
            if self._closed:
                return True
            self._closed = True

        deadline = None if timeout is None else time.monotonic() + timeout
        self._stop.set()
        self._trigger.set()

        ok = True
        if self._thread is not None:
            self._thread.join(_remaining(deadline))
            ok = ok and not self._thread.is_alive()
        self._dispatch_ready()  # drain anything the loop did not (e.g. after self-disable)
        ok = self._wait_inflight(_remaining(deadline)) and ok
        self._cancel.set()  # abort any lingering retry sleeps
        self._executor.shutdown(wait=False)
        return ok

    def _wait_inflight(self, timeout: Optional[float]) -> bool:
        """Block until all in-flight sends complete or timeout expires."""
        with self._inflight_cond:
            return self._inflight_cond.wait_for(lambda: self._inflight == 0, timeout)

    def _report_queue(self) -> None:
        if self._obs is not None:
            self._obs.on_queue(len(self._ring), self._ring.pending_bytes())

    def _observe_exhausted(self, n: int) -> None:
        if self._obs is not None:
            self._obs.on_send_exhausted(n)

    def _logf(self, level: Level, msg: str) -> None:
        if self._log is not None:
            self._log.log(level, msg)


def _remaining(deadline: Optional[float]) -> Optional[float]:
    if deadline is None:
        return None
    return max(0.0, deadline - time.monotonic())
