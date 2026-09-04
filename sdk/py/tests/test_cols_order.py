"""Column-ordering contract tests for the output layer.

Covers the five settled rules:

1. A ColumnSpec list drives default column order + headers.
2. ``--cols`` reorders as well as selects — user order wins.
3. ``header == key`` universally; validation and lookup use the same name.
4. Zero rows emits nothing — emptiness decided by ROW count.
5. ``priority`` is accepted, stored and ignored by the payload SDKs.
"""

from __future__ import annotations

import io
import json
from typing import Any

import pytest
import typer
from typer.testing import CliRunner

from hop_top_kit.output import new_registry
from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import dispatch
from hop_top_kit.output.formatter import ColumnSpec
from hop_top_kit.output.formatters.csv_formatter import CSVFormatter
from hop_top_kit.output.formatters.json_formatter import JSONFormatter
from hop_top_kit.output.formatters.table_formatter import TableFormatter
from hop_top_kit.output.formatters.text_formatter import TextFormatter
from hop_top_kit.output.projection import filter_columns, to_rows

runner = CliRunner()

# Payload key order deliberately DIFFERS from the ColumnSpec order below, so
# any assertion on sequence distinguishes "spec order" from "payload order".
ROWS = [
    {"count": "1", "status": "ok", "name": "alice"},
    {"count": "2", "status": "warn", "name": "bob"},
]

# ColumnSpec order: name, count, status.
COLUMNS = [
    ColumnSpec(header="name", key="name"),
    ColumnSpec(header="count", key="count"),
    ColumnSpec(header="status", key="status"),
]


def _build_app(columns: list[ColumnSpec] | None = COLUMNS, data=None):
    payload = ROWS if data is None else data
    app = typer.Typer(no_args_is_help=False, add_completion=False)
    register_output_flags(app)

    @app.command("list")
    def list_cmd(ctx: typer.Context) -> None:
        dispatch(ctx, payload, columns=columns)

    return app


def _header_line(out: str) -> list[str]:
    return out.splitlines()[0].split()


# ---------------------------------------------------------------------------
# Rule 1 — ColumnSpec list drives default order + headers
# ---------------------------------------------------------------------------


def test_table_default_order_follows_columnspec_not_payload_keys():
    """No --cols: header sequence is the ColumnSpec list order, exactly."""
    app = _build_app()
    result = runner.invoke(app, ["list"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert _header_line(result.stdout) == ["name", "count", "status"]


def test_csv_default_order_follows_columnspec_not_payload_keys():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "csv"])
    assert result.exit_code == 0, result.stdout + result.stderr
    lines = result.stdout.splitlines()
    assert lines[0] == "name,count,status"
    assert lines[1] == "alice,1,ok"


def test_text_default_order_follows_columnspec_not_payload_keys():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "text"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert result.stdout.splitlines()[:3] == ["name=alice", "count=1", "status=ok"]


def test_json_default_key_order_follows_columnspec():
    """Serialized key order — not membership — is the ColumnSpec order."""
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "json"])
    assert result.exit_code == 0, result.stdout + result.stderr
    decoded = json.loads(result.stdout)
    assert list(decoded[0].keys()) == ["name", "count", "status"]


