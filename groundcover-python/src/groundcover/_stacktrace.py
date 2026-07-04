"""Stack capture and in-app frame classification.

Unlike Go errors, Python exceptions carry their traceback, so a captured
exception's frames come from ``exc.__traceback__`` (the raise site). When no
traceback is available the current call stack is captured instead, mirroring
the Go SDK.
"""

from __future__ import annotations

import os
import sys
import sysconfig
from types import FrameType, TracebackType
from typing import List, Optional

from ._event import Frame

_PACKAGE_DIR = os.path.dirname(os.path.abspath(__file__))

_STDLIB_PATHS = tuple(
    p
    for p in {
        sysconfig.get_paths().get("stdlib", ""),
        sysconfig.get_paths().get("platstdlib", ""),
    }
    if p
)

_DEFAULT_MAX_DEPTH = 128


def _frame_function(frame: FrameType) -> str:
    """Render a frame's fully-qualified function name
    (``module.Class.method``)."""
    code = frame.f_code
    name = getattr(code, "co_qualname", None) or code.co_name
    module = frame.f_globals.get("__name__", "")
    if module:
        return f"{module}.{name}"
    return name


def _make_frame(frame: FrameType, line: int, in_app_root: str) -> Frame:
    filename = frame.f_code.co_filename
    function = _frame_function(frame)
    return Frame(
        function=function,
        file=filename,
        line=line,
        in_app=is_in_app(function, filename, in_app_root),
    )


def _is_sdk_frame(frame: FrameType) -> bool:
    """Report whether a frame belongs to the SDK itself (skipped when
    capturing the caller's stack)."""
    try:
        return os.path.abspath(frame.f_code.co_filename).startswith(_PACKAGE_DIR)
    except Exception:
        return False


def capture_stack(max_depth: int, in_app_root: str) -> List[Frame]:
    """Capture the current thread's stack, innermost first, skipping the
    SDK's own frames and capturing at most max_depth frames."""
    if max_depth <= 0:
        max_depth = _DEFAULT_MAX_DEPTH
    try:
        frame: Optional[FrameType] = sys._getframe(1)
    except ValueError:
        return []
    while frame is not None and _is_sdk_frame(frame):
        frame = frame.f_back
    out: List[Frame] = []
    while frame is not None and len(out) < max_depth:
        out.append(_make_frame(frame, frame.f_lineno, in_app_root))
        frame = frame.f_back
    return out


def frames_from_traceback(
    tb: Optional[TracebackType], max_depth: int, in_app_root: str
) -> List[Frame]:
    """Resolve an exception traceback into frames, innermost first, capturing
    at most max_depth frames."""
    if max_depth <= 0:
        max_depth = _DEFAULT_MAX_DEPTH
    out: List[Frame] = []
    while tb is not None:
        out.append(_make_frame(tb.tb_frame, tb.tb_lineno, in_app_root))
        tb = tb.tb_next
    out.reverse()  # tracebacks are outermost-first; the wire format is innermost-first
    return out[:max_depth]


def is_in_app(function: str, file: str, in_app_root: str) -> bool:
    """Report whether a frame belongs to the application: its file lives under
    the application root and it is not an installed package, the stdlib, or a
    synthetic frame."""
    if not file or file.startswith("<"):
        return False
    if "/site-packages/" in file or "/dist-packages/" in file:
        return False
    for stdlib in _STDLIB_PATHS:
        if file.startswith(stdlib):
            return False
    if in_app_root:
        return file.startswith(in_app_root)
    # Without a known application root, treat non-installed, non-stdlib
    # frames as in-app on a best-effort basis.
    return not file.startswith(sys.prefix)


def in_app_frames(frames: List[Frame]) -> List[Frame]:
    """Return only the in-app frames, preserving order."""
    return [f for f in frames if f.in_app]
