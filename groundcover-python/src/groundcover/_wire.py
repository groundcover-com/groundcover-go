"""Wire encoding: the RUM ingestion JSON body."""

from __future__ import annotations

import json
from typing import Any, Dict, List, Mapping

from ._event import Attributes, Event
from ._resource import (
    ATTR_DEPLOY_ENV,
    ATTR_K8S_CLUSTER,
    ATTR_K8S_NAMESPACE,
    ATTR_SERVICE_NAME,
    ATTR_SERVICE_VER,
    Resource,
)
from ._uuid import new_span_id, new_trace_id
from ._version import SDK_NAME, VERSION

SCALAR_SIZE_ESTIMATE = 16
"""The assumed byte cost of a non-string scalar attribute value (number/bool)
in the buffer's byte budget."""

_SESSION_LEVEL_KEYS = frozenset(
    {ATTR_SERVICE_NAME, ATTR_SERVICE_VER, ATTR_DEPLOY_ENV, ATTR_K8S_NAMESPACE, ATTR_K8S_CLUSTER}
)

_SDK_MANAGED_KEYS = frozenset(
    {
        "user.id",
        "user.email",
        "user.name",
        "user.organization",
        "session.id",
        "anonymous_id",
        "level",
        "severity_number",
        "gc.title",
    }
)


def user_agent() -> str:
    """Return the SDK identifier sent on the wire."""
    return f"{SDK_NAME}/{VERSION}"


def session_attributes(res: Resource) -> Dict[str, Any]:
    """Build the per-batch resource spine. These keys are first-class on the
    RUM ingestion endpoint and become queryable top-level fields
    (service.name, env, namespace, cluster, releaseId)."""
    return {
        ATTR_SERVICE_NAME: res.service_name,
        "env": res.env,
        "namespace": res.namespace,
        "cluster": res.cluster,
        "releaseId": res.release,
        "session_start_time": res.start_time_ns,
        "userAgent": user_agent(),
    }


def is_session_level_key(k: str) -> bool:
    """Report whether a resource attribute already travels in
    session_attributes and so should be omitted from per-event error_metadata
    to avoid redundancy."""
    return k in _SESSION_LEVEL_KEYS


def is_sdk_managed_key(k: str) -> bool:
    """Report whether a metadata key is owned by the SDK and must not be set
    or overridden by caller attributes."""
    return k in _SDK_MANAGED_KEYS


def build_metadata(e: Event, res: Resource) -> Dict[str, Any]:
    """Assemble the error_metadata bag for a single event: identity, detailed
    resource attributes, severity, and custom attributes. On the RUM endpoint
    this is the only durable custom bag (top-level custom attributes are
    dropped), so all queryable custom data is nested here.

    Caller attributes are written first; SDK-managed keys (resource, identity,
    severity, gc.title) are written afterwards so a caller cannot override
    them — e.g. inject a raw, unhashed user.id or a non-numeric
    severity_number. The event's attributes are expected to be sanitized
    already (done at capture).
    """
    md: Dict[str, Any] = {}

    # Caller-supplied custom attributes, minus the reserved SDK-managed keys.
    for k, v in e.attributes.items():
        if is_sdk_managed_key(k):
            continue
        md[k] = v

    # Detailed resource attributes (telemetry.sdk.*, process.*, host.*, k8s.pod.*).
    for k, v in res.attrs.items():
        if is_session_level_key(k):
            continue
        md[k] = v

    # Identity as dotted keys.
    _set_if_non_empty(md, "user.id", e.user.id)
    _set_if_non_empty(md, "user.email", e.user.email)
    _set_if_non_empty(md, "user.name", e.user.name)
    _set_if_non_empty(md, "user.organization", e.user.organization)
    _set_if_non_empty(md, "session.id", e.session_id)
    _set_if_non_empty(md, "anonymous_id", e.anonymous_id)

    # Severity (always numeric severity_number + string level).
    md["level"] = e.level.value
    md["severity_number"] = e.level.severity_number

    # Reserved gc.* namespace: the human-readable display title (separate
    # from the opaque error_fingerprint grouping key).
    if e.title:
        md["gc.title"] = e.title
    return md


def _set_if_non_empty(m: Dict[str, Any], k: str, v: str) -> None:
    if v:
        m[k] = v


def to_wire_event(e: Event, res: Resource) -> Dict[str, Any]:
    """Convert an internal Event to its wire representation."""
    frames = [
        {"filename": f.file, "function": f.function, "lineno": f.line, "colno": 0}
        for f in e.stacktrace
    ]
    attributes: Dict[str, Any] = {
        "error_type": e.error_type,
        "error_message": e.error_message,
        "error_handled": e.error_handled,
    }
    if frames:
        attributes["error_stacktrace"] = frames
    if e.fingerprint:
        attributes["error_fingerprint"] = e.fingerprint
    metadata = build_metadata(e, res)
    if metadata:
        attributes["error_metadata"] = metadata
    return {
        "type": e.type,
        "level": e.level.value,
        "timestamp": e.timestamp_ns,
        "id": e.id,
        "spanId": new_span_id(),
        "parentSpanId": "",
        "traceId": new_trace_id(),
        "attributes": attributes,
    }


def encode_batch(events: List[Event], res: Resource) -> bytes:
    """Serialize a batch of events into the RUM ingestion body."""
    payload = {
        "sessionAttributes": session_attributes(res),
        "events": [to_wire_event(e, res) for e in events],
    }
    return json.dumps(payload, separators=(",", ":"), default=str).encode("utf-8")


def estimate_size(e: Event) -> int:
    """Return a cheap byte estimate of an event for the buffer's byte budget.
    It intentionally over- rather than under-estimates."""
    base = 256
    per_frame_overhead = 24
    size = (
        base
        + len(e.type)
        + len(e.error_type)
        + len(e.error_message)
        + len(e.fingerprint)
        + len(e.title)
    )
    for f in e.stacktrace:
        size += len(f.function) + len(f.file) + per_frame_overhead
    for k, v in e.attributes.items():
        size += len(k) + _estimate_value_size(v)
    size += len(e.user.id) + len(e.user.email) + len(e.user.name) + len(e.user.organization)
    return size


def _estimate_value_size(v: Any) -> int:
    if isinstance(v, str):
        return len(v)
    if v is None:
        return SCALAR_SIZE_ESTIMATE
    if isinstance(v, Mapping):
        return _estimate_map_size(v)
    if isinstance(v, (list, tuple)):
        return 2 + sum(_estimate_value_size(item) for item in v)
    return SCALAR_SIZE_ESTIMATE


def _estimate_map_size(m: Attributes) -> int:
    total = 2
    for k, item in m.items():
        total += len(str(k)) + _estimate_value_size(item)
    return total
