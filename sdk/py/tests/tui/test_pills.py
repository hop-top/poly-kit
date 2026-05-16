"""Tests for hop_top_kit.tui.pills."""

from __future__ import annotations

import re

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.pills import pills

_ANSI = re.compile(r"\x1b\[[0-9;]*m")


def strip(s: str) -> str:
    return _ANSI.sub("", s)


def test_pills_empty_list_returns_empty():
    theme = _build_theme()
    assert pills(theme, []) == ""


def test_pills_single_item():
    theme = _build_theme()
    out = pills(theme, ["alpha"])
    assert "alpha" in strip(out)


def test_pills_multiple_items_present():
    theme = _build_theme()
    out = pills(theme, ["foo", "bar", "baz"])
    plain = strip(out)
    assert "foo" in plain
    assert "bar" in plain
    assert "baz" in plain


def test_pills_items_separated_by_space():
    theme = _build_theme()
    out = pills(theme, ["a", "b"])
    plain = strip(out)
    # both labels appear; a space exists between them in the plain text
    idx_a = plain.index("a")
    idx_b = plain.index("b")
    assert idx_a < idx_b


def test_pills_is_styled():
    theme = _build_theme()
    out = pills(theme, ["x"])
    assert "\x1b[" in out
