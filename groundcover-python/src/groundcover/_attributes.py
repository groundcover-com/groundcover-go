"""Attribute sanitization: the capture-time snapshot of custom data."""

from __future__ import annotations

from typing import Any, Mapping

from ._event import Attributes

MAX_ATTR_DEPTH = 10
"""Bounds attribute nesting to avoid pathological or cyclic input."""


def sanitize_attributes(a: Attributes) -> Attributes:
    """Return a deep, JSON-coerced copy of a. It is applied at capture time so
    the queued event is an immutable snapshot (later caller mutation of nested
    mappings/sequences cannot change it) and so the byte estimate and wire
    encoding operate on fully-expanded values."""
    if not a:
        return {}
    return {str(k): sanitize_value(v, 0) for k, v in a.items()}


def sanitize_value(v: Any, depth: int) -> Any:
    """Convert an arbitrary value into a JSON-friendly form, bounding recursion
    depth and coercing unsupported types to strings. It is the single place
    that decides how Python values appear on the wire."""
    if v is None:
        return None
    if depth >= MAX_ATTR_DEPTH:
        return _stringify(v)
    if isinstance(v, bool):  # bool before int: bool is an int subclass
        return v
    if isinstance(v, (str, int, float)):
        return v
    if isinstance(v, BaseException):
        return _stringify(v)
    if isinstance(v, Mapping):
        return {_stringify(k): sanitize_value(item, depth + 1) for k, item in v.items()}
    if isinstance(v, (list, tuple, set, frozenset)):
        return [sanitize_value(item, depth + 1) for item in v]
    return _stringify(v)


def _stringify(v: Any) -> str:
    try:
        return str(v)
    except Exception:
        return f"<unprintable {type(v).__name__}>"
