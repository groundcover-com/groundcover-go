"""Scrubbing PII with before_send and pseudonymizing identity with a hasher.

uv run examples/before_send.py
"""

from __future__ import annotations

import os
import re
from typing import Optional

import groundcover

_TOKEN = re.compile(r"token=\S+")


def scrub(event: groundcover.Event) -> Optional[groundcover.Event]:
    # Drop noisy events entirely by returning None.
    if event.error_type == "example.IgnoredError":
        return None
    # Rewrite anything sensitive; the title/fingerprint are computed after this.
    event.error_message = _TOKEN.sub("token=[redacted]", event.error_message)
    event.attributes.pop("authorization", None)
    return event


def main() -> None:
    groundcover.init(
        dsn=os.environ.get("GC_DSN", "https://example.invalid"),
        ingestion_key=os.environ.get("GC_INGESTION_KEY", ""),
        service_name="example-before-send",
        before_send=scrub,
        hasher=groundcover.HMACHasher(os.environb.get(b"GC_PII_KEY", b"dev-key")),
        debug=True,  # the debug output shows the post-scrub, post-hash event
    )
    try:
        groundcover.set_user(groundcover.User(id="user-42", email="a@b.com"))
        try:
            raise PermissionError("denied for token=super-secret-value")
        except PermissionError as exc:
            groundcover.capture_error(exc, attributes={"authorization": "Bearer xyz"})
    finally:
        groundcover.close(timeout=5.0)


if __name__ == "__main__":
    main()
