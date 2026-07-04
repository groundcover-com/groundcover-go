"""Tests for the internal subsystems: ring buffer, safeguard, log throttling,
and self-metrics."""

from __future__ import annotations

from groundcover import Level
from groundcover._internal import safeguard
from groundcover._internal.logthrottle import Throttler
from groundcover._internal.ringbuf import RingBuffer
from groundcover._internal.selfmetrics import DropReason, Metrics

# ------------------------------------------------------------------ ringbuf


def test_ringbuf_fifo_and_len():
    b = RingBuffer(10, 0)
    for i in range(5):
        assert b.push(i) == 0
    assert len(b) == 5
    assert b.pop_batch(3, 0) == [0, 1, 2]
    assert b.pop_batch(0, 0) == [3, 4]
    assert len(b) == 0


def test_ringbuf_drop_oldest_on_item_overflow():
    b = RingBuffer(3, 0)
    dropped = sum(b.push(i) for i in range(10))
    assert dropped == 7
    assert b.drain_all() == [7, 8, 9], "newest wins"


def test_ringbuf_byte_budget_eviction():
    b = RingBuffer(100, 10, size_of=lambda _i: 4)
    dropped = 0
    for i in range(4):
        dropped += b.push(i)
    # 4 items * 4 bytes = 16 > 10: the oldest are evicted down to <= 10 bytes.
    assert dropped > 0
    assert b.pending_bytes() <= 10 or len(b) == 1


def test_ringbuf_batch_respects_byte_limit():
    b = RingBuffer(100, 0, size_of=lambda _i: 10)
    for i in range(5):
        b.push(i)
    batch = b.pop_batch(10, 25)
    assert batch == [0, 1], "stops before exceeding the byte budget"
    # At least one entry is always returned when non-empty.
    assert b.pop_batch(10, 1) == [2]


def test_ringbuf_always_accepts():
    b = RingBuffer(1, 1, size_of=lambda _i: 100)
    b.push("a")
    dropped = b.push("b")
    assert dropped == 1
    assert b.drain_all() == ["b"]


# ---------------------------------------------------------------- safeguard


def test_safeguard_do_contains_and_reports():
    infos = []
    ok = safeguard.do(lambda: (_ for _ in ()).throw(RuntimeError("x")), infos.append)
    assert ok is False
    assert len(infos) == 1
    assert isinstance(infos[0].value, RuntimeError)
    assert infos[0].stack

    assert safeguard.do(lambda: None, infos.append) is True
    assert len(infos) == 1


def test_safeguard_contains_raising_handler():
    def bad_handler(_info):
        raise RuntimeError("handler broke")

    ok = safeguard.do(lambda: (_ for _ in ()).throw(ValueError("x")), bad_handler)
    assert ok is False  # secondary exception is swallowed


def test_safeguard_go_runs_and_contains():
    import threading

    done = threading.Event()

    def work():
        done.set()
        raise RuntimeError("in thread")

    t = safeguard.go(work, None)
    t.join(5.0)
    assert done.is_set()
    assert not t.is_alive()


# -------------------------------------------------------------- logthrottle


def test_logthrottle_per_key_window_and_suppressed_count():
    lines = []
    now = [0.0]
    t = Throttler(
        sink=lambda level, msg, suppressed: lines.append((level, msg, suppressed)),
        window=5.0,
        global_window=1.0,
        global_cap=0,
        now=lambda: now[0],
    )

    def emit():
        t.log(Level.ERROR, "boom")  # same call site every time

    emit()
    emit()
    emit()
    assert len(lines) == 1, "repeats within the window are suppressed"

    now[0] = 6.0
    emit()
    assert len(lines) == 2
    assert lines[1][2] == 2, "the suppressed count is reported on re-emit"


def test_logthrottle_global_cap():
    lines = []
    now = [0.0]
    t = Throttler(
        sink=lambda level, msg, suppressed: lines.append(msg),
        window=0.001,
        global_window=10.0,
        global_cap=2,
        now=lambda: now[0],
    )
    for i in range(5):
        now[0] += 0.01  # step past the per-key window each time
        t.log(Level.INFO, f"m{i}")
    assert len(lines) == 2, "the global cap bounds output across call sites"


def test_logthrottle_contains_raising_sink():
    def bad_sink(_level, _msg, _suppressed):
        raise RuntimeError("sink broke")

    t = Throttler(sink=bad_sink)
    t.log(Level.ERROR, "x")  # must not raise


# -------------------------------------------------------------- selfmetrics


def test_selfmetrics_counters_and_snapshot():
    m = Metrics()
    m.inc_captured()
    m.add_sent(3)
    m.add_dropped(DropReason.OVERFLOW, 2)
    m.add_dropped(DropReason.SEND_EXHAUSTED, 1)
    m.add_dropped(DropReason.BEFORE_SEND, 4)
    m.inc_retries()
    m.inc_rate_limited()
    m.inc_panics_recovered()
    m.inc_config_reloads()
    m.inc_subsystems_disabled()
    m.set_queue_pending(7, 700)

    s = m.snapshot()
    assert s.captured == 1
    assert s.sent == 3
    assert s.dropped_overflow == 2
    assert s.dropped_send == 1
    assert s.dropped_before_send == 4
    assert s.retries == 1
    assert s.rate_limited == 1
    assert s.panics_recovered == 1
    assert s.config_reloads == 1
    assert s.subsystems_disabled == 1
    assert s.queue_pending_items == 7
    assert s.queue_pending_bytes == 700

    m.reset()
    assert m.snapshot() == type(s)()
