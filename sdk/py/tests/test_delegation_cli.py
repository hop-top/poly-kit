"""The delegation gate on a real kit Typer app: root flags, leaf annotations,
shadowing, envelopes."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest
import typer
from typer.testing import CliRunner

from hop_top_kit import policy as pol
from hop_top_kit.cli import Disable, GlobalFlag, annotate, create_app, is_dry_run
from hop_top_kit.gate import PromptSource

runner = CliRunner()
NOTTY = PromptSource.none()


def _app(calls: list[str], **kw: Any) -> typer.Typer:
    kw.setdefault("prompt_source", NOTTY)
    app, _ = create_app(name="kitapp", version="1.0.0", help="h", **kw)

    def echo(name: str) -> None:
        calls.append(name)
        typer.echo(json.dumps({"dry_run": is_dry_run(), "path": name}, sort_keys=True))

    @app.command("ls")
    @annotate(side_effect="read")
    def ls() -> None:
        echo("ls")

    @app.command("add")
    @annotate(side_effect="write")
    def add() -> None:
        echo("add")

    @app.command("rm")
    @annotate(side_effect="destructive")
    def rm() -> None:
        echo("rm")

    @app.command("purge")
    @annotate(side_effect="destructive", destructive_token=True)
    def purge() -> None:
        echo("purge")

    @app.command("delete")
    def delete() -> None:  # unannotated, destructive by name
        echo("delete")

    @app.command("shell")
    @annotate(side_effect="interactive")
    def shell() -> None:
        echo("shell")

    @app.command("import")
    @annotate(side_effect="write")
    def import_(policy: str = typer.Option("", "--policy")) -> None:
        calls.append(f"import:{policy}")

    @app.command("danger")
    @annotate(side_effect="destructive")
    def danger(confirm: str = typer.Option("", "--confirm")) -> None:
        calls.append(f"danger:{confirm}")

    widget = typer.Typer()

    @widget.command("remove")
    def wremove() -> None:
        echo("widget remove")

    @widget.command("list")
    def wlist() -> None:
        echo("widget list")

    app.add_typer(widget, name="widget")
    return app


def _run(app: typer.Typer, *args: str):
    return runner.invoke(app, list(args))


def test_destructive_without_a_terminal_is_refused() -> None:
    calls: list[str] = []
    r = _run(_app(calls), "rm")
    assert r.exit_code == 5
    assert "UNAUTHORIZED" in r.stderr
    assert "refused: --confirm=no (or non-TTY default)" in r.stderr
    assert calls == []


@pytest.mark.parametrize("mode", ["yes", "auto"])
def test_confirm_modes_let_it_run(mode: str) -> None:
    calls: list[str] = []
    r = _run(_app(calls), "--confirm", mode, "rm")
    assert r.exit_code == 0, r.stderr
    assert json.loads(r.stdout) == {"dry_run": False, "path": "rm"}


def test_prompt_answers() -> None:
    calls: list[str] = []
    ok = _run(
        _app(calls, prompt_source=PromptSource.from_answers(["y"])), "--confirm", "prompt", "rm"
    )
    assert ok.exit_code == 0 and calls == ["rm"]
    no = _run(
        _app(calls, prompt_source=PromptSource.from_answers(["n"])), "--confirm", "prompt", "rm"
    )
    assert no.exit_code == 5
    assert "aborted by user at confirm prompt for kitapp rm" in no.stderr


def test_writes_and_reads_run_unconfirmed() -> None:
    calls: list[str] = []
    assert _run(_app(calls), "add").exit_code == 0
    assert _run(_app(calls), "ls").exit_code == 0
    assert calls == ["add", "ls"]


def test_json_refusal_is_an_envelope() -> None:
    r = _run(_app([]), "--format", "json", "rm")
    assert r.exit_code == 5
    assert json.loads(r.stderr)["code"] == "UNAUTHORIZED"


def test_typed_token() -> None:
    calls: list[str] = []
    r = _run(_app(calls), "--confirm", "yes", "purge")
    assert r.exit_code == 5 and "requires --confirm-token=9bcc9064589a" in r.stderr
    r = _run(_app(calls), "purge", "--confirm-token", "deadbeef0000")
    assert r.exit_code == 5
    assert "--confirm-token mismatch for kitapp purge; expected 9bcc9064589a" in r.stderr
    assert calls == []
    r = _run(_app(calls), "purge", "--confirm-token", "9bcc9064589a")
    assert r.exit_code == 0 and calls == ["purge"]


def test_token_flag_only_on_token_leaves() -> None:
    assert "--confirm-token" in _run(_app([]), "purge", "--help").stdout
    assert "--confirm-token" not in _run(_app([]), "rm", "--help").stdout


def test_inferred_destructive_names_are_gated_even_in_a_group() -> None:
    calls: list[str] = []
    assert _run(_app(calls), "delete").exit_code == 5
    assert _run(_app(calls), "widget", "remove").exit_code == 5
    assert _run(_app(calls), "widget", "list").exit_code == 0
    assert calls == ["widget list"]
    assert _run(_app(calls), "--confirm", "yes", "delete").exit_code == 0


def test_dry_run_tags_a_write_and_skips_the_destructive_confirm() -> None:
    r = _run(_app([]), "--dry-run", "add")
    assert json.loads(r.stdout) == {"dry_run": True, "path": "add"}
    r = _run(_app([]), "--dry-run", "rm")
    assert r.exit_code == 0 and json.loads(r.stdout)["dry_run"] is True


def test_dry_run_on_an_inferred_leaf_does_not_invoke_the_handler() -> None:
    calls: list[str] = []
    r = _run(_app(calls), "--dry-run", "delete")
    assert r.exit_code == 0, r.stderr
    assert calls == []  # handler never ran: nothing could mutate
    assert "not run" in r.stdout


def test_dry_run_refusals() -> None:
    r = _run(_app([]), "--dry-run", "shell")
    assert r.exit_code == 2 and '"kitapp shell" does not accept --dry-run' in r.stderr
    r = _run(_app([]), "--format", "json", "--dry-run", "shell")
    assert json.loads(r.stderr)["code"] == "USAGE"
    assert _run(_app([]), "--dry-run", "ls").exit_code == 0


def test_dry_run_flag_can_be_disabled() -> None:
    app, _ = create_app(name="t", version="1", help="h", disable=Disable(dry_run=True))
    assert _run(app, "--dry-run").exit_code == 2


def test_policy_file_refusal_and_unknown(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("XDG_CONFIG_HOME", str(tmp_path))
    d = tmp_path / "kitapp" / "policies"
    d.mkdir(parents=True)
    (d / "readonly.yaml").write_text("allow:\n  write: []\n  destructive: []\n")
    (d / "broken.yaml").write_text("allow: [\n")
    kw = {"policy_loader": pol.default_policy_loader("kitapp")}
    r = _run(_app([], **kw), "--policy", "readonly", "add")
    assert r.exit_code == 5 and "policy: write not allowed for add" in r.stderr
    assert _run(_app([], **kw), "--policy", "readonly", "ls").exit_code == 0
    r = _run(_app([], **kw), "--policy", "ghost", "add")
    assert r.exit_code == 2 and 'policy "ghost"' in r.stderr
    assert _run(_app([], **kw), "--policy", "broken", "add").exit_code == 2
    for bad in ("../x", str(d / "readonly")):
        r = _run(_app([], **kw), "--policy", bad, "add")
        assert r.exit_code == 2 and "invalid policy name" in r.stderr


def test_kit_default_policy_needs_no_loader() -> None:
    r = _run(_app([]), "--policy", "kit-default", "add")
    assert r.exit_code == 5 and "policy kit-default:" in r.stderr


def test_policy_without_loader_is_usage() -> None:
    assert _run(_app([]), "--policy", "x", "add").exit_code == 2


def test_leaf_local_policy_flag_is_not_the_delegation_policy() -> None:
    calls: list[str] = []
    r = _run(_app(calls), "import", "--policy", "/tmp/some.yaml")
    assert r.exit_code == 0, r.stderr
    assert calls == ["import:/tmp/some.yaml"]


def test_leaf_local_confirm_never_satisfies_the_root_gate() -> None:
    """A leaf's own --confirm is the leaf's: it cannot approve its own run."""
    calls: list[str] = []
    r = _run(_app(calls), "danger", "--confirm", "yes")
    assert r.exit_code == 5
    assert calls == []
    r = _run(_app(calls), "--confirm", "yes", "danger", "--confirm", "no")
    assert r.exit_code == 0 and calls == ["danger:no"]


def test_max_ops_within_budget_runs() -> None:
    r = _run(_app([]), "--max-ops", "1", "add")
    assert r.exit_code == 0


def test_reserved_global_names_are_refused() -> None:
    for n in ("confirm", "max-ops", "policy", "dry-run"):
        with pytest.raises(ValueError, match="reserved"):
            create_app(name="t", version="1", help="h", globals=[GlobalFlag(name=n, usage="u")])


def test_help_and_completion_are_not_gated() -> None:
    app, _ = create_app(name="kitapp", version="1.0.0", help="h")
    assert _run(app, "--help").exit_code == 0


def test_malformed_tier_leaf_fails_closed_without_running() -> None:
    calls: list[str] = []
    app, _ = create_app(name="kitapp", version="1", help="h", prompt_source=NOTTY)

    @app.command("nuke")
    @annotate(side_effect="destructve")
    def nuke() -> None:
        calls.append("mutated")

    for args in (["nuke"], ["--confirm", "yes", "nuke"], ["--dry-run", "nuke"]):
        r = _run(app, *args)
        assert r.exit_code == 2 and "malformed kit/side-effect" in r.stderr
    assert calls == []
