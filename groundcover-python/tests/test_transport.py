"""Delivery pipeline tests: retry/backoff policy, rate limiting, and flush
semantics, ported from the Go SDK's delivery_test.go / timeout_test.go /
transport tests."""

from __future__ import annotations

import time

from groundcover._internal.ringbuf import RingBuffer
from groundcover._internal.transport import (
    SendError,
    Worker,
    WorkerConfig,
    classify_status,
    parse_retry_after,
)

from .conftest import MockSender


def _worker(sender, ring=None, **cfg_kwargs):
    cfg_kwargs.setdefault("sleep", lambda _s: None)  # skip real backoff sleeps
    cfg_kwargs.setdefault("flush_interval", 3600.0)  # tests drive dispatch via flush
    ring = ring if ring is not None else RingBuffer(100, 0)
    w = Worker(
        ring=ring,
        sender=sender,
        encode=lambda items: repr(items).encode(),
        observer=None,
        log=None,
        on_panic=None,
        cfg=WorkerConfig(**cfg_kwargs),
    )
    return w, ring


class _CountingObserver:
    def __init__(self):
        self.sent = 0
        self.retries = 0
        self.rate_limited = 0
        self.exhausted = 0
        self.disabled = 0

    def on_sent(self, n):
        self.sent += n

    def on_retry(self):
        self.retries += 1

    def on_rate_limited(self):
        self.rate_limited += 1

    def on_send_exhausted(self, n):
        self.exhausted += n

    def on_queue(self, items, size_bytes):
        pass

    def on_subsystem_disabled(self):
        self.disabled += 1


def _observed_worker(sender, **cfg_kwargs):
    cfg_kwargs.setdefault("sleep", lambda _s: None)
    cfg_kwargs.setdefault("flush_interval", 3600.0)
    ring = RingBuffer(100, 0)
    obs = _CountingObserver()
    w = Worker(
        ring=ring,
        sender=sender,
        encode=lambda items: repr(items).encode(),
        observer=obs,
        log=None,
        on_panic=None,
        cfg=WorkerConfig(**cfg_kwargs),
    )
    return w, ring, obs


def test_retry_then_success():
    calls = []

    def responder(call, _body):
        calls.append(call)
        if call < 2:
            raise SendError("server error", status_code=500, retryable=True)

    sender = MockSender(responder)
    w, ring, obs = _observed_worker(sender, max_retries=3)
    ring.push("e")
    assert w.flush(5.0)
    assert sender.calls == 3, "two failures then success"
    assert obs.sent == 1
    assert obs.retries == 2
    w.close(5.0)


def test_retries_exhausted_drops():
    def responder(_call, _body):
        raise SendError("server error", status_code=500, retryable=True)

    sender = MockSender(responder)
    w, ring, obs = _observed_worker(sender, max_retries=2)
    ring.push("e")
    assert w.flush(5.0)
    assert sender.calls == 3, "initial try + 2 retries"
    assert obs.exhausted == 1
    assert obs.sent == 0
    w.close(5.0)


def test_permanent_error_no_retry():
    def responder(_call, _body):
        raise SendError("client error", status_code=400, retryable=False)

    sender = MockSender(responder)
    w, ring, obs = _observed_worker(sender, max_retries=5)
    ring.push("e")
    assert w.flush(5.0)
    assert sender.calls == 1, "4xx must not be retried"
    assert obs.exhausted == 1
    w.close(5.0)


def test_rate_limited_counted_and_retried():
    def responder(call, _body):
        if call == 0:
            raise SendError(
                "rate limited", status_code=429, rate_limited=True, retryable=True, retry_after=0.0
            )

    sender = MockSender(responder)
    w, ring, obs = _observed_worker(sender, max_retries=3)
    ring.push("e")
    assert w.flush(5.0)
    assert obs.rate_limited == 1
    assert obs.sent == 1
    w.close(5.0)


def test_encode_failure_drops_batch():
    sender = MockSender()
    ring = RingBuffer(100, 0)
    obs = _CountingObserver()

    def bad_encode(_items):
        raise ValueError("cannot encode")

    w = Worker(
        ring=ring,
        sender=sender,
        encode=bad_encode,
        observer=obs,
        log=None,
        on_panic=None,
        cfg=WorkerConfig(flush_interval=3600.0, sleep=lambda _s: None),
    )
    ring.push("e")
    assert w.flush(5.0)
    assert sender.calls == 0
    assert obs.exhausted == 1
    w.close(5.0)


def test_hostile_sender_exception_is_contained():
    def responder(_call, _body):
        raise RuntimeError("not a SendError")

    sender = MockSender(responder)
    w, ring, obs = _observed_worker(sender)
    ring.push("e")
    assert w.flush(5.0)
    assert obs.exhausted == 1, "an unexpected sender exception drops the batch"
    w.close(5.0)


def test_flush_timeout_against_slow_sender():
    def responder(_call, _body):
        time.sleep(2.0)

    sender = MockSender(responder)
    w, ring, _obs = _observed_worker(sender)
    w.start()
    ring.push("e")
    w.notify()
    time.sleep(0.1)  # let the worker pick up the batch
    start = time.monotonic()
    ok = w.flush(0.2)
    elapsed = time.monotonic() - start
    assert ok is False, "flush must report timeout"
    assert elapsed < 1.5, "flush must respect its bound"
    w.close(5.0)


def test_close_idempotent_and_drains():
    sender = MockSender()
    w, ring, obs = _observed_worker(sender)
    w.start()
    ring.push("e")
    assert w.close(5.0)
    assert w.close(5.0), "close is idempotent"
    assert obs.sent == 1, "close drains pending events"


def test_worker_batches_by_size():
    sender = MockSender()
    w, ring, obs = _observed_worker(sender, batch_size=2)
    for i in range(5):
        ring.push(i)
    assert w.flush(5.0)
    assert obs.sent == 5
    assert sender.calls == 3, "5 items with batch_size=2 -> 3 requests"
    w.close(5.0)


def test_classify_status():
    e = classify_status(429, retry_after=12.0)
    assert e.rate_limited and e.retryable and e.retry_after == 12.0
    assert classify_status(500).retryable
    assert not classify_status(400).retryable
    assert not classify_status(401).retryable


def test_parse_retry_after():
    assert parse_retry_after("") == 0.0
    assert parse_retry_after("15") == 15.0
    assert parse_retry_after("-3") == 0.0
    assert parse_retry_after("garbage") == 0.0
    # HTTP-date form: a date in the past yields zero.
    assert parse_retry_after("Wed, 21 Oct 2015 07:28:00 GMT") == 0.0


def test_exp_backoff_bounded():
    sender = MockSender()
    w, _ring = _worker(sender, base_backoff=0.2, retry_max=1.0)
    for attempt in range(10):
        b = w._exp_backoff(attempt)
        assert 0.0 <= b <= 1.0
    w.close(5.0)
