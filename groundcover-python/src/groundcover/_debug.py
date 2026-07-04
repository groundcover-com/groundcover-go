"""Compact human-readable event rendering for local development."""

from __future__ import annotations

from ._event import Attributes, Event
from ._user import User

DEBUG_MAX_FRAMES = 8
"""Bounds how many stack frames the debug renderer prints."""

DEBUG_MAX_VALUE_LEN = 80
"""Bounds the rendered length of an attribute value."""


def render_debug(e: Event) -> str:
    """Format a finalized event as a compact, human-readable block for local
    development. It renders the post-scrub, post-hash event, so it honors
    before_send scrubbing and IdentityHasher pseudonymization."""
    lines = [
        f"[groundcover] {e.level.value} {e.type}  {e.title}",
        f"  fingerprint={e.fingerprint} handled={'true' if e.error_handled else 'false'}",
    ]
    if not e.user.is_zero():
        lines.append(f"  user: {_user_summary(e.user)}")
    if e.attributes:
        lines.append(f"  attrs: {_attrs_summary(e.attributes)}")
    if e.stacktrace:
        lines.append("  stack:")
        shown = min(len(e.stacktrace), DEBUG_MAX_FRAMES)
        for f in e.stacktrace[:shown]:
            lines.append(f"    {f.function} ({f.file}:{f.line})")
        if len(e.stacktrace) > shown:
            lines.append(f"    ... {len(e.stacktrace) - shown} more")
    return "\n".join(lines) + "\n"


def _user_summary(u: User) -> str:
    parts = []
    if u.id:
        parts.append(f"id={u.id}")
    if u.email:
        parts.append(f"email={u.email}")
    if u.name:
        parts.append(f"name={u.name}")
    if u.organization:
        parts.append(f"org={u.organization}")
    return " ".join(parts)


def _attrs_summary(a: Attributes) -> str:
    parts = []
    for k in sorted(a):
        parts.append(f"{k}={_truncate_value(str(a[k]))}")
    return " ".join(parts)


def _truncate_value(s: str) -> str:
    if len(s) <= DEBUG_MAX_VALUE_LEN:
        return s
    return s[: DEBUG_MAX_VALUE_LEN - 1] + "\u2026"
