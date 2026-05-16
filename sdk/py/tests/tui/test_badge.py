"""Tests for hop_top_kit.tui.badge."""

from __future__ import annotations

import re

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.badge import badge

_ANSI = re.compile(r"\x1b\[[0-9;]*m")


def strip(s: str) -> str:
    return _ANSI.sub("", s)


def test_badge_contains_text():
    theme = _build_theme()
    out = badge(theme, "UPDATE")
    assert "UPDATE" in strip(out)


def test_badge_default_color_uses_accent():
    """Output should contain ANSI codes (styled, not plain)."""
    theme = _build_theme()
    out = badge(theme, "v1.2.3")
    # styled output always has at least one ESC sequence
    assert "\x1b[" in out


def test_badge_custom_color():
    theme = _build_theme()
    out = badge(theme, "label", color="#FF0000")
    assert "label" in strip(out)
    assert "\x1b[" in out


def test_badge_empty_text():
    theme = _build_theme()
    out = badge(theme, "")
    # should not raise; text body may be empty but output is still styled
    assert isinstance(out, str)


def test_badge_whitespace_padding():
    """Badge adds a space on each side of the text."""
    theme = _build_theme()
    out = badge(theme, "X")
    plain = strip(out)
    assert " X " in plain
