"""Client configuration."""

from __future__ import annotations

import dataclasses
from typing import TYPE_CHECKING, Callable, Optional

if TYPE_CHECKING:
    from ._event import Event
    from ._hasher import IdentityHasher
    from ._logger import Logger

# Default configuration values.
DEFAULT_MAX_QUEUE = 10_000
DEFAULT_MAX_BYTES = 32 * 1024 * 1024  # 32 MiB
DEFAULT_BATCH_SIZE = 250
DEFAULT_FLUSH_INTERVAL = 5.0
DEFAULT_MAX_BATCH_BYTES = 512 * 1024  # 512 KiB
DEFAULT_MAX_RETRIES = 3
DEFAULT_RETRY_MAX = 30.0
DEFAULT_RATE_LIMIT_BACKOFF = 30.0
DEFAULT_STACK_DEPTH_MAX = 128
DEFAULT_HTTP_TIMEOUT = 30.0

INGEST_PATH = "/json/rum"
"""The SDK-owned path appended to the DSN. v1 targets the RUM ingestion
endpoint; a dedicated errors endpoint will replace it with no user-facing
change."""


class MissingDSNError(ValueError):
    """Raised by init()/Client() when no DSN is configured on an enabled
    client."""

    def __init__(self) -> None:
        super().__init__("groundcover: DSN is required (set Config.dsn) unless disabled is True")


@dataclasses.dataclass
class Config:
    """Configures a Client. The zero value is only valid when disabled is True.

    Most callers set only ``dsn`` and ``ingestion_key``; every other field has
    a sensible default and is a tuning knob you can ignore until you need it.
    Durations are seconds (float); sizes are bytes.
    """

    dsn: str = ""
    """The base ingestion origin (e.g. ``https://<tenant>.platform.grcv.io``
    for BYOC). The SDK owns the path and appends ``/json/rum``. A missing
    scheme defaults to https. Find it in the groundcover UI under
    Settings -> Access -> Ingestion Keys."""

    ingestion_key: str = ""
    """The write key sent as ``Authorization: Bearer <key>``. It is REQUIRED
    when posting directly to a cloud/BYOC ingestion origin (omitting it yields
    silent 401s, since capture never raises at the call site). It is optional
    ONLY when dsn points at a local in-cluster sensor, which needs no auth.
    Use a RUM-type ingestion key."""

    service_name: str = ""
    """The service identity (OpenTelemetry service.name; the "service" in
    Datadog/OTel terms). Auto-detected from OTEL_SERVICE_NAME /
    GC_SERVICE_NAME when unset. In Kubernetes you can usually leave this empty
    and let the groundcover sensor enrich pod -> workload server-side."""

    env: str = ""
    """Overrides deployment.environment.name (env: GC_ENV /
    DEPLOYMENT_ENVIRONMENT)."""

    release: str = ""
    """Overrides service.version (env: GC_RELEASE)."""

    max_queue: int = 0
    """Bounds the pending buffer by item count (default 10000)."""

    max_bytes: int = 0
    """Bounds the pending buffer by estimated bytes (default 32 MiB)."""

    batch_size: int = 0
    """The maximum number of events per request (default 250)."""

    flush_interval: float = 0.0
    """The periodic flush cadence in seconds (default 5)."""

    max_batch_bytes: int = 0
    """The maximum estimated request size (default 512 KiB)."""

    max_retries: int = 0
    """The retry attempt cap after the first try (default 3; negative means
    no retries)."""

    retry_max: float = 0.0
    """Caps exponential backoff in seconds (default 30)."""

    rate_limit_backoff: float = 0.0
    """The minimum 429 backoff in seconds (default 30)."""

    stack_depth_max: int = 0
    """Caps captured stack frames (default 128)."""

    on_drop: Optional[Callable[[int], None]] = None
    """Observes dropped events (also recorded as a self-metric)."""

    before_send: Optional[Callable[[Event], Optional[Event]]] = None
    """Can scrub or sample an event; returning None drops it."""

    hasher: Optional[IdentityHasher] = None
    """Optionally pseudonymizes user.id / user.email via keyed HMAC."""

    logger: Optional[Logger] = None
    """Receives throttled SDK-internal logs."""

    disabled: bool = False
    """Makes the client a no-op with near-zero overhead."""

    debug: bool = False
    """Prints each captured event to stderr in a compact, readable form (after
    scrubbing/hashing), for local development. It does not affect delivery.
    Leave off in production."""

    in_app_root: str = ""
    """The filesystem path under which stack frames are classified as in-app
    (application code, not libraries). Auto-detected from the ``__main__``
    module's location when unset. This is the Python analog of the Go SDK's
    main-module detection."""

    http_timeout: float = 0.0
    """The per-request HTTP timeout in seconds (default 30). Primarily a
    tuning/test knob."""

    def with_defaults(self) -> Config:
        """Return a copy of the config with zero fields replaced by
        defaults."""
        c = dataclasses.replace(self)
        if c.max_queue <= 0:
            c.max_queue = DEFAULT_MAX_QUEUE
        if c.max_bytes <= 0:
            c.max_bytes = DEFAULT_MAX_BYTES
        if c.batch_size <= 0:
            c.batch_size = DEFAULT_BATCH_SIZE
        if c.flush_interval <= 0:
            c.flush_interval = DEFAULT_FLUSH_INTERVAL
        if c.max_batch_bytes <= 0:
            c.max_batch_bytes = DEFAULT_MAX_BATCH_BYTES
        if c.max_retries < 0:
            c.max_retries = 0
        elif c.max_retries == 0:
            c.max_retries = DEFAULT_MAX_RETRIES
        if c.retry_max <= 0:
            c.retry_max = DEFAULT_RETRY_MAX
        if c.rate_limit_backoff <= 0:
            c.rate_limit_backoff = DEFAULT_RATE_LIMIT_BACKOFF
        if c.stack_depth_max <= 0:
            c.stack_depth_max = DEFAULT_STACK_DEPTH_MAX
        if c.http_timeout <= 0:
            c.http_timeout = DEFAULT_HTTP_TIMEOUT
        return c

    def validate(self) -> None:
        """Check the configuration; raise MissingDSNError if it is unusable."""
        if self.disabled:
            return
        if not self.dsn.strip():
            raise MissingDSNError()

    def endpoint(self) -> str:
        """Return the fully-qualified ingestion URL (dsn + ingest path),
        adding a default https scheme when the DSN omits one."""
        dsn = self.dsn.strip().rstrip("/")
        if dsn and not dsn.startswith(("http://", "https://")):
            dsn = "https://" + dsn
        return dsn + INGEST_PATH
