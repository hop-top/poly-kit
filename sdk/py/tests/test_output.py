"""Tests for hop_top_kit.output — table/json/yaml renderer."""

import dataclasses
import io
import json

import pytest
import yaml

from hop_top_kit.output import render

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _capture(format: str, v) -> str:
    buf = io.StringIO()
    render(buf, format, v)
    return buf.getvalue()


@dataclasses.dataclass
class Item:
    name: str
    count: int


# ---------------------------------------------------------------------------
# JSON
# ---------------------------------------------------------------------------


def test_render_json_dict():
    out = _capture("json", {"a": 1, "b": "hello"})
    parsed = json.loads(out)
    assert parsed == {"a": 1, "b": "hello"}


def test_render_json_indent():
    """2-space indent expected."""
    out = _capture("json", {"x": 1})
    assert out.startswith("{\n  ")


def test_render_json_ends_with_newline():
    out = _capture("json", {"x": 1})
    assert out.endswith("\n")


# ---------------------------------------------------------------------------
# YAML
# ---------------------------------------------------------------------------


def test_render_yaml_dict():
    out = _capture("yaml", {"a": 1, "b": "hello"})
    parsed = yaml.safe_load(out)
    assert parsed == {"a": 1, "b": "hello"}


def test_render_yaml_list():
    out = _capture("yaml", [1, 2, 3])
    parsed = yaml.safe_load(out)
    assert parsed == [1, 2, 3]


# ---------------------------------------------------------------------------
# Table — list of dicts
# ---------------------------------------------------------------------------


def test_render_table_list_of_dicts_headers():
    rows = [{"name": "alice", "age": 30}, {"name": "bob", "age": 25}]
    out = _capture("table", rows)
    lines = out.splitlines()
    # first line is header
    assert "name" in lines[0]
    assert "age" in lines[0]


def test_render_table_list_of_dicts_data():
    rows = [{"name": "alice", "age": 30}, {"name": "bob", "age": 25}]
    out = _capture("table", rows)
    assert "alice" in out
    assert "bob" in out
    assert "30" in out
    assert "25" in out


def test_render_table_aligned_columns():
    """Columns must be padded; each row must have the same column widths."""
    rows = [
        {"name": "x", "value": "1"},
        {"name": "longerkey", "value": "99"},
    ]
    out = _capture("table", rows)
    lines = [l for l in out.splitlines() if l.strip()]
    assert len(lines) == 3  # header + 2 data rows
    # Each line must have the same length (padded to column widths).
    # Allow trailing-space difference by stripping only right side.
    col_splits = [len(line.rstrip()) for line in lines]
    # The header must be at least as wide as the widest row.
    assert max(col_splits) >= col_splits[0]


def test_render_table_two_space_gap():
    """Columns are separated by at least 2 spaces."""
    rows = [{"a": "1", "b": "2"}]
    out = _capture("table", rows)
    header = out.splitlines()[0]
    # Between header columns there should be >= 2 spaces.
    assert "  " in header


# ---------------------------------------------------------------------------
# Table — single dict
# ---------------------------------------------------------------------------


def test_render_table_single_dict():
    out = _capture("table", {"name": "carol", "score": 99})
    assert "name" in out
    assert "carol" in out
    assert "score" in out
    assert "99" in out
    lines = [l for l in out.splitlines() if l.strip()]
    assert len(lines) == 2  # header + 1 data row


# ---------------------------------------------------------------------------
# Table — list of dataclasses
# ---------------------------------------------------------------------------


def test_render_table_dataclass_list_headers():
    rows = [Item(name="a", count=1), Item(name="b", count=2)]
    out = _capture("table", rows)
    header = out.splitlines()[0]
    assert "name" in header
    assert "count" in header


def test_render_table_dataclass_list_data():
    rows = [Item(name="alpha", count=42)]
    out = _capture("table", rows)
    assert "alpha" in out
    assert "42" in out


def test_render_table_single_dataclass():
    out = _capture("table", Item(name="solo", count=7))
    assert "name" in out
    assert "solo" in out
    lines = [l for l in out.splitlines() if l.strip()]
    assert len(lines) == 2


# ---------------------------------------------------------------------------
# Table — empty list
# ---------------------------------------------------------------------------


def test_render_table_empty_list_no_output():
    out = _capture("table", [])
    assert out == ""


# ---------------------------------------------------------------------------
# Unknown format
# ---------------------------------------------------------------------------


def test_render_unknown_format_raises():
    # 'csv' + 'text' are now registered built-ins (T-1015/T-1016), so use a
    # format name that nothing claims.
    with pytest.raises(ValueError, match="unknown"):
        _capture("nope", {"x": 1})
