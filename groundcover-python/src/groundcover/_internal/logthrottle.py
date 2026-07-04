"""A self-throttling log front-end.

SDK-internal logging must never become a source of noise or load: lines are
de-duplicated by call-site and level, suppressed within a per-key window, and
capped by a global rate. When a key is allowed to log again it reports how
many lines were suppressed in the meantime.
"""

from __future__ import annotations

import sys
import threading
import time
from typing import Callable, Dict, Optional, Tuple

from .._level import Level

Sink = Callable[[Level, str, int], None]
"""The pluggable destination for throttled log lines. ``suppressed`` is the
number of lines that were dropped for the same call-site/level since the last
emitted line. A sink must not raise; if it does, the exception is contained."""


class _Entry:
    __slots__ = ("next_allowed", "suppressed")

    def __init__(self) -> None:
        self.next_allowed = 0.0
        self.suppressed = 0


class Throttler:
    """De-duplicates and rate-limits log lines."""

    def __init__(
        self,
        sink: Optional[Sink],
        window: float = 5.0,
        global_window: float = 1.0,
        global_cap: int = 0,
        now: Optional[Callable[[], float]] = None,
    ) -> None:
        """Create a Throttler writing allowed lines to sink. A None sink
        discards output.

        window is the per-call-site suppression window; global_cap is the
        maximum number of lines emitted per global_window across all
        call-sites (zero or negative disables the global cap). now overrides
        the clock for deterministic tests.
        """
        self._window = window if window > 0 else 5.0
        self._global_window = global_window if global_window > 0 else 1.0
        self._global_cap = global_cap
        self._now = now if now is not None else time.monotonic
        self._lock = threading.Lock()
        self._entries: Dict[Tuple[str, int, Level], _Entry] = {}
        self._global_window_start: Optional[float] = None
        self._global_count = 0
        self._sink = sink

    def log(self, level: Level, msg: str) -> None:
        """Throttle a log line attributed to the immediate caller's source
        location."""
        try:
            frame = sys._getframe(1)
            key = (frame.f_code.co_filename, frame.f_lineno, level)
        except Exception:
            key = ("?", 0, level)
        emit, suppressed = self._admit(key)
        if not emit:
            return
        self._deliver(level, msg, suppressed)

    def _admit(self, key: Tuple[str, int, Level]) -> Tuple[bool, int]:
        """Apply the per-key window and global cap, returning whether the line
        should be emitted and how many prior lines were suppressed for the
        key."""
        with self._lock:
            now = self._now()
            if (
                self._global_window_start is None
                or now - self._global_window_start >= self._global_window
            ):
                self._global_window_start = now
                self._global_count = 0

            entry = self._entries.get(key)
            if entry is None:
                entry = _Entry()
                self._entries[key] = entry

            if now < entry.next_allowed:
                entry.suppressed += 1
                return False, 0
            if self._global_cap > 0 and self._global_count >= self._global_cap:
                entry.suppressed += 1
                return False, 0

            suppressed = entry.suppressed
            entry.suppressed = 0
            entry.next_allowed = now + self._window
            self._global_count += 1
            return True, suppressed

    def _deliver(self, level: Level, msg: str, suppressed: int) -> None:
        """Send a line to the sink, containing any exception from the sink
        itself."""
        if self._sink is None:
            return
        try:
            self._sink(level, msg, suppressed)
        except Exception:
            pass
