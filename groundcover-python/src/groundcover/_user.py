"""The identity associated with a captured event."""

from __future__ import annotations

import dataclasses


@dataclasses.dataclass
class User:
    """Identifies the principal associated with an event.

    ``organization`` is the B2B group key used for attribution; it has no OTel
    convention and is a groundcover extension.
    """

    id: str = ""
    """A stable user identifier."""
    email: str = ""
    """The user's email address."""
    name: str = ""
    """A human-readable user name."""
    organization: str = ""
    """The B2B group/tenant key."""

    def is_zero(self) -> bool:
        """Report whether the user carries no information."""
        return not (self.id or self.email or self.name or self.organization)

    def copy(self) -> User:
        """Return a shallow copy of the user."""
        return dataclasses.replace(self)
