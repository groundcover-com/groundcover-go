"""Prometheus bridge tests (skipped when prometheus-client is missing)."""

from __future__ import annotations

import pytest

prometheus_client = pytest.importorskip("prometheus_client")

from groundcover import Stats  # noqa: E402
from groundcover.prometheus import Collector  # noqa: E402


class _FakeSource:
    def stats(self) -> Stats:
        return Stats(
            captured=10,
            sent=8,
            dropped_overflow=1,
            dropped_send_exhausted=1,
            dropped_before_send=2,
            retries=3,
            rate_limited=1,
            panics_recovered=0,
            queue_pending_items=5,
            queue_pending_bytes=500,
        )


def test_collector_exposes_all_metrics():
    families = {m.name: m for m in Collector(_FakeSource()).collect()}

    assert families["groundcover_sdk_captured_total"].samples[0].value == 10.0
    assert families["groundcover_sdk_sent_total"].samples[0].value == 8.0

    dropped = {
        s.labels["reason"]: s.value for s in families["groundcover_sdk_dropped_total"].samples
    }
    assert dropped == {"overflow": 1.0, "send_exhausted": 1.0, "before_send": 2.0}

    queue = {s.labels["unit"]: s.value for s in families["groundcover_sdk_queue_pending"].samples}
    assert queue == {"items": 5.0, "bytes": 500.0}


def test_collector_registers_on_registry():
    registry = prometheus_client.CollectorRegistry()
    registry.register(Collector(_FakeSource()))
    assert registry.get_sample_value("groundcover_sdk_captured_total") == 10.0
