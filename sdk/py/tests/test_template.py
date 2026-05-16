"""Tests for --template option (Jinja2 escape hatch)."""

from __future__ import annotations

import typer
from typer.testing import CliRunner

from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import dispatch
from hop_top_kit.output.formatter import ColumnSpec

runner = CliRunner()


def _build_app():
    app = typer.Typer(no_args_is_help=False, add_completion=False)
    register_output_flags(app)

    @app.command("list")
    def list_cmd(ctx: typer.Context) -> None:
        data = [
            {"name": "alice", "count": "1"},
            {"name": "bob", "count": "2"},
        ]
        cols = [
            ColumnSpec(header="name", key="name"),
            ColumnSpec(header="count", key="count"),
        ]
        dispatch(ctx, data, columns=cols)

    return app


# ---------------------------------------------------------------------------
# Basic for-loop
# ---------------------------------------------------------------------------


def test_template_for_loop_renders_items():
    app = _build_app()
    tmpl = "{% for it in items %}{{ it.name }}={{ it.count }};{% endfor %}"
    result = runner.invoke(app, ["list", "--template", tmpl])
    assert result.exit_code == 0
    assert result.stdout.startswith("alice=1;bob=2;")


def test_template_exposes_cols():
    app = _build_app()
    tmpl = "cols: {{ cols | join(',') }}"
    result = runner.invoke(app, ["list", "--template", tmpl])
    assert result.exit_code == 0
    assert "cols: name,count" in result.stdout


# ---------------------------------------------------------------------------
# Errors: missing field, mutual exclusion with --cols, parse errors
# ---------------------------------------------------------------------------


def test_template_missing_field_renders_blank_no_crash():
    """Jinja2 renders missing attrs as empty by default — confirm we don't crash."""
    app = _build_app()
    tmpl = "{% for it in items %}{{ it.unknown_field }}{% endfor %}"
    result = runner.invoke(app, ["list", "--template", tmpl])
    assert result.exit_code == 0


def test_template_mutex_with_cols():
    app = _build_app()
    result = runner.invoke(
        app,
        ["list", "--template", "{{ items }}", "--cols", "name"],
    )
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "mutually exclusive" in msg


def test_template_parse_error_surfaces():
    app = _build_app()
    # Unmatched {% for %} → parse error
    result = runner.invoke(app, ["list", "--template", "{% for x in items %}"])
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "parse template" in msg or "execute template" in msg


# ---------------------------------------------------------------------------
# No autoescape (raw text output for non-HTML adopters)
# ---------------------------------------------------------------------------


def test_template_no_autoescape():
    app = _build_app()
    tmpl = "{{ '<a>' }}"
    result = runner.invoke(app, ["list", "--template", tmpl])
    assert result.exit_code == 0
    assert "<a>" in result.stdout
    assert "&lt;" not in result.stdout
