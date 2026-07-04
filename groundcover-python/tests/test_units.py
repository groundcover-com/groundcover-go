"""Unit tests for the small pure modules, ported from the Go SDK's
units_test.go."""

from __future__ import annotations

import pytest

from groundcover import Config, HMACHasher, Level, MissingDSNError
from groundcover._attributes import sanitize_value
from groundcover._config import DEFAULT_BATCH_SIZE, DEFAULT_MAX_QUEUE
from groundcover._errors import error_type
from groundcover._event import Event, Frame
from groundcover._fingerprint import fingerprint, normalize_message
from groundcover._stacktrace import capture_stack, is_in_app


class TypedError(Exception):
    def error_type(self) -> str:
        return "custom.DomainError"


class OuterError(Exception):
    """An outer wrapper advertising its own error_type; it must not relabel
    the innermost type."""

    def error_type(self) -> str:
        return "OuterType"


def test_error_type_extraction():
    assert error_type(ValueError("boom")) == "ValueError"

    # Explicit chaining resolves to the innermost cause.
    inner = ValueError("inner")
    try:
        try:
            raise inner
        except ValueError as e:
            raise RuntimeError("context") from e
    except RuntimeError as wrapped:
        assert error_type(wrapped) == "ValueError"

    # An error_type() method on the innermost error wins.
    try:
        raise RuntimeError("outer") from TypedError("inner")
    except RuntimeError as typed:
        assert error_type(typed) == "custom.DomainError"

    assert error_type(None) == ""


def test_error_type_module_qualified():
    assert error_type(TypedError("x")) == "custom.DomainError"

    class Local(Exception):
        pass

    got = error_type(Local("x"))
    assert got.endswith("Local")
    assert "test_units" in got, "non-builtin errors are module-qualified"


def test_error_type_ignores_outer_error_type_method():
    # error_type() on an outer wrapper must not relabel the innermost type.
    try:
        raise OuterError("outer") from ValueError("inner")
    except OuterError as err:
        assert error_type(err) == "ValueError"


def test_error_type_exception_group():
    import sys

    if sys.version_info < (3, 11):
        pytest.skip("ExceptionGroup requires Python 3.11+")
    eg = ExceptionGroup("joined", [ValueError("a"), KeyError("b")])  # noqa: F821
    assert error_type(eg) == "ValueError", "a group resolves to its first branch"


def test_fingerprint_stable_across_line_changes():
    e1 = Event(
        error_type="ValueError",
        stacktrace=[
            Frame(function="main.run", file="/app/main.py", line=10, in_app=True),
            Frame(function="main.helper", file="/app/util.py", line=20, in_app=True),
        ],
    )
    e2 = Event(
        error_type="ValueError",
        stacktrace=[
            Frame(function="main.run", file="/app/main.py", line=99, in_app=True),
            Frame(function="main.helper", file="/app/util.py", line=200, in_app=True),
        ],
    )
    assert fingerprint(e1) == fingerprint(e2), "fingerprint must ignore line numbers"

    e3 = Event(error_type="other", stacktrace=e1.stacktrace)
    assert fingerprint(e1) != fingerprint(e3)


def test_fingerprint_message_fallback():
    a = Event(error_message="user 12345 not found")
    b = Event(error_message="user 67890 not found")
    assert fingerprint(a) == fingerprint(b)
    c = Event(error_message="totally different")
    assert fingerprint(a) != fingerprint(c)


def test_fingerprint_matches_go_fnv():
    """The FNV-1a implementation matches Go's hash/fnv (same constants), so
    grouping keys agree across SDKs for identical input."""
    # FNV-1a 64 of "hello" is a well-known vector.
    e = Event(error_message="hello")
    assert fingerprint(e) == "a430d84680aabd0b"


def test_normalize_message():
    assert normalize_message("id=42 retries=7") == "id=0 retries=0"


def test_stacktrace_capture_and_in_app():
    frames = capture_stack(32, in_app_root=_this_dir())
    assert frames, "expected captured frames"
    assert any(f.in_app for f in frames), "expected an in-app frame under the test dir"
    # The SDK's own frames are skipped; the innermost frame is this test.
    assert frames[0].function.endswith("test_stacktrace_capture_and_in_app")


def _this_dir() -> str:
    import os

    return os.path.dirname(os.path.abspath(__file__))


def test_is_in_app():
    root = "/src/app"
    assert is_in_app("app.pkg.func", "/src/app/pkg/file.py", root)
    assert not is_in_app("x.y.func", "/venv/lib/python3.12/site-packages/x/y.py", root)
    assert not is_in_app("json.dumps", "<frozen importlib._bootstrap>", root)
    assert not is_in_app("other.func", "/elsewhere/file.py", root)


def test_sanitize_value():
    out = sanitize_value(
        {
            "s": "x",
            "n": 5,
            "f": 1.5,
            "b": True,
            "nested": {"inner": [1, "two", False]},
        },
        0,
    )
    assert out["s"] == "x"
    assert out["n"] == 5
    assert out["f"] == 1.5
    assert out["b"] is True
    assert out["nested"]["inner"] == [1, "two", False]


def test_sanitize_exception_and_object():
    assert sanitize_value(ValueError("e"), 0) == "e"

    class Custom:
        def __str__(self) -> str:
            return "custom-repr"

    assert sanitize_value(Custom(), 0) == "custom-repr"


def test_sanitize_depth_bound():
    deep: dict = {}
    cur = deep
    for _ in range(20):
        nxt: dict = {}
        cur["k"] = nxt
        cur = nxt
    out = sanitize_value(deep, 0)
    # Recursion is bounded; somewhere down the chain the rest is stringified.
    node = out
    depth = 0
    while isinstance(node, dict) and "k" in node:
        node = node["k"]
        depth += 1
    assert depth <= 10


def test_config_defaults_and_endpoint():
    c = Config(dsn="app.example.com").with_defaults()
    assert c.max_queue == DEFAULT_MAX_QUEUE
    assert c.batch_size == DEFAULT_BATCH_SIZE
    assert Config(dsn="app.example.com").endpoint() == "https://app.example.com/json/rum"
    assert Config(dsn="http://h:8080/").endpoint() == "http://h:8080/json/rum"


def test_config_validate():
    with pytest.raises(MissingDSNError):
        Config().validate()
    Config(disabled=True).validate()  # must not raise


def test_level_severity():
    assert Level.ERROR.severity_number == 17
    assert Level.DEBUG.severity_number == 5
    from groundcover._level import coerce_level

    assert coerce_level("warning") is Level.WARNING
    assert coerce_level(Level.FATAL) is Level.FATAL
    assert coerce_level("bogus") is None
    assert coerce_level(None) is None


def test_hmac_hasher():
    h = HMACHasher(b"k")
    assert h.hash_identity("") == ""
    a, b = h.hash_identity("user"), h.hash_identity("user")
    assert a == b, "hash must be deterministic"
    assert a != "user"
