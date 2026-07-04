"""WSGI middleware that captures unhandled exceptions through groundcover and
seeds a fresh request scope. It depends only on the standard library and the
core SDK. This is the Python analog of the Go SDK's net/http middleware."""

from __future__ import annotations

from typing import Callable, Iterable, Optional

from ._client import Client
from ._client_global import capture_recovered as _global_capture_recovered
from ._event import Attributes
from ._scope import isolated_scope


class GroundcoverMiddleware:
    """Wraps a WSGI application so that each request gets an isolated scope
    and any unhandled exception is captured (as an unhandled error) and then
    re-raised, leaving the host's control flow unchanged.

        app = GroundcoverMiddleware(app)

    Pass ``client`` to route captures to an explicit client instead of the
    module-level global one.
    """

    def __init__(self, app: Callable, client: Optional[Client] = None) -> None:
        self._app = app
        self._client = client

    def __call__(self, environ: dict, start_response: Callable) -> Iterable[bytes]:
        # Seed a fresh, isolated scope for this request. Because the scope is
        # mutable and shared, a handler's set_user/with_scope during the
        # request is visible to the capture below.
        with isolated_scope():
            try:
                return self._app(environ, start_response)
            except Exception as exc:
                self._capture(exc, _request_attributes(environ))
                raise  # re-raise: the WSGI server handles application errors

    def _capture(self, exc: BaseException, attributes: Attributes) -> None:
        if self._client is not None:
            self._client.capture_recovered(exc, attributes=attributes)
            return
        _global_capture_recovered(exc, attributes=attributes)


def _request_attributes(environ: dict) -> Attributes:
    """Attach OTel-style HTTP attributes to the captured event."""
    return {
        "http.request.method": environ.get("REQUEST_METHOD", ""),
        "url.path": environ.get("PATH_INFO", ""),
        "server.address": environ.get("HTTP_HOST", environ.get("SERVER_NAME", "")),
    }
