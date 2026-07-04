"""Random identifiers used on the wire."""

from __future__ import annotations

import os

_ZERO_UUID = "00000000-0000-0000-0000-000000000000"


def new_uuid() -> str:
    """Return a random RFC 4122 version 4 UUID string. On the unlikely event
    that the system RNG fails, it returns a zero UUID rather than raising."""
    try:
        b = bytearray(os.urandom(16))
    except Exception:
        return _ZERO_UUID
    b[6] = (b[6] & 0x0F) | 0x40  # version 4
    b[8] = (b[8] & 0x3F) | 0x80  # variant 10
    h = bytes(b).hex()
    return f"{h[0:8]}-{h[8:12]}-{h[12:16]}-{h[16:20]}-{h[20:32]}"


def new_span_id() -> str:
    """Return a random 8-byte hex identifier (16 hex chars), matching the RUM
    event id/spanId shape."""
    return _rand_hex(8)


def new_trace_id() -> str:
    """Return a random 16-byte hex identifier (32 hex chars)."""
    return _rand_hex(16)


def _rand_hex(n: int) -> str:
    try:
        return os.urandom(n).hex()
    except Exception:
        return "00" * n
