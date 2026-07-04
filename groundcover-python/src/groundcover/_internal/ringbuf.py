"""The SDK's bounded pending buffer.

A FIFO bounded by both an item count and a byte budget. On overflow it evicts
the oldest entries ("drop-oldest, newest wins") and reports how many were
dropped, so the pipeline can account for them. All operations are safe for
concurrent use.
"""

from __future__ import annotations

import collections
import threading
from typing import Any, Callable, List, Optional

Sizer = Callable[[Any], int]
"""Estimates the byte cost of a single item for the byte budget. The returned
value is clamped to a minimum of one."""

_UNBOUNDED = 1 << 62


class RingBuffer:
    """A bounded FIFO. Push always accepts (the caller never blocks); the
    oldest entries are evicted to stay within bounds."""

    def __init__(self, max_items: int, max_bytes: int, size_of: Optional[Sizer] = None) -> None:
        """Create a buffer bounded by max_items entries and max_bytes total
        estimated size. Non-positive bounds fall back to permissive defaults.
        size_of may be None, in which case every item counts as one byte."""
        self._max_items = max(1, max_items)
        self._max_bytes = max_bytes if max_bytes > 0 else _UNBOUNDED
        self._size_of: Sizer = size_of if size_of is not None else (lambda _item: 1)
        self._lock = threading.Lock()
        self._items: collections.deque = collections.deque()
        self._sizes: collections.deque = collections.deque()
        self._bytes = 0

    def push(self, item: Any) -> int:
        """Append item, evicting the oldest entries until the buffer is within
        both bounds. It always accepts item and returns the number of entries
        evicted to make room."""
        size = max(1, self._size_of(item))
        with self._lock:
            dropped = 0
            if len(self._items) == self._max_items:
                self._pop_oldest()
                dropped += 1
            self._items.append(item)
            self._sizes.append(size)
            self._bytes += size
            while self._bytes > self._max_bytes and len(self._items) > 1:
                self._pop_oldest()
                dropped += 1
            return dropped

    def pop_batch(self, max_items: int, max_bytes: int) -> List[Any]:
        """Remove and return up to max_items oldest entries, stopping early if
        adding the next entry would exceed max_bytes (at least one entry is
        always returned when the buffer is non-empty). Non-positive limits are
        ignored."""
        with self._lock:
            out: List[Any] = []
            batch_bytes = 0
            while self._items:
                if max_items > 0 and len(out) >= max_items:
                    break
                size = self._sizes[0]
                if out and max_bytes > 0 and batch_bytes + size > max_bytes:
                    break
                out.append(self._pop_oldest())
                batch_bytes += size
            return out

    def drain_all(self) -> List[Any]:
        """Remove and return every entry, oldest first."""
        with self._lock:
            out = list(self._items)
            self._items.clear()
            self._sizes.clear()
            self._bytes = 0
            return out

    def __len__(self) -> int:
        """Return the current number of buffered entries."""
        with self._lock:
            return len(self._items)

    def pending_bytes(self) -> int:
        """Return the current estimated size of buffered entries."""
        with self._lock:
            return self._bytes

    def _pop_oldest(self) -> Any:
        """Remove and return the head entry. The caller must hold the lock and
        ensure the buffer is non-empty."""
        self._bytes -= self._sizes.popleft()
        return self._items.popleft()
