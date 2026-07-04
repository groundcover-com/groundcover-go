"""Severity levels for captured events.

Levels follow OTel SeverityText conventions and map to a numeric
SeverityNumber on the wire.
"""

from __future__ import annotations

import enum
from typing import Optional, Union


class Level(str, enum.Enum):
    """The severity of a captured event."""

    DEBUG = "debug"
    """Fine-grained diagnostic information."""
    INFO = "info"
    """Informational."""
    WARNING = "warning"
    """A recoverable problem or notable condition."""
    ERROR = "error"
    """An error; the default for capture_error."""
    FATAL = "fatal"
    """An unrecoverable error."""

    @property
    def severity_number(self) -> int:
        """Map the level to the OTel SeverityNumber range."""
        return _SEVERITY_NUMBERS.get(self, 17)


_SEVERITY_NUMBERS = {
    Level.DEBUG: 5,
    Level.INFO: 9,
    Level.WARNING: 13,
    Level.ERROR: 17,
    Level.FATAL: 21,
}


def coerce_level(value: Union[Level, str, None]) -> Optional[Level]:
    """Return the Level for value, accepting Level members or their string
    names/values; return None for anything unrecognized."""
    if value is None:
        return None
    if isinstance(value, Level):
        return value
    if isinstance(value, str):
        try:
            return Level(value.lower())
        except ValueError:
            return None
    return None
