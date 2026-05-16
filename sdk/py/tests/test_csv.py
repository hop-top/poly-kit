"""Tests for hop_top_kit.output.formatters.csv_formatter."""

from __future__ import annotations

import dataclasses
import io

import pytest

from hop_top_kit.output import default_registry
from hop_top_kit.output.formatters.csv_formatter import CSVFormatter


@dataclasses.dataclass
class _Row:
    name: str
    count: int


def _render(data, opts=None, cols=None) -> str:
    buf = io.StringIO()
    CSVFormatter().render(buf, data, opts or {"delimiter": ","}, cols or [])
    return buf.getvalue()


def test_csv_basic_dicts():
    out = _render([{"a": "1", "b": "2"}, {"a": "3", "b": "4"}])
    assert out == "a,b\n1,2\n3,4\n"


def test_csv_dataclass():
    out = _render([_Row(name="x", count=1), _Row(name="y", count=2)])
    assert out == "name,count\nx,1\ny,2\n"


def test_csv_empty_list_no_output():
    assert _render([]) == ""


def test_csv_delimiter_override():
    out = _render(
        [{"a": "1", "b": "2"}],
        opts={"delimiter": ";"},
    )
    assert out == "a;b\n1;2\n"


def test_csv_no_header():
    out = _render(
        [{"a": "1", "b": "2"}],
        opts={"delimiter": ",", "no-header": True},
    )
    assert out == "1,2\n"


def test_csv_quote_all():
    out = _render(
        [{"a": "1", "b": "two"}],
        opts={"delimiter": ",", "quote-all": True},
    )
    assert out == '"a","b"\n"1","two"\n'


def test_csv_quote_all_escapes_internal_quotes():
    out = _render(
        [{"x": 'he said "hi"'}],
        opts={"delimiter": ",", "quote-all": True},
    )
    assert out == '"x"\n"he said ""hi"""\n'


def test_csv_crlf_line_endings():
    out = _render(
        [{"a": "1"}],
        opts={"delimiter": ",", "crlf": True},
    )
    assert out == "a\r\n1\r\n"


def test_csv_cols_subset():
    out = _render(
        [{"a": "1", "b": "2", "c": "3"}],
        opts={"delimiter": ","},
        cols=["c", "a"],
    )
    assert out == "c,a\n3,1\n"


def test_csv_cols_unknown_raises():
    with pytest.raises(ValueError, match="unknown column 'mystery'"):
        _render(
            [{"a": "1"}],
            opts={"delimiter": ","},
            cols=["mystery"],
        )


def test_csv_invalid_delimiter_length():
    with pytest.raises(ValueError, match="exactly one character"):
        _render([{"a": "1"}], opts={"delimiter": "||"})


def test_csv_registered_with_extension():
    f = default_registry.lookup("csv")
    assert f is not None
    assert ".csv" in f.extensions
    em = default_registry.extension_map()
    assert em[".csv"] == "csv"
