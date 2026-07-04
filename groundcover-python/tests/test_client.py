"""Client behavior tests, ported from the Go SDK's client_test.go."""

from __future__ import annotations

import pytest

import groundcover
from groundcover import Client, Config, HMACHasher, Level, MissingDSNError, User
from groundcover._version import SDK_NAME

from .conftest import decode_payload


def test_capture_error_round_trip(sender, client_factory):
    c = client_factory(service_name="checkout", env="prod", release="1.2.3")

    c.set_user(User(id="u-1", organization="acme"))
    try:
        raise RuntimeError("charge failed")
    except RuntimeError as exc:
        c.capture_error(
            exc,
            attributes={
                "gc.test_id": "abc-123",
                "order_id": "o-9",
                "amount": 42.5,
                "is_retry": True,
            },
        )
    assert c.flush(5.0)

    p = decode_payload(sender)
    assert p["sessionAttributes"]["service.name"] == "checkout"
    assert p["sessionAttributes"]["env"] == "prod"
    assert p["sessionAttributes"]["releaseId"] == "1.2.3"
    assert len(p["events"]) == 1
    ev = p["events"][0]
    assert ev["type"] == "exception"
    assert ev["attributes"]["error_message"] == "charge failed"
    assert ev["attributes"]["error_handled"] is True
    md = ev["attributes"]["error_metadata"]
    assert md["gc.test_id"] == "abc-123"
    assert md["user.id"] == "u-1"
    assert md["user.organization"] == "acme"
    assert md["amount"] == 42.5
    assert md["is_retry"] is True
    assert md["telemetry.sdk.name"] == SDK_NAME
    assert c.stats().sent == 1


def test_merge_precedence_scope_then_option(sender, client_factory):
    c = client_factory()

    c.with_scope(
        lambda s: (s.set_attribute("key", "from-scope"), s.set_attribute("only-scope", "x"))
    )
    c.capture_error(ValueError("e"), attributes={"key": "from-option"})
    c.flush(5.0)

    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["key"] == "from-option", "per-call option must win"
    assert md["only-scope"] == "x", "scope attribute lost"


def test_drop_oldest_overflow(sender, client_factory):
    dropped = []
    # Prevent the worker from draining so the buffer overflows deterministically.
    c = client_factory(max_queue=3, flush_interval=3600.0, on_drop=lambda n: dropped.append(n))

    for _ in range(10):
        c.capture_error(ValueError("e"))
    assert c.stats().dropped_overflow > 0
    assert dropped, "on_drop should have been called"
    assert len(c._ring) <= 3


def test_before_send_drop(sender, client_factory):
    c = client_factory(
        before_send=lambda e: None if e.error_message == "drop me" else e,
    )

    c.capture_error(ValueError("drop me"))
    c.capture_error(ValueError("keep me"))
    c.flush(5.0)

    assert c.stats().dropped_before_send == 1
    for ev in decode_payload(sender)["events"]:
        assert ev["attributes"]["error_message"] != "drop me"


def test_before_send_scrub(sender, client_factory):
    def scrub(e):
        e.error_message = "[scrubbed]"
        return e

    c = client_factory(before_send=scrub)
    c.capture_error(ValueError("secret token=xyz"))
    c.flush(5.0)
    assert decode_payload(sender)["events"][0]["attributes"]["error_message"] == "[scrubbed]"


def test_hasher_pseudonymizes_identity(sender, client_factory):
    c = client_factory(hasher=HMACHasher(b"key"))
    c.set_user(User(id="u-1", email="a@b.com"))
    c.capture_error(ValueError("e"))
    c.flush(5.0)

    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["user.id"] not in ("u-1", "")
    assert md["user.email"] not in ("a@b.com", "")


def test_disabled_client_is_noop():
    c = Client(Config(disabled=True))
    c.capture_error(ValueError("e"))
    c.capture_message("m", Level.INFO)
    assert c.flush(1.0)
    assert c.close(1.0)
    assert c.stats() == groundcover.Stats()


def test_capture_none_error_ignored(sender, client_factory):
    c = client_factory()
    c.capture_error(None)
    c.flush(5.0)
    assert c.stats().captured == 0


def test_recover_reraises(sender, client_factory):
    c = client_factory()

    with pytest.raises(RuntimeError, match="kaboom"):
        with c.recover():
            raise RuntimeError("kaboom")

    p = decode_payload(sender)
    ev = p["events"][0]
    assert ev["attributes"]["error_handled"] is False
    assert ev["attributes"]["error_message"] == "kaboom"
    assert ev["level"] == "fatal"


def test_recover_without_exception_is_noop(sender, client_factory):
    c = client_factory()
    with c.recover():
        pass
    assert c.stats().captured == 0


def test_recover_passes_base_exceptions_through(sender, client_factory):
    c = client_factory()
    with pytest.raises(KeyboardInterrupt):
        with c.recover():
            raise KeyboardInterrupt()
    assert c.stats().captured == 0, "KeyboardInterrupt must not be captured"


def test_capture_message(sender, client_factory):
    c = client_factory()
    c.capture_message("stale cache", Level.WARNING)
    c.flush(5.0)
    ev = decode_payload(sender)["events"][0]
    assert ev["attributes"]["error_message"] == "stale cache"
    assert ev["level"] == "warning"


def test_new_requires_dsn():
    with pytest.raises(MissingDSNError):
        Client(Config())


def test_capture_never_raises_on_hostile_before_send(sender, client_factory):
    def explode(_e):
        raise RuntimeError("hostile callback")

    c = client_factory(before_send=explode)
    c.capture_error(ValueError("e"))  # must not raise
    c.flush(5.0)
    # A raising before_send keeps the event unmodified.
    assert decode_payload(sender)["events"][0]["attributes"]["error_message"] == "e"
    assert c.stats().panics_recovered >= 1
