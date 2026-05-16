"""Tests for hop_top_kit.output.formatter — Protocol + OptionSpec + parse_options."""

from __future__ import annotations

import io

import pytest

from hop_top_kit.output.formatter import (
    ColumnSpec,
    Formatter,
    OptionSpec,
    parse_options,
)

# ---------------------------------------------------------------------------
# OptionSpec / ColumnSpec basics
# ---------------------------------------------------------------------------


def test_option_spec_frozen():
    import dataclasses

    s = OptionSpec(name="indent", type="int", usage="x", default=2)
    with pytest.raises(dataclasses.FrozenInstanceError):
        s.name = "other"  # type: ignore[misc]


def test_column_spec_default_priority():
    c = ColumnSpec(header="Name", key="name")
    assert c.priority == 5


# ---------------------------------------------------------------------------
# Protocol (runtime_checkable)
# ---------------------------------------------------------------------------


class _Good:
    key = "x"
    extensions: tuple[str, ...] = ()

    def options(self) -> list[OptionSpec]:
        return []

    def render(self, out, data, opts, cols) -> None:  # pragma: no cover
        pass


class _MissingKey:
    extensions: tuple[str, ...] = ()

    def options(self) -> list[OptionSpec]:  # pragma: no cover
        return []

    def render(self, out, data, opts, cols) -> None:  # pragma: no cover
        pass


def test_formatter_protocol_isinstance_good():
    assert isinstance(_Good(), Formatter)


def test_formatter_protocol_isinstance_missing_attr():
    # runtime_checkable inspects attribute presence — missing key fails.
    assert not isinstance(_MissingKey(), Formatter)


# ---------------------------------------------------------------------------
# parse_options — coercion
# ---------------------------------------------------------------------------


def test_parse_options_string():
    specs = [OptionSpec(name="delimiter", type="string", default=",")]
    out = parse_options(["delimiter=;"], specs)
    assert out == {"delimiter": ";"}


def test_parse_options_int():
    specs = [OptionSpec(name="indent", type="int", default=2)]
    out = parse_options(["indent=4"], specs)
    assert out == {"indent": 4}


def test_parse_options_bool_true():
    specs = [OptionSpec(name="flag", type="bool", default=False)]
    assert parse_options(["flag=true"], specs) == {"flag": True}
    assert parse_options(["flag=1"], specs) == {"flag": True}
    assert parse_options(["flag=yes"], specs) == {"flag": True}


def test_parse_options_bool_false():
    specs = [OptionSpec(name="flag", type="bool", default=True)]
    assert parse_options(["flag=false"], specs) == {"flag": False}
    assert parse_options(["flag=0"], specs) == {"flag": False}


def test_parse_options_bool_key_only():
    """Key-only form is bool-true shortcut — only valid for bool specs."""
    specs = [OptionSpec(name="quote-all", type="bool", default=False)]
    out = parse_options(["quote-all"], specs)
    assert out == {"quote-all": True}


def test_parse_options_enum():
    specs = [OptionSpec(name="style", type="enum", enum=("kv", "lines"), default="kv")]
    out = parse_options(["style=lines"], specs)
    assert out == {"style": "lines"}


# ---------------------------------------------------------------------------
# parse_options — defaults
# ---------------------------------------------------------------------------


def test_parse_options_fills_defaults():
    specs = [
        OptionSpec(name="delimiter", type="string", default=","),
        OptionSpec(name="no-header", type="bool", default=False),
    ]
    out = parse_options([], specs)
    assert out == {"delimiter": ",", "no-header": False}


def test_parse_options_default_none_skipped():
    specs = [OptionSpec(name="x", type="string")]  # default None
    out = parse_options([], specs)
    assert out == {}


# ---------------------------------------------------------------------------
# parse_options — error paths
# ---------------------------------------------------------------------------


def test_parse_options_unknown_key():
    specs = [OptionSpec(name="known", type="string", default="")]
    with pytest.raises(ValueError, match="unknown option 'mystery'"):
        parse_options(["mystery=x"], specs)


def test_parse_options_int_type_error():
    specs = [OptionSpec(name="indent", type="int", default=2)]
    with pytest.raises(ValueError, match="not an int"):
        parse_options(["indent=abc"], specs)


def test_parse_options_bool_type_error():
    specs = [OptionSpec(name="flag", type="bool", default=False)]
    with pytest.raises(ValueError, match="not a bool"):
        parse_options(["flag=maybe"], specs)


def test_parse_options_enum_out_of_range():
    specs = [OptionSpec(name="style", type="enum", enum=("kv", "lines"))]
    with pytest.raises(ValueError, match="not in"):
        parse_options(["style=other"], specs)


def test_parse_options_key_only_for_non_bool_raises():
    specs = [OptionSpec(name="delimiter", type="string", default=",")]
    with pytest.raises(ValueError, match="requires a value"):
        parse_options(["delimiter"], specs)


def test_parse_options_empty_key():
    specs = [OptionSpec(name="x", type="string", default="")]
    with pytest.raises(ValueError, match="empty option key"):
        parse_options(["=v"], specs)


# Smoke check: stdlib io.StringIO is an acceptable TextIO target.
def test_textio_works():
    buf = io.StringIO()
    buf.write("ok")
    assert buf.getvalue() == "ok"
