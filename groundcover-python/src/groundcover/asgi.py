"""ASGI middleware that captures unhandled exceptions through groundcover and
seeds a fresh request scope. It depends only on the standard library and the
core SDK. Scope isolation relies on contextvars, which propagate naturally
through async tasks."""

from __future__ import annotations

from typing import Callable, Optional

from ._client import Client
from ._client_global import capture_recovered as _global_capture_recovered
from ._event import Attributes
from ._scope import isolated_scope


class GroundcoverASGIMiddleware:
    """Wraps an ASGI application so that each HTTP request gets an isolated
    scope and any unhandled exception is captured (as an unhandled error) and
    then re-raised, leaving the host's control flow unchanged.

        app = GroundcoverASGIMiddleware(app)

    Pass ``client`` to route captures to an explicit client instead of the
    module-level global one.
    """

    def __init__(self, app: Callable, client: Optional[Client] = None) -> None:
        self._app = app
        self._client = client

    async def __call__(self, scope: dict, receive: Callable, send: Callable) -> None:
        if scope.get("type") != "http":
            await self._app(scope, receive, send)
            return
        with isolated_scope():
            try:
                await self._app(scope, receive, send)
            except Exception as exc:
                self._capture(exc, _request_attributes(scope))
                raise  # re-raise: the ASGI server handles application errors

    def _capture(self, exc: BaseException, attributes: Attributes) -> None:
        if self._client is not None:
            self._client.capture_recovered(exc, attributes=attributes)
            return
        _global_capture_recovered(exc, attributes=attributes)


def _request_attributes(scope: dict) -> Attributes:
    """Attach OTel-style HTTP attributes to the captured event."""
    host = ""
    for name, value in scope.get("headers") or []:
        if name == b"host":
            host = value.decode("latin-1")
            break
    return {
        "http.request.method": scope.get("method", ""),
        "url.path": scope.get("path", ""),
        "server.address": host,
    }
