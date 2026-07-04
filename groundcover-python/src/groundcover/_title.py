"""Human-readable display title derivation."""

from __future__ import annotations

from ._event import Event

MAX_TITLE_LEN = 256
"""Caps the computed display title length (in characters)."""

MESSAGE_ERROR_TYPE = "message"
"""The error_type marker used for capture_message events."""


def title_for(e: Event) -> str:
    """Derive a human-readable, single-line display title from an event's type
    and message. It mirrors Sentry's "{type}: {value}" issue title, with the
    message reduced to a single line and the whole title length-capped.
    Non-error notices (capture_message) use the bare message."""
    msg = collapse_whitespace(e.error_message)
    if e.error_type in ("", MESSAGE_ERROR_TYPE):
        return truncate_title(msg, MAX_TITLE_LEN)
    if not msg:
        return truncate_title(e.error_type, MAX_TITLE_LEN)
    return truncate_title(f"{e.error_type}: {msg}", MAX_TITLE_LEN)


def collapse_whitespace(s: str) -> str:
    """Replace every run of Unicode whitespace (including newlines and tabs)
    with a single space and trim the ends."""
    out: list = []
    in_space = False
    for ch in s:
        if ch.isspace():
            in_space = True
            continue
        if in_space and out:
            out.append(" ")
        in_space = False
        out.append(ch)
    return "".join(out)


def truncate_title(s: str, max_chars: int) -> str:
    """Shorten s to at most max_chars characters, appending an ellipsis when
    it cuts."""
    if max_chars <= 0:
        return s
    if len(s) <= max_chars:
        return s
    if max_chars == 1:
        return "\u2026"
    return s[: max_chars - 1] + "\u2026"
