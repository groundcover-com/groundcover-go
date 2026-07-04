"""Minimal end-to-end usage: init, capture, flush.

Run with real credentials to submit an event:

    GC_DSN=https://<tenant>.platform.grcv.io \
    GC_INGESTION_KEY=<rum-key> \
    uv run examples/basic.py
"""

from __future__ import annotations

import os

import groundcover


def charge(order_id: str) -> None:
    raise ConnectionError(f"charge failed for {order_id}: connection refused")


def main() -> None:
    groundcover.init(
        dsn=os.environ.get("GC_DSN", "https://example.invalid"),
        ingestion_key=os.environ.get("GC_INGESTION_KEY", ""),
        service_name="example-basic",
        debug=True,  # print captured events to stderr
    )
    try:
        groundcover.set_user(groundcover.User(id="u-123", organization="acme"))
        try:
            charge("o-9")
        except ConnectionError as exc:
            groundcover.capture_error(exc, attributes={"order_id": "o-9", "amount": 42.5})
            # control flow unchanged: handle/re-raise as you normally would
        groundcover.capture_message("falling back to stale cache", groundcover.Level.WARNING)
    finally:
        groundcover.close(timeout=5.0)


if __name__ == "__main__":
    main()
