"""Error type extraction.

The Go SDK unwraps wrapped errors to their innermost cause and honors an
``ErrorType()`` override. The Python translation follows the explicit
``__cause__`` chain (``raise ... from ...``) and the first branch of an
exception group, and honors an ``error_type()`` method on the innermost
exception.
"""

from __future__ import annotations

from typing import Any, Optional


def innermost_exception(exc: BaseException) -> BaseException:
    """Unwrap exc following both explicit chaining (``__cause__``) and
    exception groups, returning the innermost meaningful exception. For a
    group it follows the first branch. Cycles are guarded against."""
    seen: set = set()
    while id(exc) not in seen:
        seen.add(id(exc))
        cause = exc.__cause__
        if isinstance(cause, BaseException):
            exc = cause
            continue
        branches = getattr(exc, "exceptions", None)
        if (
            isinstance(branches, (list, tuple))
            and branches
            and isinstance(branches[0], BaseException)
        ):
            exc = branches[0]
            continue
        break
    return exc


def error_type(exc: Optional[BaseException]) -> str:
    """Return the type string for exc, using the innermost chained exception:

    1. an ``error_type() -> str`` method if present;
    2. otherwise the qualified class name (e.g. ``ValueError`` or
       ``mypkg.DomainError``).

    This ports the OTel semconv ErrorType behavior.
    """
    if exc is None:
        return ""
    inner = innermost_exception(exc)

    # Both the error_type() override and the reflected type are taken from
    # the innermost exception, so an outer wrapper cannot relabel the type.
    override = getattr(inner, "error_type", None)
    if callable(override):
        try:
            t = override()
        except Exception:
            t = ""
        if isinstance(t, str) and t:
            return t

    return type_name(type(inner))


def type_name(cls: Any) -> str:
    """Render an exception class as a module-qualified name such as
    ``mypkg.DomainError``, with the ``builtins`` prefix stripped
    (``ValueError``, not ``builtins.ValueError``)."""
    module = getattr(cls, "__module__", "") or ""
    name = getattr(cls, "__qualname__", "") or getattr(cls, "__name__", "") or "error"
    if not module or module == "builtins":
        return name
    return f"{module}.{name}"
