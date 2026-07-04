"""Scope semantics: isolation, cloning, and contextvar propagation."""

from __future__ import annotations

import contextvars
import threading

from groundcover import Level, User
from groundcover._event import Event
from groundcover._scope import Scope, current_scope, ensure_scope, isolated_scope


def test_scope_apply_to_event():
    sc = Scope()
    sc.set_user(User(id="u-1"))
    sc.set_attributes({"a": 1})
    sc.set_attribute("b", 2)
    sc.set_level(Level.WARNING)
    sc.set_fingerprint("fp")
    sc.set_session_id("sess")
    sc.set_anonymous_id("anon")

    e = Event()
    sc.apply_to(e)
    assert e.user.id == "u-1"
    assert e.attributes == {"a": 1, "b": 2}
    assert e.level is Level.WARNING
    assert e.fingerprint == "fp"
    assert e.session_id == "sess"
    assert e.anonymous_id == "anon"


def test_scope_clone_is_deep_for_attributes():
    sc = Scope()
    sc.set_attribute("k", "v")
    clone = sc.clone()
    clone.set_attribute("k", "changed")
    e = Event()
    sc.apply_to(e)
    assert e.attributes["k"] == "v", "mutating a clone must not affect the source"


def test_isolated_scope_does_not_leak():
    outer = ensure_scope()
    outer.set_user(User(id="outer"))

    with isolated_scope() as inner:
        inner.set_user(User(id="inner"))
        assert current_scope() is inner

    assert current_scope() is outer
    e = Event()
    outer.apply_to(e)
    assert e.user.id == "outer", "inner mutations must not leak out"


def test_isolated_scope_inherits_copy_of_current():
    ensure_scope().set_attribute("inherited", "yes")
    with isolated_scope() as inner:
        e = Event()
        inner.apply_to(e)
        assert e.attributes.get("inherited") == "yes"


def test_scope_propagates_to_thread_with_copy_context():
    ensure_scope().set_user(User(id="from-parent"))
    seen = {}

    def work():
        sc = current_scope()
        e = Event()
        if sc is not None:
            sc.apply_to(e)
        seen["user"] = e.user.id

    ctx = contextvars.copy_context()
    t = threading.Thread(target=lambda: ctx.run(work))
    t.start()
    t.join(5.0)
    assert seen["user"] == "from-parent"


def test_fresh_thread_has_no_scope():
    ensure_scope().set_user(User(id="parent-only"))
    seen = {}

    def work():
        seen["scope"] = current_scope()

    t = threading.Thread(target=work)
    t.start()
    t.join(5.0)
    assert seen["scope"] is None, "a fresh thread starts without a scope"
