"""The SDK's self-observability counters.

Every internal action increments a counter so the SDK can report on its own
behavior through stats() and an optional Prometheus bridge without depending
on any metrics library in the core.
"""

from __future__ import annotations

import dataclasses
import enum
import threading


class DropReason(enum.Enum):
    """Enumerates the reasons an event can be dropped."""

    OVERFLOW = "overflow"
    """An event was evicted because the bounded buffer exceeded its item or
    byte budget."""
    SEND_EXHAUSTED = "send_exhausted"
    """An event was dropped after delivery retries were exhausted or the
    server returned a permanent error."""
    BEFORE_SEND = "before_send"
    """An event was dropped by a before_send callback."""


@dataclasses.dataclass(frozen=True)
class Snapshot:
    """An immutable copy of all counters at a point in time. Field names
    mirror the exported Prometheus metric names."""

    captured: int = 0
    sent: int = 0
    dropped_overflow: int = 0
    dropped_send: int = 0
    dropped_before_send: int = 0
    retries: int = 0
    rate_limited: int = 0
    panics_recovered: int = 0
    config_reloads: int = 0
    queue_pending_items: int = 0
    queue_pending_bytes: int = 0
    subsystems_disabled: int = 0


class Metrics:
    """A set of counters safe for concurrent access."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._captured = 0
        self._sent = 0
        self._dropped_overflow = 0
        self._dropped_send = 0
        self._dropped_before_send = 0
        self._retries = 0
        self._rate_limited = 0
        self._panics_recovered = 0
        self._config_reloads = 0
        self._queue_pending_items = 0
        self._queue_pending_bytes = 0
        self._subsystems_disabled = 0

    def inc_captured(self) -> None:
        """Record a successful capture (an event accepted into the
        pipeline)."""
        with self._lock:
            self._captured += 1

    def add_sent(self, n: int) -> None:
        """Record n successfully delivered events."""
        with self._lock:
            self._sent += n

    def add_dropped(self, reason: DropReason, n: int) -> None:
        """Record n dropped events for the given reason."""
        with self._lock:
            if reason is DropReason.OVERFLOW:
                self._dropped_overflow += n
            elif reason is DropReason.SEND_EXHAUSTED:
                self._dropped_send += n
            elif reason is DropReason.BEFORE_SEND:
                self._dropped_before_send += n

    def inc_retries(self) -> None:
        """Record a delivery retry attempt."""
        with self._lock:
            self._retries += 1

    def inc_rate_limited(self) -> None:
        """Record a 429 (rate limited) response from the server."""
        with self._lock:
            self._rate_limited += 1

    def inc_panics_recovered(self) -> None:
        """Record a contained SDK-internal fault."""
        with self._lock:
            self._panics_recovered += 1

    def inc_config_reloads(self) -> None:
        """Record a configuration swap."""
        with self._lock:
            self._config_reloads += 1

    def inc_subsystems_disabled(self) -> None:
        """Record a background subsystem self-disabling after a fault."""
        with self._lock:
            self._subsystems_disabled += 1

    def set_queue_pending(self, items: int, size_bytes: int) -> None:
        """Update the pending-queue gauges (current items and bytes)."""
        with self._lock:
            self._queue_pending_items = items
            self._queue_pending_bytes = size_bytes

    def snapshot(self) -> Snapshot:
        """Return an immutable copy of all counters."""
        with self._lock:
            return Snapshot(
                captured=self._captured,
                sent=self._sent,
                dropped_overflow=self._dropped_overflow,
                dropped_send=self._dropped_send,
                dropped_before_send=self._dropped_before_send,
                retries=self._retries,
                rate_limited=self._rate_limited,
                panics_recovered=self._panics_recovered,
                config_reloads=self._config_reloads,
                queue_pending_items=self._queue_pending_items,
                queue_pending_bytes=self._queue_pending_bytes,
                subsystems_disabled=self._subsystems_disabled,
            )

    def reset(self) -> None:
        """Zero every counter. It exists for test isolation and must not be
        used on a live client."""
        with self._lock:
            self._captured = 0
            self._sent = 0
            self._dropped_overflow = 0
            self._dropped_send = 0
            self._dropped_before_send = 0
            self._retries = 0
            self._rate_limited = 0
            self._panics_recovered = 0
            self._config_reloads = 0
            self._queue_pending_items = 0
            self._queue_pending_bytes = 0
            self._subsystems_disabled = 0
