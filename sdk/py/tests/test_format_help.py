"""Tests for --format-help (catalog + per-format options)."""

from __future__ import annotations

import io

import pytest
import typer
from typer.testing import CliRunner

from hop_top_kit.output import default_registry
from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import dispatch
from hop_top_kit.output.format_help import (
    format_options,
    list_formats,
    render_format_help,
)

runner = CliRunner()


def _build_app():
    app = typer.Typer(no_args_is_help=False, add_completion=False)
    register_output_flags(app)

    @app.command("list")
    def list_cmd(ctx: typer.Context) -> None:
        dispatch(ctx, [{"a": "1"}])

    return app


# ---------------------------------------------------------------------------
# Pure helpers
# ---------------------------------------------------------------------------


def test_list_formats_returns_all_builtins():
    rows = list_formats(default_registry)
    keys = [r["key"] for r in rows]
    assert keys == sorted(keys)
    assert {"json", "yaml", "table", "csv", "text"}.issubset(set(keys))


def test_list_formats_extensions_summary():
    rows = list_formats(default_registry)
    yaml_row = next(r for r in rows if r["key"] == "yaml")
    assert ".yaml" in yaml_row["extensions"]
    assert ".yml" in yaml_row["extensions"]


def test_format_options_csv():
    rows = format_options(default_registry, "csv")
    names = [r["name"] for r in rows]
    assert names == ["delimiter", "no-header", "quote-all", "crlf"]


def test_format_options_unknown_raises():
    with pytest.raises(ValueError, match="unknown format 'mystery'"):
        format_options(default_registry, "mystery")


def test_render_format_help_no_arg_lists_all():
    buf = io.StringIO()
    render_format_help(buf, default_registry, "")
    out = buf.getvalue()
    assert "json" in out and "csv" in out and "text" in out
    # header line
    assert "key" in out and "extensions" in out and "options" in out


def test_render_format_help_per_format():
    buf = io.StringIO()
    render_format_help(buf, default_registry, "csv")
    out = buf.getvalue()
    assert "delimiter" in out
    assert "no-header" in out


def test_render_format_help_no_options():
    buf = io.StringIO()
    render_format_help(buf, default_registry, "table")
    out = buf.getvalue()
    assert "no options" in out


# ---------------------------------------------------------------------------
# Through Typer
# ---------------------------------------------------------------------------


def test_format_help_flag_lists_catalog():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format-help"])
    assert result.exit_code == 0
    assert "csv" in result.stdout
    assert "json" in result.stdout


def test_format_help_with_format_scopes_to_one():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "csv", "--format-help"])
    assert result.exit_code == 0
    assert "delimiter" in result.stdout
    assert "no-header" in result.stdout


def test_format_help_unknown_format_errors():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "weird", "--format-help"])
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "unknown format 'weird'" in msg
