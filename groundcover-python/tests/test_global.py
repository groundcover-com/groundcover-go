"""Module-level (Sentry-style) API tests, ported from the Go SDK's
global_test.go."""

from __future__ import annotations

import pytest

import groundcover
from groundcover import Config, Level, MissingDSNError, User
from groundcover._client_global import current_global

from .conftest import MockSender, decode_payload


@pytest.fixture(autouse=True)
def _reset_global():
    """Restore the module-level client after each test."""
    import groundcover._client_global as g

    prev = g._client
    yield
    with g._lock:
        g._client = prev


def _init_with_mock(**config_kwargs) -> MockSender:
    """Install a global client wired to a mock sender (bypassing init so the
    test seam can be injected)."""
    import groundcover._client_global as g

    sender = MockSender()
    config_kwargs.setdefault("dsn", "https://example.invalid")
    client = groundcover.Client(Config(**config_kwargs), _sender=sender)
    with g._lock:
        g._client = client
    return sender


def test_global_functions_are_safe_before_init():
    import groundcover._client_global as g

    with g._lock:
        g._client = None
    groundcover.capture_error(ValueError("e"))
    groundcover.capture_message("m", Level.INFO)
    assert groundcover.flush(1.0)
    assert groundcover.global_stats().captured == 0
    assert current_global().disabled


def test_init_requires_dsn():
    with pytest.raises(MissingDSNError):
        groundcover.init(Config())


def test_init_rejects_config_and_kwargs():
    with pytest.raises(TypeError):
        groundcover.init(Config(disabled=True), dsn="x")


def test_init_replaces_previous_client():
    groundcover.init(disabled=True)
    first = current_global()
    groundcover.init(disabled=True)
    assert current_global() is not first


def test_global_capture_round_trip():
    sender = _init_with_mock(service_name="svc")
    groundcover.set_user(User(id="u-1"))
    groundcover.capture_error(ValueError("boom"))
    assert groundcover.flush(5.0)

    p = decode_payload(sender)
    assert p["sessionAttributes"]["service.name"] == "svc"
    assert p["events"][0]["attributes"]["error_metadata"]["user.id"] == "u-1"
    assert groundcover.global_stats().captured == 1
    groundcover.close(5.0)


def test_global_recover_reraises_and_captures():
    sender = _init_with_mock()
    with pytest.raises(RuntimeError, match="kaboom"):
        with groundcover.recover():
            raise RuntimeError("kaboom")
    ev = decode_payload(sender)["events"][0]
    assert ev["attributes"]["error_handled"] is False
    groundcover.close(5.0)


def test_global_with_scope_and_isolated_scope():
    sender = _init_with_mock()
    with groundcover.isolated_scope():
        groundcover.with_scope(lambda s: s.set_attribute("feature", "checkout"))
        groundcover.capture_error(ValueError("e"))
    groundcover.flush(5.0)
    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["feature"] == "checkout"
    groundcover.close(5.0)
