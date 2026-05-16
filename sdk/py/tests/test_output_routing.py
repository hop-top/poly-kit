"""Tests for --output|-o flag (stdout sentinel + ext inference + mismatch)."""

from __future__ import annotations

import json

import typer
from typer.testing import CliRunner

from hop_top_kit.output.cli import register_output_flags
from hop_top_kit.output.dispatch import dispatch, resolve_writer

runner = CliRunner()


def _build_app(disable_output: bool = False):
    app = typer.Typer(no_args_is_help=False, add_completion=False)
    register_output_flags(
        app,
        disable={"output": True} if disable_output else None,
    )

    @app.command("list")
    def list_cmd(ctx: typer.Context) -> None:
        data = [{"name": "alice", "count": "1"}]
        dispatch(ctx, data)

    return app


# ---------------------------------------------------------------------------
# resolve_writer (pure helper)
# ---------------------------------------------------------------------------


def test_resolve_writer_empty_path_is_stdout():
    import sys

    with resolve_writer("") as w:
        assert w is sys.stdout


def test_resolve_writer_dash_is_stdout():
    import sys

    with resolve_writer("-") as w:
        assert w is sys.stdout


def test_resolve_writer_opens_and_closes_file(tmp_path):
    p = tmp_path / "out.txt"
    with resolve_writer(str(p)) as w:
        w.write("hello")
    assert p.read_text() == "hello"


def test_resolve_writer_truncates_existing(tmp_path):
    p = tmp_path / "out.txt"
    p.write_text("old contents")
    with resolve_writer(str(p)) as w:
        w.write("new")
    assert p.read_text() == "new"


def test_resolve_writer_directory_errors(tmp_path):
    import pytest

    with pytest.raises(OSError, match="is a directory"):
        with resolve_writer(str(tmp_path)) as _:
            pass


# ---------------------------------------------------------------------------
# Through Typer
# ---------------------------------------------------------------------------


def test_output_to_stdout_default():
    app = _build_app()
    result = runner.invoke(app, ["list"])
    assert result.exit_code == 0
    assert "alice" in result.stdout


def test_output_to_file(tmp_path):
    app = _build_app()
    p = tmp_path / "out.csv"
    result = runner.invoke(app, ["list", "--format", "csv", "-o", str(p)])
    assert result.exit_code == 0
    assert p.exists()
    body = p.read_text()
    assert body.startswith("name,count")


def test_output_dash_sentinel_writes_to_stdout():
    app = _build_app()
    result = runner.invoke(app, ["list", "-o", "-", "--format", "json"])
    assert result.exit_code == 0
    assert result.stdout.startswith("[")


def test_output_disabled_strips_flag():
    app = _build_app(disable_output=True)
    # --output flag should not be recognized → exit code != 0
    result = runner.invoke(app, ["list", "-o", "/tmp/x"])
    assert result.exit_code != 0


# ---------------------------------------------------------------------------
# Extension inference + mismatch
# ---------------------------------------------------------------------------


def test_ext_inference_json(tmp_path):
    """No --format + .json output → format infers to json."""
    app = _build_app()
    p = tmp_path / "out.json"
    result = runner.invoke(app, ["list", "-o", str(p)])
    assert result.exit_code == 0
    body = p.read_text()
    parsed = json.loads(body)
    assert parsed == [{"name": "alice", "count": "1"}]


def test_ext_inference_csv(tmp_path):
    app = _build_app()
    p = tmp_path / "out.csv"
    result = runner.invoke(app, ["list", "-o", str(p)])
    assert result.exit_code == 0
    body = p.read_text()
    assert body.startswith("name,count")


def test_explicit_format_wins_when_ext_matches(tmp_path):
    """Explicit --format=json + .json → still json (no error)."""
    app = _build_app()
    p = tmp_path / "out.json"
    result = runner.invoke(app, ["list", "--format", "json", "-o", str(p)])
    assert result.exit_code == 0
    body = p.read_text()
    json.loads(body)  # parses as JSON


def test_explicit_format_mismatch_errors(tmp_path):
    """Explicit --format=json + .csv ext → mismatch error."""
    app = _build_app()
    p = tmp_path / "out.csv"
    result = runner.invoke(app, ["list", "--format", "json", "-o", str(p)])
    assert result.exit_code != 0
    msg = result.stdout + result.stderr
    assert "does not match output extension" in msg


def test_unknown_extension_falls_through_to_default(tmp_path):
    """Unknown ext → no inference; default 'table' format used."""
    app = _build_app()
    p = tmp_path / "out.unknownext"
    result = runner.invoke(app, ["list", "-o", str(p)])
    assert result.exit_code == 0
    # table renders headers + row aligned
    assert "name" in p.read_text()


def test_overwrite_existing_file(tmp_path):
    """--output silently overwrites existing files."""
    app = _build_app()
    p = tmp_path / "out.json"
    p.write_text("stale")
    result = runner.invoke(app, ["list", "-o", str(p)])
    assert result.exit_code == 0
    assert "stale" not in p.read_text()
