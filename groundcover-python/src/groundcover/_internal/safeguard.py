"""Fault guards used at every SDK boundary and around every spawned thread.

The SDK must never crash or destabilize the host application: internal
exceptions are caught, converted into a structured report, and swallowed.
This is the Python translation of the Go SDK's panic safeguard; it contains
``Exception`` (not ``BaseException``, so ``KeyboardInterrupt``/``SystemExit``
still propagate to the host).
"""

from __future__ import annotations

import dataclasses
import threading
import traceback
from typing import Callable, Optional


@dataclasses.dataclass
class PanicInfo:
    """Describes a contained internal fault."""

    value: BaseException
    """The exception that was caught."""
    stack: str
    """The stack trace captured at recovery time."""


Handler = Callable[[PanicInfo], None]
"""Observes a contained fault. Implementations must not raise; if one does,
the secondary exception is itself caught and dropped."""


def do(fn: Callable[[], None], handler: Optional[Handler]) -> bool:
    """Run fn, containing any exception. Return True if fn completed without
    raising. A contained exception is reported through handler."""
    try:
        fn()
        return True
    except Exception as exc:
        _report(handler, exc)
        return False


def go(fn: Callable[[], None], handler: Optional[Handler], name: str = "") -> threading.Thread:
    """Run fn in a new daemon thread guarded against exceptions. An exception
    in fn is caught and reported through handler instead of killing the
    thread noisily."""

    def _run() -> None:
        do(fn, handler)

    t = threading.Thread(target=_run, daemon=True, name=name or "groundcover")
    t.start()
    return t


def _report(handler: Optional[Handler], exc: BaseException) -> None:
    """Invoke handler with the caught exception, guarding against a handler
    that itself raises."""
    if handler is None:
        return
    stack = traceback.format_exc()
    try:
        handler(PanicInfo(value=exc, stack=stack))
    except Exception:
        pass
