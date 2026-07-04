"""The internal representation of a captured occurrence."""

from __future__ import annotations

import dataclasses
from typing import Any, Dict, List

from ._level import Level
from ._user import User

Attributes = Dict[str, Any]
"""A bag of custom, caller-supplied data attached to an event.

Nested mappings and sequences are allowed. Values are kept with their natural
JSON type so the backend can route them (strings/bools into string columns,
numbers into numeric columns). The ``gc.*`` key namespace is reserved for the
SDK.
"""

EVENT_TYPE = "exception"
"""The only event type emitted in v1 (error tracking)."""


@dataclasses.dataclass
class Frame:
    """A single resolved stack frame.

    Field names follow OTel ``code.*`` semantics internally; they are mapped to
    the wire representation at encode time.
    """

    function: str = ""
    """The fully-qualified function name (code.function.name)."""
    file: str = ""
    """The source file path (code.file.path)."""
    line: int = 0
    """The source line number (code.line.number)."""
    in_app: bool = False
    """Whether the frame belongs to the application (under the detected
    application root, excluding installed packages and the stdlib)."""


@dataclasses.dataclass
class Service:
    """Identifies the instrumented service on the wire."""

    name: str = ""
    """service.name."""
    version: str = ""
    """service.version."""


@dataclasses.dataclass
class Event:
    """The internal representation of a captured occurrence.

    Public callers never build an Event directly; per-call keyword options
    mutate it before enqueue. It is exported only so that ``before_send`` can
    operate on it.
    """

    id: str = ""
    """A per-occurrence identifier used for de-duplication."""
    timestamp_ns: int = 0
    """When the event was captured, in nanoseconds since the Unix epoch."""
    type: str = EVENT_TYPE
    """The event type (always "exception" in v1)."""
    level: Level = Level.ERROR
    """The severity."""
    user: User = dataclasses.field(default_factory=User)
    """The associated identity, if any."""
    session_id: str = ""
    """An optional session identifier (usually empty for backends)."""
    anonymous_id: str = ""
    """A caller-supplied pre-auth identifier (no PII by construction)."""
    service: Service = dataclasses.field(default_factory=Service)
    """Identifies the instrumented service."""
    error_type: str = ""
    """The innermost meaningful error type."""
    error_message: str = ""
    """The error message."""
    error_handled: bool = True
    """Whether the error was handled (vs. an uncaught exception)."""
    stacktrace: List[Frame] = dataclasses.field(default_factory=list)
    """The resolved frames, innermost first."""
    fingerprint: str = ""
    """The client-computed grouping key (opaque hash)."""
    title: str = ""
    """The human-readable display label (e.g. "ConnectionError: connection
    refused"). Derived from error_type and error_message when left empty.
    Unlike fingerprint, it is for display, not grouping."""
    attributes: Attributes = dataclasses.field(default_factory=dict)
    """The custom data bag."""

    _level_locked: bool = dataclasses.field(default=False, repr=False)
    """Marks an intrinsically-severe event (an uncaught exception) whose level
    must not be downgraded by a request scope. Per-call options may still
    change it."""
