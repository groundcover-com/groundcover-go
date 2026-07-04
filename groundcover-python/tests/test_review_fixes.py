"""Hardening behaviors, ported from the Go SDK's review_fixes_test.go."""

from __future__ import annotations

import io

from groundcover import HMACHasher, Level, User
from groundcover._attributes import sanitize_attributes
from groundcover._debug import render_debug
from groundcover._event import Event, Frame

from .conftest import decode_payload


def test_reserved_keys_not_overridable_by_attributes(sender, client_factory):
    """Caller attributes cannot overwrite SDK-managed identity/severity keys
    (which would bypass the IdentityHasher or break numeric type stability)."""
    c = client_factory(hasher=HMACHasher(b"k"))

    c.set_user(User(id="u-1", email="a@b.com"))
    c.capture_error(
        ValueError("e"),
        attributes={
            "user.id": "plaintext-bypass",
            "user.email": "raw@evil.com",
            "severity_number": "not-a-number",
            "level": "definitely-not-a-level",
            "order_id": "kept",  # a normal custom attr survives
        },
    )
    c.flush(5.0)

    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["user.id"] != "plaintext-bypass"
    assert md["user.id"] != "u-1", "user.id should be hashed, not raw"
    assert md["user.email"] != "raw@evil.com"
    assert isinstance(md["severity_number"], int), "severity_number must stay numeric"
    assert md["level"] == Level.ERROR.value, "level must be SDK-managed"
    assert md["order_id"] == "kept"


def test_title_and_fingerprint_computed_after_before_send(sender, client_factory):
    """A scrubber that rewrites the message is reflected in the title (no
    pre-scrub data leaks)."""

    def scrub(e):
        e.error_message = "[redacted]"
        return e

    c = client_factory(before_send=scrub)
    c.capture_error(ValueError("secret-token-12345"))
    c.flush(5.0)

    ev = decode_payload(sender)["events"][0]
    title = ev["attributes"]["error_metadata"]["gc.title"]
    assert "secret-token" not in title
    assert "[redacted]" in title
    assert "secret-token" not in ev["attributes"]["error_message"]


def test_before_send_can_override_title_and_fingerprint(sender, client_factory):
    """Explicit values set in before_send are preserved (not recomputed over)."""

    def override(e):
        e.title = "Custom Title"
        e.fingerprint = "custom-fp"
        return e

    c = client_factory(before_send=override)
    c.capture_error(ValueError("x"))
    c.flush(5.0)

    ev = decode_payload(sender)["events"][0]
    assert ev["attributes"]["error_fingerprint"] == "custom-fp"
    assert ev["attributes"]["error_metadata"]["gc.title"] == "Custom Title"


def test_attributes_snapshot_on_capture(sender, client_factory):
    """Mutating a nested attribute value after capture does not change the
    already-queued event."""
    c = client_factory(flush_interval=3600.0)

    nested = {"k": "original"}
    listed = ["a", "b"]
    c.capture_error(ValueError("e"), attributes={"data": nested, "list": listed})

    # Mutate the caller's structures after capture but before flush/encode.
    nested["k"] = "mutated"
    listed[0] = "z"

    c.flush(5.0)

    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["data"] == {"k": "original"}, "nested mapping was not snapshotted at capture"
    assert md["list"][0] == "a", "nested list was not snapshotted at capture"


def test_hasher_applies_after_before_send(sender, client_factory):
    """Identity set (or left raw) by before_send is still pseudonymized — the
    hash runs after before_send."""

    def set_raw(e):
        e.user.id = "raw-from-beforesend"
        e.user.email = "raw@evil.com"
        return e

    c = client_factory(hasher=HMACHasher(b"k"), before_send=set_raw)
    c.capture_error(ValueError("e"))
    c.flush(5.0)

    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["user.id"] != "raw-from-beforesend"
    assert md["user.email"] != "raw@evil.com"
    assert md["user.id"], "expected a hashed user.id"


def test_capture_message_level_beats_scope(sender, client_factory):
    """The per-call level argument wins over a scope level
    (global < scope < per-call)."""
    c = client_factory()
    c.with_scope(lambda s: s.set_level(Level.WARNING))
    c.capture_message("m", Level.ERROR)
    c.flush(5.0)
    assert decode_payload(sender)["events"][0]["level"] == "error"


def test_scope_does_not_downgrade_panic_level(sender, client_factory):
    """A captured unhandled exception stays fatal even when the request scope
    sets a lower level."""
    c = client_factory()
    c.with_scope(lambda s: s.set_level(Level.WARNING))
    c.capture_recovered(RuntimeError("kaboom"))
    c.flush(5.0)
    assert decode_payload(sender)["events"][0]["level"] == "fatal"


def test_scope_still_sets_level_for_non_panic(sender, client_factory):
    c = client_factory()
    c.with_scope(lambda s: s.set_level(Level.WARNING))
    c.capture_error(ValueError("e"))
    c.flush(5.0)
    assert decode_payload(sender)["events"][0]["level"] == "warning"


def test_shared_scope_mutation_visible_to_capture(sender, client_factory):
    """Mutating the scope after middleware seeds it (as middleware + handler
    do) is seen at capture."""
    c = client_factory()
    with c.isolated_scope():  # middleware seeds
        c.set_user(User(id="handler-user"))  # handler mutates
        c.capture_error(ValueError("e"))  # capture in the same context
    c.flush(5.0)
    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["user.id"] == "handler-user"


def test_debug_mode_honors_scrubbing(sender, client_factory):
    buf = io.StringIO()
    c = client_factory(debug=True, hasher=HMACHasher(b"k"))
    c._debug_out = buf

    c.set_user(User(id="secret-user-id"))
    c.capture_error(ValueError("boom"))
    c.flush(5.0)

    out = buf.getvalue()
    assert out, "debug mode should write the event"
    assert "secret-user-id" not in out, "debug output must honor identity hashing"
    assert "[groundcover]" in out
    assert "boom" in out


def test_render_debug():
    e = Event(
        level=Level.ERROR,
        type="exception",
        title="x.E: boom",
        fingerprint="fp",
        error_handled=True,
        attributes={"order_id": "o-9"},
        stacktrace=[Frame(function="main.run", file="/app/main.py", line=10)],
    )
    e.user.id = "u-1"
    e.user.organization = "acme"
    out = render_debug(e)
    for want in (
        "[groundcover] error exception",
        "x.E: boom",
        "fingerprint=fp",
        "id=u-1",
        "order_id=o-9",
        "main.run",
    ):
        assert want in out


def test_sanitize_attributes_expands_typed_collections():
    """The capture-time snapshot expands non-list/dict collections so the byte
    estimate sees real structure."""
    out = sanitize_attributes({"ids": (1, 2, 3), "m": {"a": 1}})
    assert isinstance(out["ids"], list)
    assert isinstance(out["m"], dict)
    assert sanitize_attributes({}) == {}
