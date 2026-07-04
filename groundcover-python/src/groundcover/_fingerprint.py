"""Client-side grouping fingerprint."""

from __future__ import annotations

from ._event import Event
from ._stacktrace import in_app_frames

_FNV_OFFSET = 0xCBF29CE484222325
_FNV_PRIME = 0x100000001B3
_MASK64 = 0xFFFFFFFFFFFFFFFF


def _fnv1a(chunks: list) -> int:
    """Compute the 64-bit FNV-1a hash of the concatenated byte chunks. The
    algorithm matches Go's hash/fnv so grouping keys agree across SDKs."""
    h = _FNV_OFFSET
    for chunk in chunks:
        for b in chunk:
            h ^= b
            h = (h * _FNV_PRIME) & _MASK64
    return h


def fingerprint(e: Event) -> str:
    """Compute the naive v1 client-side grouping key for an event.

    Component priority mirrors the server pipeline: when in-app frames exist,
    the fingerprint is the error type plus the in-app frame signatures
    (function+file, no line numbers, so the group is stable across edits);
    otherwise it falls back to a normalized message with identifiers and
    numbers stripped.
    """
    frames = in_app_frames(e.stacktrace)
    if frames:
        chunks = [e.error_type.encode("utf-8")]
        for f in frames:
            chunks.append(b"\x00")
            chunks.append(f.function.encode("utf-8"))
            chunks.append(b"\x00")
            chunks.append(f.file.encode("utf-8"))
    else:
        chunks = [normalize_message(e.error_message).encode("utf-8")]
    return format(_fnv1a(chunks), "x")


def normalize_message(msg: str) -> str:
    """Collapse each maximal run of digits into a single "0" so that messages
    differing only by identifiers/counters group together when no stack is
    available."""
    out: list = []
    in_digits = False
    for ch in msg:
        if ch.isdigit():
            if not in_digits:
                out.append("0")
                in_digits = True
            continue
        in_digits = False
        out.append(ch)
    return "".join(out).strip()
