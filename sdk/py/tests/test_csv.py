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


# --- CR/LF preservation -------------------------------------------------

# The adversarial row that pins csv quoting rules byte-for-byte. Every field
# is a separate hazard: the delimiter, an internal quote, an embedded LF, a
# leading space, a trailing space, empty, a tab, and a LONE CR.
_ADVERSARIAL = {
    "a": "plain",
    "b": "with,comma",
    "c": 'with"quote',
    "d": "with\nnewline",
    "e": " leading space",
    "f": "trailing ",
    "g": "",
    "h": "with\ttab",
    "i": "with\rcr",
}

_ADVERSARIAL_VALUES = [
    "plain",
    "with,comma",
    'with"quote',
    "with\nnewline",
    " leading space",
    "trailing ",
    "",
    "with\ttab",
    "with\rcr",
]


@pytest.mark.parametrize(
    ("label", "opts"),
    [
        ("lf", {"delimiter": ",", "no-header": True}),
        ("crlf", {"delimiter": ",", "no-header": True, "crlf": True}),
        ("lf/quote-all", {"delimiter": ",", "no-header": True, "quote-all": True}),
        (
            "crlf/quote-all",
            {"delimiter": ",", "no-header": True, "quote-all": True, "crlf": True},
        ),
    ],
)
def test_csv_preserves_cr_and_lf_verbatim(label, opts):
    """A field holding CR and/or LF is quoted and its bytes survive verbatim.

    RFC 4180 lists CR and LF as separate alternatives inside the ``escaped``
    production, so a bare CR between quotes is legal. Emitting it UNQUOTED,
    as the stdlib writer does, produces CSV that python's own reader refuses
    to parse or silently truncates.
    """
    out = _render([_ADVERSARIAL], opts=opts)
    assert '"with\rcr"' in out, f"{label}: lone CR must be quoted and preserved"
    assert '"with\nnewline"' in out, f"{label}: in-field LF must stay LF"
    assert '" leading space"' in out, f"{label}: leading space must be quoted"


@pytest.mark.parametrize(
    ("label", "opts", "delim"),
    [
        ("lf", {"delimiter": ",", "no-header": True}, ","),
        ("crlf", {"delimiter": ",", "no-header": True, "crlf": True}, ","),
        ("lf/quote-all", {"delimiter": ",", "no-header": True, "quote-all": True}, ","),
        (
            "crlf/quote-all",
            {"delimiter": ",", "no-header": True, "quote-all": True, "crlf": True},
            ",",
        ),
        ("semicolon", {"delimiter": ";", "no-header": True}, ";"),
    ],
)
def test_csv_round_trips_adversarial_row(label, opts, delim):
    """Round-trip is the acceptance criterion, not byte-equality.

    Byte-equality alone would be satisfied by every runtime agreeing on lossy
    output. Decoded with python's own csv.reader.
    """
    import csv as _csv

    out = _render([_ADVERSARIAL], opts=opts)
    records = list(_csv.reader(io.StringIO(out, newline=""), delimiter=delim))
    assert len(records) == 1, f"{label}: one row must decode to one record, got {out!r}"
    assert records[0] == _ADVERSARIAL_VALUES, f"{label}: round-trip must be lossless"


def test_csv_crlf_mode_does_not_promote_in_field_lf():
    """Only the record terminator is CRLF; an in-field LF stays an LF."""
    out = _render([{"v": "a\nb"}], opts={"delimiter": ",", "no-header": True, "crlf": True})
    assert out == '"a\nb"\r\n'


@pytest.mark.parametrize(
    ("value", "want"),
    [
        ("\tlead", '"\tlead"\n'),
        (" lead", '" lead"\n'),
        ("\u00a0lead", '"\u00a0lead"\n'),
        ("\vlead", '"\vlead"\n'),
        ("trail ", "trail \n"),
        ("plain", "plain\n"),
    ],
)
def test_csv_quotes_any_leading_unicode_space(value, want):
    """Quoting keys off a leading unicode space, not merely an ASCII one."""
    out = _render([{"v": value}], opts={"delimiter": ",", "no-header": True})
    assert out == want


def test_csv_quotes_postgres_copy_sentinel():
    r"""``\.`` alone on a line terminates a PostgreSQL COPY stream."""
    out = _render([{"v": "\\."}], opts={"delimiter": ",", "no-header": True})
    assert out == '"\\."\n'
