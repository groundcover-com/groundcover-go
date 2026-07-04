"""Identity pseudonymization at the SDK boundary."""

from __future__ import annotations

import hashlib
import hmac
from typing import Protocol, runtime_checkable


@runtime_checkable
class IdentityHasher(Protocol):
    """Pseudonymizes identity fields (user.id / user.email) at the SDK
    boundary.

    Implementations should use a keyed function (e.g. HMAC), not a plain hash,
    so values cannot be trivially reversed via a dictionary.
    """

    def hash_identity(self, value: str) -> str:
        """Return the pseudonymized form of value. An empty input must map to
        an empty output."""
        ...


class HMACHasher:
    """A keyed HMAC-SHA256 IdentityHasher."""

    def __init__(self, key: bytes) -> None:
        """Return an HMACHasher keyed with the given secret."""
        self._key = bytes(key)

    def hash_identity(self, value: str) -> str:
        """Return the hex-encoded HMAC-SHA256 of value, or "" for ""."""
        if not value:
            return ""
        return hmac.new(self._key, value.encode("utf-8"), hashlib.sha256).hexdigest()
