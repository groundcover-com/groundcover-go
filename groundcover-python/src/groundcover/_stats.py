"""Public self-observability counters."""

from __future__ import annotations

import dataclasses

from ._internal.selfmetrics import Snapshot


@dataclasses.dataclass(frozen=True)
class Stats:
    """A point-in-time snapshot of the SDK's self-observability counters.
    Field names mirror the exported Prometheus metric names."""

    captured: int = 0
    """The number of events accepted into the pipeline."""
    sent: int = 0
    """The number of events successfully delivered."""
    dropped_overflow: int = 0
    """The number of events evicted by buffer overflow."""
    dropped_send_exhausted: int = 0
    """The number of events dropped after delivery failed."""
    dropped_before_send: int = 0
    """The number of events dropped by before_send."""
    retries: int = 0
    """The number of delivery retry attempts."""
    rate_limited: int = 0
    """The number of 429 responses observed."""
    panics_recovered: int = 0
    """The number of contained SDK-internal faults."""
    config_reloads: int = 0
    """The number of configuration swaps."""
    queue_pending_items: int = 0
    """The current number of buffered events."""
    queue_pending_bytes: int = 0
    """The current estimated size of buffered events."""
    subsystems_disabled: int = 0
    """The number of background subsystems self-disabled after a fault."""


def stats_from_snapshot(s: Snapshot) -> Stats:
    """Map an internal counter snapshot to the public Stats type."""
    return Stats(
        captured=s.captured,
        sent=s.sent,
        dropped_overflow=s.dropped_overflow,
        dropped_send_exhausted=s.dropped_send,
        dropped_before_send=s.dropped_before_send,
        retries=s.retries,
        rate_limited=s.rate_limited,
        panics_recovered=s.panics_recovered,
        config_reloads=s.config_reloads,
        queue_pending_items=s.queue_pending_items,
        queue_pending_bytes=s.queue_pending_bytes,
        subsystems_disabled=s.subsystems_disabled,
    )