def test_no_columnspec_falls_back_to_payload_key_order():
    """Fallback path: payload insertion order when no ColumnSpec supplied."""
    app = _build_app(columns=None)
    result = runner.invoke(app, ["list"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert _header_line(result.stdout) == ["count", "status", "name"]


# ---------------------------------------------------------------------------
# Rule 2 — --cols reorders as well as selects; user order wins
# ---------------------------------------------------------------------------


def test_cols_reorders_against_columnspec_order():
    """--cols status,name renders status then name, not the spec's order."""
    app = _build_app()
    result = runner.invoke(app, ["list", "--cols", "status,name"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert _header_line(result.stdout) == ["status", "name"]


def test_cols_reorders_on_the_no_schema_fallback_path():
    """Same rule with no ColumnSpec: user order wins over payload order."""
    app = _build_app(columns=None)
    result = runner.invoke(app, ["list", "--cols", "name,count"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert _header_line(result.stdout) == ["name", "count"]


def test_cols_repeated_flags_render_in_first_seen_order():
    """Dedupe order from _split_cols BECOMES the render order."""
    app = _build_app()
    result = runner.invoke(app, ["list", "--cols", "status,count", "--cols", "name,status"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert _header_line(result.stdout) == ["status", "count", "name"]


def test_json_cols_projects_and_reorders():
    """json honors --cols: projected rows, keys in user-typed order."""
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "json", "--cols", "status,name"])
    assert result.exit_code == 0, result.stdout + result.stderr
    decoded = json.loads(result.stdout)
    assert [list(r.keys()) for r in decoded] == [
        ["status", "name"],
        ["status", "name"],
    ]
    assert decoded[0] == {"status": "ok", "name": "alice"}


def test_json_cols_projects_single_mapping():
    """Single-dict payloads project too, not just lists."""
    app = _build_app(columns=None, data={"count": "1", "status": "ok", "name": "alice"})
    result = runner.invoke(app, ["list", "--format", "json", "--cols", "name,status"])
    assert result.exit_code == 0, result.stdout + result.stderr
    decoded = json.loads(result.stdout)
    assert list(decoded.keys()) == ["name", "status"]


# ---------------------------------------------------------------------------
# Rule 3 — one source of truth for validation and lookup
# ---------------------------------------------------------------------------


def test_columnspec_column_missing_from_payload_renders_blank_not_valueerror():
    """dispatch validation and filter_columns agree: a spec'd column absent
    from the payload is a hole, never a mid-render 'unknown column'."""
    columns = [*COLUMNS, ColumnSpec(header="extra", key="extra")]
    app = _build_app(columns=columns)
    result = runner.invoke(app, ["list", "--cols", "name,extra"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert _header_line(result.stdout) == ["name", "extra"]


def test_columnspec_column_missing_from_payload_is_valid_without_cols():
    columns = [*COLUMNS, ColumnSpec(header="extra", key="extra")]
    app = _build_app(columns=columns)
    result = runner.invoke(app, ["list", "--format", "csv"])
    assert result.exit_code == 0, result.stdout + result.stderr
    lines = result.stdout.splitlines()
    assert lines[0] == "name,count,status,extra"
    assert lines[1] == "alice,1,ok,"


def test_unknown_column_still_rejected_against_columnspec():
    app = _build_app()
    result = runner.invoke(app, ["list", "--cols", "mystery"])
    assert result.exit_code != 0
    assert "unknown column 'mystery'" in result.stdout + result.stderr


def test_columnspec_rejects_header_key_divergence():
    """header == key universally — drift is impossible by construction."""
    with pytest.raises(ValueError, match="header"):
        ColumnSpec(header="Name", key="name")


# ---------------------------------------------------------------------------
# Adopter compatibility — the four-argument render() form still works
# ---------------------------------------------------------------------------


def test_four_argument_adopter_formatter_still_receives_render():
    """Formatters predating the contract are called without *columns*."""
    seen: list[tuple[Any, ...]] = []

    class _LegacyFormatter:
        key = "legacy"
        extensions: tuple[str, ...] = ()

        def options(self) -> list:
            return []

        def render(self, out, data, opts, cols) -> None:
            seen.append((data, cols))
            out.write("legacy\n")

    registry = new_registry()
    registry.register(_LegacyFormatter())
    app = typer.Typer(no_args_is_help=False, add_completion=False)
    register_output_flags(app, registry=registry)

    @app.command("list")
    def list_cmd(ctx: typer.Context) -> None:
        dispatch(ctx, ROWS, columns=COLUMNS)

    result = runner.invoke(app, ["list", "--format", "legacy", "--cols", "name"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert result.stdout == "legacy\n"
    assert seen == [(ROWS, ["name"])]


# ---------------------------------------------------------------------------
# Rule 4 — zero rows emits nothing, decided by ROW count
# ---------------------------------------------------------------------------


def test_empty_rows_with_columnspec_emits_nothing_table():
    app = _build_app(columns=COLUMNS, data=[])
    result = runner.invoke(app, ["list"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert result.stdout == ""


def test_empty_rows_with_columnspec_emits_nothing_csv():
    app = _build_app(columns=COLUMNS, data=[])
    result = runner.invoke(app, ["list", "--format", "csv"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert result.stdout == ""


def test_empty_rows_with_columnspec_emits_nothing_text():
    app = _build_app(columns=COLUMNS, data=[])
    result = runner.invoke(app, ["list", "--format", "text"])
    assert result.exit_code == 0, result.stdout + result.stderr
    assert result.stdout == ""


def test_table_formatter_emits_nothing_for_zero_rows_with_headers():
    """Direct: headers present, zero rows → no bare header row."""
    buf = io.StringIO()
    TableFormatter().render(buf, [], {}, [], columns=COLUMNS)
    assert buf.getvalue() == ""


def test_csv_formatter_emits_nothing_for_zero_rows_with_headers():
    buf = io.StringIO()
    CSVFormatter().render(buf, [], {"delimiter": ","}, [], columns=COLUMNS)
    assert buf.getvalue() == ""


def test_text_formatter_emits_nothing_for_zero_rows_with_headers():
    buf = io.StringIO()
    TextFormatter().render(buf, [], {"style": "kv", "separator": "="}, [], columns=COLUMNS)
    assert buf.getvalue() == ""


def test_json_formatter_emits_empty_list_for_zero_rows():
    """Rule 4 governs tabular formatters; json still encodes the value."""
    buf = io.StringIO()
    JSONFormatter().render(buf, [], {"indent": 0}, ["name"], columns=COLUMNS)
    assert buf.getvalue() == "[]\n"


# ---------------------------------------------------------------------------
# Rule 5 — priority accepted, stored, ignored
# ---------------------------------------------------------------------------


def test_priority_is_stored_and_ignored():
    columns = [
        ColumnSpec(header="name", key="name", priority=9),
        ColumnSpec(header="count", key="count", priority=1),
        ColumnSpec(header="status", key="status", priority=5),
    ]
    assert [c.priority for c in columns] == [9, 1, 5]
    app = _build_app(columns=columns)
    result = runner.invoke(app, ["list"])
    assert result.exit_code == 0, result.stdout + result.stderr
    # List order, untouched by priority.
    assert _header_line(result.stdout) == ["name", "count", "status"]


# ---------------------------------------------------------------------------
# projection helpers — single source of truth
# ---------------------------------------------------------------------------


def test_to_rows_honors_columns_over_payload_key_order():
    headers, rows = to_rows(ROWS, columns=COLUMNS)
    assert headers == ["name", "count", "status"]
    assert rows == [["alice", "1", "ok"], ["bob", "2", "warn"]]


def test_to_rows_fills_missing_columnspec_keys_with_blank():
    columns = [*COLUMNS, ColumnSpec(header="extra", key="extra")]
    headers, rows = to_rows(ROWS, columns=columns)
    assert headers == ["name", "count", "status", "extra"]
    assert rows[0] == ["alice", "1", "ok", ""]


def test_filter_columns_validates_against_columnspec_not_row_keys():
    """A spec'd-but-absent column passes; a truly unknown one still raises."""
    columns = [*COLUMNS, ColumnSpec(header="extra", key="extra")]
    headers, rows = to_rows(ROWS, columns=columns)
    got_h, got_r = filter_columns(headers, rows, ["extra", "name"])
    assert got_h == ["extra", "name"]
    assert got_r[0] == ["", "alice"]
    with pytest.raises(ValueError, match="unknown column 'mystery'"):
        filter_columns(headers, rows, ["mystery"])
