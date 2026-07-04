"""The pluggable sink for SDK-internal logs."""

from __future__ import annotations

import logging
from typing import Optional, Protocol, runtime_checkable

from ._level import Level


@runtime_checkable
class Logger(Protocol):
    """The pluggable sink for SDK-internal logs. Implementations must never
    raise; a raising logger is contained by the SDK. ``suppressed`` reports
    how many identical lines were throttled since the last emitted line."""

    def log(self, level: Level, msg: str, suppressed: int) -> None:
        """Emit a single SDK-internal log line."""
        ...


_LOGGING_LEVELS = {
    Level.DEBUG: logging.DEBUG,
    Level.INFO: logging.INFO,
    Level.WARNING: logging.WARNING,
    Level.ERROR: logging.ERROR,
    Level.FATAL: logging.ERROR,
}


class StdlibLogger:
    """The default Logger, writing through the ``logging`` module."""

    def __init__(self) -> None:
        self._logger = logging.getLogger("groundcover")

    def log(self, level: Level, msg: str, suppressed: int) -> None:
        """Emit the line via logging.getLogger("groundcover")."""
        if suppressed > 0:
            msg = f"{msg} (suppressed={suppressed})"
        self._logger.log(_LOGGING_LEVELS.get(level, logging.INFO), "%s", msg)


def resolve_logger(logger: Optional[Logger]) -> Logger:
    """Return logger, or the stdlib default when None."""
    if logger is not None:
        return logger
    return StdlibLogger()
