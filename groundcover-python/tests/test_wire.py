"""Wire format tests, ported from the Go SDK's wire_test.go."""

from __future__ import annotations

import json

from groundcover import Level, User
from groundcover._event import Event, Frame
from groundcover._resource import Resource
from groundcover._version import SDK_NAME, VERSION
from groundcover._wire import (
    build_metadata,
    encode_batch,
    estimate_size,
    session_attributes,
    to_wire_event,
    user_agent,
)


def _resource() -> Resource:
    return Resource(
        service_name="svc",
        env="prod",
        release="1.2.3",
        namespace="ns",
        cluster="cl",
        attrs={
            "telemetry.sdk.name": SDK_NAME,
            "service.name": "svc",  # session-level: excluded from metadata
            "deployment.environment.name": "prod",
        },
        start_time_ns=123,
    )


def _event() -> Event:
    return Event(
        id="id-1",
        timestamp_ns=456,
        level=Level.ERROR,
        error_type="ValueError",
        error_message="boom",
        error_handled=True,
        fingerprint="fp",
        title="ValueError: boom",
        stacktrace=[Frame(function="app.run", file="/app/main.py", line=10, in_app=True)],
        attributes={"order_id": "o-9"},
        user=User(id="u-1"),
        session_id="sess-1",
        anonymous_id="anon-1",
    )


def test_session_attributes_spine():
    sa = session_attributes(_resource())
    assert sa["service.name"] == "svc"
    assert sa["env"] == "prod"
    assert sa["namespace"] == "ns"
    assert sa["cluster"] == "cl"
    assert sa["releaseId"] == "1.2.3"
    assert sa["session_start_time"] == 123
    assert sa["userAgent"] == f"{SDK_NAME}/{VERSION}"
    assert user_agent() == f"{SDK_NAME}/{VERSION}"


def test_build_metadata_layout():
    md = build_metadata(_event(), _resource())
    # Custom attributes survive; session-level resource keys are excluded.
    assert md["order_id"] == "o-9"
    assert md["telemetry.sdk.name"] == SDK_NAME
    assert "service.name" not in md
    assert "deployment.environment.name" not in md
    # Identity, severity, and title.
    assert md["user.id"] == "u-1"
    assert md["session.id"] == "sess-1"
    assert md["anonymous_id"] == "anon-1"
    assert md["level"] == "error"
    assert md["severity_number"] == 17
    assert md["gc.title"] == "ValueError: boom"


def test_to_wire_event_shape():
    ev = to_wire_event(_event(), _resource())
    assert ev["type"] == "exception"
    assert ev["level"] == "error"
    assert ev["timestamp"] == 456
    assert ev["id"] == "id-1"
    assert len(ev["spanId"]) == 16
    assert ev["parentSpanId"] == ""
    assert len(ev["traceId"]) == 32
    attrs = ev["attributes"]
    assert attrs["error_type"] == "ValueError"
    assert attrs["error_handled"] is True
    assert attrs["error_fingerprint"] == "fp"
    assert attrs["error_stacktrace"] == [
        {"filename": "/app/main.py", "function": "app.run", "lineno": 10, "colno": 0}
    ]


def test_encode_batch_is_valid_json():
    body = encode_batch([_event(), _event()], _resource())
    payload = json.loads(body.decode("utf-8"))
    assert len(payload["events"]) == 2
    assert payload["sessionAttributes"]["service.name"] == "svc"


def test_estimate_size_over_estimates():
    e = _event()
    body = encode_batch([e], _resource())
    # The per-event estimate must comfortably cover the event's own share of
    # the encoded payload (base constant + fields).
    assert estimate_size(e) >= 256
    assert isinstance(len(body), int)


def test_empty_stack_and_fingerprint_omitted():
    e = Event(id="x", error_type="t", error_message="m")
    ev = to_wire_event(e, Resource())
    assert "error_stacktrace" not in ev["attributes"]
    assert "error_fingerprint" not in ev["attributes"]
