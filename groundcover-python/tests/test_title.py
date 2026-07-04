"""Display title tests, ported from the Go SDK's title_test.go."""

from __future__ import annotations

from groundcover._event import Event
from groundcover._title import (
    MAX_TITLE_LEN,
    MESSAGE_ERROR_TYPE,
    collapse_whitespace,
    title_for,
    truncate_title,
)


def test_title_for_error():
    e = Event(error_type="ValueError", error_message="boom")
    assert title_for(e) == "ValueError: boom"


def test_title_for_message_event_uses_bare_message():
    e = Event(error_type=MESSAGE_ERROR_TYPE, error_message="stale cache")
    assert title_for(e) == "stale cache"


def test_title_for_empty_message_uses_type():
    e = Event(error_type="ValueError", error_message="")
    assert title_for(e) == "ValueError"


def test_title_collapses_whitespace():
    e = Event(error_type="E", error_message="line one\n\tline   two")
    assert title_for(e) == "E: line one line two"


def test_title_truncated():
    e = Event(error_type="E", error_message="x" * 1000)
    t = title_for(e)
    assert len(t) == MAX_TITLE_LEN
    assert t.endswith("\u2026")


def test_collapse_whitespace():
    assert collapse_whitespace("  a\n b\t\tc  ") == "a b c"
    assert collapse_whitespace("") == ""


def test_truncate_title():
    assert truncate_title("short", 10) == "short"
    assert truncate_title("exactly10!", 10) == "exactly10!"
    assert truncate_title("elevenchars", 10) == "elevencha\u2026"
    assert truncate_title("xy", 1) == "\u2026"
    assert truncate_title("anything", 0) == "anything"
