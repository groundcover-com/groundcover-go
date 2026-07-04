"""Shared test seams: an injectable mock sender and scope isolation."""

from __future__ import annotations

import json
import threading

import pytest

from groundcover import Client, Config
from groundcover._scope import _scope_var


class MockSender:
    """An in-memory Sender. It records every body it receives and can be
    programmed to raise errors via ``responder(call, body)`` (zero-based call
    index)."""

    def __init__(self, responder=None):
        self._lock = threading.Lock()
        self.responder = responder
        self._calls = 0
        self._bodies = []

    def send(self, body: bytes) -> None:
        with self._lock:
            call = self._calls
            self._calls += 1
            self._bodies.append(bytes(body))
            responder = self.responder
        if responder is not None:
            responder(call, body)

    @property
    def calls(self) -> int:
        with self._lock:
            return self._calls

    def bodies(self):
        with self._lock:
            return list(self._bodies)


@pytest.fixture(autouse=True)
def _isolate_scope():
    """Reset the contextvar-carried scope around every test so request scope
    never leaks between tests."""
    token = _scope_var.set(None)
    try:
        yield
    finally:
        _scope_var.reset(token)


@pytest.fixture()
def sender():
    return MockSender()


def make_client(sender, **config_kwargs) -> Client:
    config_kwargs.setdefault("dsn", "https://example.invalid")
    return Client(Config(**config_kwargs), _sender=sender)


@pytest.fixture()
def client_factory(sender):
    """Return a factory building clients wired to the mock sender; every
    created client is closed at teardown."""
    created = []

    def _make(**config_kwargs) -> Client:
        c = make_client(sender, **config_kwargs)
        created.append(c)
        return c

    yield _make
    for c in created:
        c.close(5.0)


def decode_payload(sender: MockSender) -> dict:
    """Unmarshal the last batch body the mock sender received."""
    bodies = sender.bodies()
    assert bodies, "no body was sent"
    return json.loads(bodies[-1].decode("utf-8"))
