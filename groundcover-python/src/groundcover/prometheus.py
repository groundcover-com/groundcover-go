"""Bridges a groundcover Client's self-metrics to Prometheus using
prometheus-client. It is an optional integration (install the ``prometheus``
extra) so the core SDK stays dependency-free; importing this module without
prometheus-client installed raises ImportError."""

from __future__ import annotations

from typing import Iterable, Protocol

try:
    from prometheus_client.core import GaugeMetricFamily
except ImportError as exc:  # pragma: no cover - exercised only without the extra
    raise ImportError(
        "groundcover.prometheus requires prometheus-client; "
        'install the extra: pip install "groundcover[prometheus]" '
        'or: uv add "groundcover[prometheus]"'
    ) from exc

from ._stats import Stats


class StatsSource(Protocol):
    """The subset of Client this collector needs."""

    def stats(self) -> Stats:
        """Return the current self-metrics snapshot."""
        ...


class Collector:
    """Exposes a client's stats() as Prometheus metrics. Counters are reported
    with their cumulative values; gauges reflect the live queue depth.

    Register it on a prometheus-client registry::

        from prometheus_client import REGISTRY
        REGISTRY.register(Collector(client))
    """

    def __init__(self, source: StatsSource) -> None:
        """Build a Collector backed by the given client (or anything with a
        stats() method)."""
        self._source = source

    def collect(self) -> Iterable[GaugeMetricFamily]:
        """Yield the SDK metrics in prometheus-client's collector format."""
        s = self._source.stats()

        def gauge(name: str, doc: str, value: int) -> GaugeMetricFamily:
            g = GaugeMetricFamily(name, doc)
            g.add_metric([], float(value))
            return g

        yield gauge(
            "groundcover_sdk_captured_total", "Events accepted into the pipeline.", s.captured
        )
        yield gauge("groundcover_sdk_sent_total", "Events successfully delivered.", s.sent)

        dropped = GaugeMetricFamily(
            "groundcover_sdk_dropped_total", "Events dropped, by reason.", labels=["reason"]
        )
        dropped.add_metric(["overflow"], float(s.dropped_overflow))
        dropped.add_metric(["send_exhausted"], float(s.dropped_send_exhausted))
        dropped.add_metric(["before_send"], float(s.dropped_before_send))
        yield dropped

        yield gauge("groundcover_sdk_retries_total", "Delivery retry attempts.", s.retries)
        yield gauge("groundcover_sdk_rate_limited_total", "429 responses observed.", s.rate_limited)
        yield gauge(
            "groundcover_sdk_panics_recovered_total",
            "Contained SDK-internal faults.",
            s.panics_recovered,
        )
        yield gauge(
            "groundcover_sdk_config_reloads_total", "Configuration swaps.", s.config_reloads
        )
        yield gauge(
            "groundcover_sdk_subsystems_disabled_total",
            "Background subsystems self-disabled after a fault.",
            s.subsystems_disabled,
        )

        queue = GaugeMetricFamily(
            "groundcover_sdk_queue_pending", "Pending queue depth, by unit.", labels=["unit"]
        )
        queue.add_metric(["items"], float(s.queue_pending_items))
        queue.add_metric(["bytes"], float(s.queue_pending_bytes))
        yield queue
