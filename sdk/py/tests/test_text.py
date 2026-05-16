"""Tests for hop_top_kit.output.formatters.text_formatter."""

from __future__ import annotations

import io

import pytest

from hop_top_kit.output import default_registry
from hop_top_kit.output.formatters.text_formatter import TextFormatter


def _render(data, opts=None, cols=None) -> str:
    buf = io.StringIO()
    TextFormatter().render(buf, data, opts or {"style": "kv", "separator": "="}, cols or [])
    return buf.getvalue()


# ---------------------------------------------------------------------------
# kv style
# ---------------------------------------------------------------------------


def test_text_kv_default():
    out = _render([{"a": "1", "b": "2"}])
    assert out == "a=1\nb=2\n"


def test_text_kv_separates_records_with_blank_line():
    out = _render([{"a": "1"}, {"a": "2"}])
    assert out == "a=1\n\na=2\n"


def test_text_kv_custom_separator():
    out = _render(
        [{"a": "1", "b": "2"}],
        opts={"style": "kv", "separator": ": "},
    )
    assert out == "a: 1\nb: 2\n"


# ---------------------------------------------------------------------------
# lines style
# ---------------------------------------------------------------------------


def test_text_lines_tab_joined():
    out = _render(
        [{"a": "1", "b": "two"}, {"a": "3", "b": "four"}],
        opts={"style": "lines"},
    )
    assert out == "1\ttwo\n3\tfour\n"


def test_text_lines_single_row():
    out = _render([{"a": "1"}], opts={"style": "lines"})
    assert out == "1\n"


# ---------------------------------------------------------------------------
# paragraph style
# ---------------------------------------------------------------------------


def test_text_paragraph_basic():
    out = _render(
        [{"a": "1", "b": "2"}],
        opts={"style": "paragraph"},
    )
    assert out == "Record 1:\n  a: 1\n  b: 2\n"


def test_text_paragraph_multiple_records():
    out = _render(
        [{"a": "1"}, {"a": "2"}],
        opts={"style": "paragraph"},
    )
    assert out == "Record 1:\n  a: 1\n\nRecord 2:\n  a: 2\n"


# ---------------------------------------------------------------------------
# cols filter + edge cases
# ---------------------------------------------------------------------------


def test_text_kv_cols_filter():
    out = _render(
        [{"a": "1", "b": "2", "c": "3"}],
        opts={"style": "kv", "separator": "="},
        cols=["c", "a"],
    )
    assert out == "c=3\na=1\n"


def test_text_empty_list_no_output():
    assert _render([]) == ""


def test_text_unknown_style_raises():
    with pytest.raises(ValueError, match="unknown style"):
        _render([{"a": "1"}], opts={"style": "weird"})


def test_text_registered_with_extension():
    f = default_registry.lookup("text")
    assert f is not None
    assert ".txt" in f.extensions
    em = default_registry.extension_map()
    assert em[".txt"] == "text"
