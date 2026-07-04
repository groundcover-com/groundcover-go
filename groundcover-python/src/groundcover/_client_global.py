"""The module-level default client (Sentry style).

This holds the single intentional module-level mutable global in the SDK; all
other state is per-Client. It starts as a no-op client so the module functions
are safe to call before init().
"""

from __future__ import annotations

import threading
from typing import Any, Callable, ContextManager, Optional, Union

from ._client import PANIC_FLUSH_TIMEOUT, Client
from ._config import Config
from ._event import Attributes
from ._internal import safeguard
from ._level import Level
from ._scope import Scope, isolated_scope
from ._stats import Stats
from ._user import User

GLOBAL_RECLOSE_TIMEOUT = 5.0
"""Bounds the background teardown of a previous global client when init() is
called more than once."""

_lock = threading.Lock()
_client: Optional[Client] = None
_disabled_client: Optional[Client] = None


def _noop_client() -> Client:
    """Return the shared no-op client used until init() succeeds."""
    global _disabled_client
    with _lock:
        if _disabled_client is None:
            _disabled_client = Client(Config(disabled=True))
        return _disabled_client


def current_global() -> Client:
    """Return the active global client, or the no-op client if init() has not
    been called."""
    c = _client
    if c is not None:
        return c
    return _noop_client()


def init(config: Optional[Config] = None, **kwargs: Any) -> None:
    """Configure the module-level default client.

    Accepts either a Config or Config field names as keyword arguments:

        groundcover.init(dsn="https://...", ingestion_key="...")

    Calling it again replaces the previous default and tears the old one down
    in the background (a bounded, best-effort close) so its worker thread does
    not linger. init() never blocks on that teardown. Raises MissingDSNError
    when the configuration is unusable.
    """
    global _client
    if config is None:
        config = Config(**kwargs)
    elif kwargs:
        raise TypeError("pass either a Config or keyword arguments, not both")

    c = Client(config)
    with _lock:
        prev = _client
        _client = c
    if prev is not None and prev is not c:
        threading.Thread(
            target=lambda: prev.close(GLOBAL_RECLOSE_TIMEOUT),
            daemon=True,
            name="groundcover-reclose",
        ).start()


def capture_error(
    error: Optional[BaseException],
    *,
    attributes: Optional[Attributes] = None,
    user: Optional[User] = None,
    level: Union[Level, str, None] = None,
    fingerprint: Optional[str] = None,
    title: Optional[str] = None,
) -> None:
    """Capture an exception using the module-level client."""
    current_global().capture_error(
        error,
        attributes=attributes,
        user=user,
        level=level,
        fingerprint=fingerprint,
        title=title,
    )


def capture_message(
    message: str,
    level: Union[Level, str] = Level.INFO,
    *,
    attributes: Optional[Attributes] = None,
    user: Optional[User] = None,
    fingerprint: Optional[str] = None,
    title: Optional[str] = None,
) -> None:
    """Capture a non-error notice using the module-level client."""
    current_global().capture_message(
        message,
        level,
        attributes=attributes,
        user=user,
        fingerprint=fingerprint,
        title=title,
    )


def capture_recovered(
    recovered: Any,
    *,
    attributes: Optional[Attributes] = None,
    user: Optional[User] = None,
    level: Union[Level, str, None] = None,
    fingerprint: Optional[str] = None,
    title: Optional[str] = None,
) -> None:
    """Capture an already-caught exception as unhandled, without re-raising,
    using the module-level client."""
    current_global().capture_recovered(
        recovered,
        attributes=attributes,
        user=user,
        level=level,
        fingerprint=fingerprint,
        title=title,
    )


class _GlobalRecoverContext:
    """Context manager backing the module-level recover(). It resolves the
    global client at exit time so a client installed after entering is still
    used."""

    def __enter__(self) -> None:
        return None

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> bool:
        if exc is None or not isinstance(exc, Exception):
            return False
        c = current_global()
        if not c.disabled:

            def _do() -> None:
                c.capture_recovered(exc)
                # Detached best-effort flush before the exception re-raises.
                c.flush(PANIC_FLUSH_TIMEOUT)

            safeguard.do(_do, c._on_panic)
        return False


def recover() -> ContextManager[None]:
    """Return a context manager that captures an escaping exception (then
    re-raises it) using the module-level client:

        with groundcover.recover():
            do_risky_work()
    """
    return _GlobalRecoverContext()


def set_user(user: User) -> Scope:
    """Set the identity on the current request scope, using the module-level
    client."""
    return current_global().set_user(user)


def with_scope(fn: Optional[Callable[[Scope], None]]) -> Scope:
    """Apply fn to the current request scope (mutating an existing scope in
    place), using the module-level client."""
    return current_global().with_scope(fn)


def flush(timeout: Optional[float] = None) -> bool:
    """Flush the module-level client."""
    return current_global().flush(timeout)


def close(timeout: Optional[float] = None) -> bool:
    """Close the module-level client."""
    return current_global().close(timeout)


def global_stats() -> Stats:
    """Return the module-level client's self-metrics snapshot. (The
    per-client accessor is the Client.stats method; this avoids colliding
    with the Stats type at module scope.)"""
    return current_global().stats()


__all__ = [
    "capture_error",
    "capture_message",
    "capture_recovered",
    "close",
    "current_global",
    "flush",
    "global_stats",
    "init",
    "isolated_scope",
    "recover",
    "set_user",
    "with_scope",
]
