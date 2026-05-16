"""Tests for hop_top_kit.output.dispatch + register_output_flags.

Phase 3 covers --format-opt accumulation + error paths + key-only bool form.
Phase 4 / 5 tests live alongside the relevant features.
"""

from __future__ import annotations

import io

import typer
from typer.testing import CliRunner

from hop_top_kit.output import default_registry, new_registry
from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import OutputFlags, dispatch
from hop_top_kit.output.formatters.csv_formatter import CSVFormatter
from hop_top_kit.output.formatters.json_formatter import JSONFormatter
from hop_top_kit.output.formatters.table_formatter import TableFormatter
from hop_top_kit.output.formatters.text_formatter import TextFormatter
from hop_top_kit.output.formatters.yaml_formatter import YAMLFormatter

runner = CliRunner()


def _build_app(registry=None):
    """Build a Typer app wired with output flags + a 'list' subcommand."""
    app = typer.Typer(no_args_is_help=False, add_completion=False)
    register_output_flags(app, registry=registry)

    @app.command("list")
    def list_cmd(ctx: typer.Context) -> None:
        data = [{"name": "alice", "count": "1"}, {"name": "bob", "count": "2"}]
        dispatch(ctx, data)

    return app


# ---------------------------------------------------------------------------
# Default → table
# ---------------------------------------------------------------------------


def test_dispatch_defaults_to_table():
    app = _build_app()
    result = runner.invoke(app, ["list"])
    assert result.exit_code == 0
    out = result.stdout
    assert "name" in out
    assert "alice" in out


# ---------------------------------------------------------------------------
# --format
# ---------------------------------------------------------------------------


def test_dispatch_format_json():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "json"])
    assert result.exit_code == 0
    assert result.stdout.startswith("[")


def test_dispatch_format_csv():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "csv"])
    assert result.exit_code == 0
    assert result.stdout.startswith("name,count")


def test_dispatch_unknown_format():
    app = _build_app()
    result = runner.invoke(app, ["list", "--format", "weird"])
    assert result.exit_code != 0
    assert "unknown output format" in result.stdout + result.stderr


# ---------------------------------------------------------------------------
# --format-opt: accumulation + error paths
# ---------------------------------------------------------------------------


def test_format_opt_accumulates():
    app = _build_app()
    result = runner.invoke(
        app,
        [
            "list",
            "--format",
            "csv",
            "--format-opt",
            "delimiter=;",
            "--format-opt",
            "no-header=true",
        ],
    )
    assert result.exit_code == 0
    # delimiter swap + no header
    assert result.stdout.startswith("alice;1")


def test_format_opt_key_only_bool():
    app = _build_app()
    result = runner.invoke(
        app,
        [
            "list",
            "--format",
            "csv",
            "--format-opt",
            "no-header",
        ],
    )
    assert result.exit_code == 0
    assert result.stdout.startswith("alice,1")


def test_format_opt_unknown_key():
    app = _build_app()
    result = runner.invoke(
        app,
        ["list", "--format", "csv", "--format-opt", "mystery=x"],
    )
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "unknown option 'mystery'" in msg


def test_format_opt_type_error_int():
    app = _build_app()
    result = runner.invoke(
        app,
        ["list", "--format", "json", "--format-opt", "indent=abc"],
    )
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "not an int" in msg


def test_format_opt_enum_out_of_range():
    app = _build_app()
    result = runner.invoke(
        app,
        ["list", "--format", "text", "--format-opt", "style=weird"],
    )
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "not in" in msg


def test_format_opt_key_only_for_non_bool_raises():
    app = _build_app()
    result = runner.invoke(
        app,
        ["list", "--format", "csv", "--format-opt", "delimiter"],
    )
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "requires a value" in msg


# ---------------------------------------------------------------------------
# Direct dispatch (no Typer runner) — sanity-check OutputFlags shape
# ---------------------------------------------------------------------------


def test_dispatch_direct_with_outputflags():
    """Building OutputFlags by hand + invoking dispatch should work without Typer."""
    import contextlib
    import sys

    class _FakeCtx:
        obj = OutputFlags(
            format="json",
            format_explicit=True,
            format_opt=["indent=4"],
            output="-",
        )

    buf = io.StringIO()
    saved = sys.stdout
    sys.stdout = buf
    try:
        # typer.Exit only fires on --format-help; defensive suppress.
        with contextlib.suppress(SystemExit):
            dispatch(_FakeCtx(), {"a": 1})
    finally:
        sys.stdout = saved
    assert '"a": 1' in buf.getvalue()
    # 4-space indent honored
    assert "{\n    " in buf.getvalue()


# ---------------------------------------------------------------------------
# Custom registry path
# ---------------------------------------------------------------------------


def test_dispatch_uses_custom_registry():
    """register_output_flags(registry=R) routes lookups through R."""
    r = new_registry()
    r.register(JSONFormatter())
    r.register(TableFormatter())  # default fallback
    r.register(YAMLFormatter())
    r.register(CSVFormatter())
    r.register(TextFormatter())
    app = _build_app(registry=r)
    result = runner.invoke(app, ["list", "--format", "json"])
    assert result.exit_code == 0


def test_default_registry_has_all_builtins():
    keys = set(default_registry.keys())
    assert {"json", "yaml", "table", "csv", "text"}.issubset(keys)
