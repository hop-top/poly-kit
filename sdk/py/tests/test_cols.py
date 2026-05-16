"""Tests for --cols / --columns repeatable Typer option."""

from __future__ import annotations

import typer
from typer.testing import CliRunner

from hop_top_kit.output.cli import _split_cols, register_output_flags
from hop_top_kit.output.dispatch import dispatch
from hop_top_kit.output.formatter import ColumnSpec

runner = CliRunner()


COLUMNS = [
    ColumnSpec(header="name", key="name"),
    ColumnSpec(header="count", key="count"),
    ColumnSpec(header="status", key="status"),
]


def _build_app(with_columns: bool = True):
    app = typer.Typer(no_args_is_help=False, add_completion=False)
    register_output_flags(app)
    columns = COLUMNS if with_columns else None

    @app.command("list")
    def list_cmd(ctx: typer.Context) -> None:
        data = [
            {"name": "alice", "count": "1", "status": "ok"},
            {"name": "bob", "count": "2", "status": "warn"},
        ]
        dispatch(ctx, data, columns=columns)

    return app


# ---------------------------------------------------------------------------
# _split_cols helper
# ---------------------------------------------------------------------------


def test_split_cols_single():
    assert _split_cols(["name"]) == ["name"]


def test_split_cols_comma_split():
    assert _split_cols(["name,count"]) == ["name", "count"]


def test_split_cols_repeated_dedupe_preserves_first_seen_order():
    """Repeated --cols flags accumulate; dedupe keeps first occurrence."""
    assert _split_cols(["name,count", "status,name"]) == ["name", "count", "status"]


def test_split_cols_strips_whitespace_and_empties():
    assert _split_cols(["name, ,count"]) == ["name", "count"]


# ---------------------------------------------------------------------------
# Through Typer — subset, all, unknown, dedupe
# ---------------------------------------------------------------------------


def test_cols_subset_table():
    app = _build_app()
    result = runner.invoke(app, ["list", "--cols", "name,count"])
    assert result.exit_code == 0
    assert "status" not in result.stdout
    assert "alice" in result.stdout


def test_cols_alias_columns_works():
    app = _build_app()
    result = runner.invoke(app, ["list", "--columns", "name"])
    assert result.exit_code == 0
    assert "alice" in result.stdout
    assert "ok" not in result.stdout  # status filtered


def test_cols_repeats_accumulate():
    app = _build_app()
    result = runner.invoke(
        app,
        ["list", "--cols", "name", "--cols", "status"],
    )
    assert result.exit_code == 0
    # name + status, no count
    assert "1" not in result.stdout.split("\n")[0]


def test_cols_unknown_column_errors():
    app = _build_app()
    result = runner.invoke(app, ["list", "--cols", "mystery"])
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "unknown column 'mystery'" in msg


def test_cols_works_for_csv():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "csv", "--cols", "name,status"])
    assert result.exit_code == 0
    assert result.stdout.startswith("name,status")
    assert "count" not in result.stdout.split("\n")[0]


def test_cols_works_for_text():
    app = _build_app()
    result = runner.invoke(
        app,
        ["list", "--format", "text", "--cols", "name"],
    )
    assert result.exit_code == 0
    # kv style: name=alice etc.
    assert "name=alice" in result.stdout
    assert "count" not in result.stdout


def test_cols_no_schema_still_filters_via_formatter():
    """When no ColumnSpec schema is passed, the formatter still honors cols
    against its own row keys."""
    app = _build_app(with_columns=False)
    result = runner.invoke(app, ["list", "--cols", "name"])
    assert result.exit_code == 0
    assert "alice" in result.stdout
    # status should be filtered out
    assert "ok" not in result.stdout
