"""WSGI / ASGI middleware tests, ported from the Go SDK's nethttp middleware
tests."""

from __future__ import annotations

import asyncio

import pytest

from groundcover import User
from groundcover.asgi import GroundcoverASGIMiddleware
from groundcover.wsgi import GroundcoverMiddleware

from .conftest import decode_payload


def _environ(path="/boom", method="GET", host="example.com"):
    return {"REQUEST_METHOD": method, "PATH_INFO": path, "HTTP_HOST": host}


def _start_response(_status, _headers):
    pass


def test_wsgi_captures_unhandled_exception_and_reraises(sender, client_factory):
    client = client_factory()

    def app(_environ, _start_response):
        raise RuntimeError("handler exploded")

    wrapped = GroundcoverMiddleware(app, client=client)
    with pytest.raises(RuntimeError, match="handler exploded"):
        wrapped(_environ(), _start_response)
    client.flush(5.0)

    ev = decode_payload(sender)["events"][0]
    assert ev["attributes"]["error_handled"] is False
    assert ev["level"] == "fatal"
    md = ev["attributes"]["error_metadata"]
    assert md["http.request.method"] == "GET"
    assert md["url.path"] == "/boom"
    assert md["server.address"] == "example.com"


def test_wsgi_healthy_request_untouched(sender, client_factory):
    client = client_factory()

    def app(_environ, start_response):
        start_response("200 OK", [])
        return [b"ok"]

    wrapped = GroundcoverMiddleware(app, client=client)
    assert wrapped(_environ("/ok"), _start_response) == [b"ok"]
    assert client.stats().captured == 0


def test_wsgi_scope_isolated_per_request(sender, client_factory):
    client = client_factory()

    def app(environ, start_response):
        # A previous request's identity must not be visible here.
        if environ["PATH_INFO"] == "/second":
            client.capture_error(ValueError("second"))
        else:
            client.set_user(User(id="first-request-user"))
        start_response("200 OK", [])
        return [b"ok"]

    wrapped = GroundcoverMiddleware(app, client=client)
    wrapped(_environ("/first"), _start_response)
    wrapped(_environ("/second"), _start_response)
    client.flush(5.0)

    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert "user.id" not in md, "identity leaked across requests"


def test_wsgi_handler_scope_visible_at_capture(sender, client_factory):
    client = client_factory()

    def app(_environ, _start_response):
        client.set_user(User(id="handler-user"))
        raise RuntimeError("after set_user")

    wrapped = GroundcoverMiddleware(app, client=client)
    with pytest.raises(RuntimeError):
        wrapped(_environ(), _start_response)
    client.flush(5.0)

    md = decode_payload(sender)["events"][0]["attributes"]["error_metadata"]
    assert md["user.id"] == "handler-user"


def _http_scope(path="/boom", method="GET", host=b"example.com"):
    return {"type": "http", "method": method, "path": path, "headers": [(b"host", host)]}


async def _receive():
    return {"type": "http.request"}


async def _send(_message):
    pass


def test_asgi_captures_unhandled_exception_and_reraises(sender, client_factory):
    client = client_factory()

    async def app(_scope, _receive, _send):
        raise RuntimeError("async handler exploded")

    wrapped = GroundcoverASGIMiddleware(app, client=client)
    with pytest.raises(RuntimeError, match="async handler exploded"):
        asyncio.run(wrapped(_http_scope(), _receive, _send))
    client.flush(5.0)

    ev = decode_payload(sender)["events"][0]
    assert ev["attributes"]["error_handled"] is False
    md = ev["attributes"]["error_metadata"]
    assert md["url.path"] == "/boom"
    assert md["server.address"] == "example.com"


def test_asgi_non_http_scope_passthrough(sender, client_factory):
    client = client_factory()
    seen = {}

    async def app(scope, _receive, _send):
        seen["type"] = scope["type"]

    wrapped = GroundcoverASGIMiddleware(app, client=client)
    asyncio.run(wrapped({"type": "lifespan"}, _receive, _send))
    assert seen["type"] == "lifespan"
    assert client.stats().captured == 0
