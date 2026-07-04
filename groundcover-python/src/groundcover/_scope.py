"""Request-level scope, carried on contextvars.

The Go SDK threads scope through ``context.Context``; the Python translation
stores it in a ``contextvars.ContextVar``, which propagates naturally through
async tasks and can be copied into threads with ``contextvars.copy_context``.
"""

from __future__ import annotations

import contextlib
import contextvars
import threading
from typing import Any, Iterator, Optional, Union

from ._event import Attributes, Event
from ._level import Level, coerce_level
from ._user import User


class Scope:
    """Holds request-level data merged into every event captured while the
    scope is current. It sits between global defaults and per-call options in
    the merge precedence.

    A Scope is mutable and safe for concurrent use. Middleware installs one
    fresh, isolated Scope per request (see isolated_scope()); handlers then
    mutate that same Scope through set_user / with_scope, and the captured
    event observes those changes.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._user = User()
        self._attributes: Attributes = {}
        self._level: Optional[Level] = None
        self._fingerprint = ""
        self._session_id = ""
        self._anonymous_id = ""

    def set_user(self, user: User) -> None:
        """Set the identity on the scope."""
        with self._lock:
            self._user = user.copy()

    def set_attributes(self, attributes: Attributes) -> None:
        """Merge attributes into the scope."""
        with self._lock:
            self._attributes.update(attributes)

    def set_attribute(self, key: str, value: Any) -> None:
        """Set a single attribute on the scope."""
        with self._lock:
            self._attributes[key] = value

    def set_level(self, level: Union[Level, str]) -> None:
        """Set the default severity for events in this scope. Note that the
        scope level never downgrades an intrinsically-fatal event (an uncaught
        exception)."""
        with self._lock:
            self._level = coerce_level(level)

    def set_fingerprint(self, fingerprint: str) -> None:
        """Override the grouping fingerprint for events in this scope."""
        with self._lock:
            self._fingerprint = fingerprint

    def set_session_id(self, session_id: str) -> None:
        """Set the session identifier for events in this scope."""
        with self._lock:
            self._session_id = session_id

    def set_anonymous_id(self, anonymous_id: str) -> None:
        """Set the pre-auth anonymous identifier for events in this scope."""
        with self._lock:
            self._anonymous_id = anonymous_id

    def clone(self) -> Scope:
        """Return a deep copy of the scope."""
        out = Scope()
        with self._lock:
            out._user = self._user.copy()
            out._attributes = dict(self._attributes)
            out._level = self._level
            out._fingerprint = self._fingerprint
            out._session_id = self._session_id
            out._anonymous_id = self._anonymous_id
        return out

    def apply_to(self, e: Event) -> None:
        """Merge the scope into an event. It runs after global defaults and
        before per-call options."""
        with self._lock:
            if not self._user.is_zero():
                e.user = self._user.copy()
            if self._attributes:
                e.attributes.update(self._attributes)
            # A scope level fills in / overrides the default, but must never
            # downgrade an intrinsically-fatal event (an uncaught exception).
            if self._level is not None and not e._level_locked:
                e.level = self._level
            if self._fingerprint:
                e.fingerprint = self._fingerprint
            if self._session_id:
                e.session_id = self._session_id
            if self._anonymous_id:
                e.anonymous_id = self._anonymous_id


_scope_var: contextvars.ContextVar[Optional[Scope]] = contextvars.ContextVar(
    "groundcover_scope", default=None
)


def current_scope() -> Optional[Scope]:
    """Return the scope attached to the current context, or None."""
    return _scope_var.get()


def ensure_scope() -> Scope:
    """Return the scope already attached to the current context (so mutations
    are visible to everything sharing that context), creating and attaching a
    fresh one if there is none."""
    sc = _scope_var.get()
    if sc is not None:
        return sc
    sc = Scope()
    _scope_var.set(sc)
    return sc


@contextlib.contextmanager
def isolated_scope() -> Iterator[Scope]:
    """Context manager installing a fresh, isolated copy of the current scope.
    Middleware uses it at request boundaries so per-request identity and
    attributes never leak across requests."""
    current = _scope_var.get()
    sc = current.clone() if current is not None else Scope()
    token = _scope_var.set(sc)
    try:
        yield sc
    finally:
        _scope_var.reset(token)
