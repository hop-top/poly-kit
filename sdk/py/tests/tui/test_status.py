"""Tests for hop_top_kit.tui.status."""

from __future__ import annotations

import re

import pytest

from hop_top_kit.cli import _build_theme
from hop_top_kit.tui.status import _SYMBOLS, status

_ANSI = re.compile(r"\x1b\[[0-9;]*m")


def strip(s: str) -> str:
    return _ANSI.sub("", s)


@pytest.mark.parametrize("kind,symbol", list(_SYMBOLS.items()))
def test_status_prefix_symbol(kind, symbol):
    theme = _build_theme()
    out = status(theme, "msg", kind=kind)
    plain = strip(out)
    assert symbol in plain


@pytest.mark.parametrize("kind", ["info", "success", "error", "warn"])
def test_status_contains_message(kind):
    theme = _build_theme()
    out = status(theme, "hello world", kind=kind)
    assert "hello world" in strip(out)


def test_status_default_kind_is_info():
    theme = _build_theme()
    out = status(theme, "default")
    plain = strip(out)
    assert _SYMBOLS["info"] in plain


def test_status_unknown_kind_falls_back_to_info():
    theme = _build_theme()
    out = status(theme, "oops", kind="unknown")
    plain = strip(out)
    assert _SYMBOLS["info"] in plain


def test_status_is_styled():
    theme = _build_theme()
    out = status(theme, "styled")
    assert "\x1b[" in out
