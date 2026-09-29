"""Kit objects placed in a typer command tree must come from the Click
layer typer runs on.

typer>=0.26 dispatches through a vendored Click (``typer._click``) with its
own classes; earlier typer runs on the ``click`` package. A ``click``
package object dropped into a typer>=0.26 tree still parses, but typer's
help renderer and shell completion select by ``isinstance`` against their
own layer and skip it: the flag vanishes from rich ``--help`` and from
option-name completion. Each test drives a real command tree the way an
adopter's binary does, so it holds on every typer in the supported range.
"""

from __future__ import annotations

import re
from pathlib import Path

import click
import pytest
import typer
import typer.main

from hop_top_kit.alias import bridge_to_click, save_to
from hop_top_kit.cli import GroupConfig, HelpConfig, create_app
from hop_top_kit.completion import static_values, to_click_shell_complete
from hop_top_kit.flagregister import FlagDisplay, register_set_flag, register_text_flag

try:  # typer>=0.26: vendored Click
    from typer._click.shell_completion import CompletionItem as _LayerCompletionItem
except ImportError:  # typer<0.26 runs on the click package
    from click.shell_completion import CompletionItem as _LayerCompletionItem

_ANSI = re.compile(r"\x1b\[[0-9;]*m")
_SDK = Path(__file__).resolve().parents[1]


def _kit_app() -> typer.Typer:
    # Groups make create_app register typer's shell completion classes.
    app, _ = create_app(
        name="t",
        version="1.0.0",
        help="t",
        help_config=HelpConfig(groups=[GroupConfig(id="commands", title="COMMANDS")]),
    )

    @app.command("go")
    def go(x: str = typer.Option("", "--x", help="x")) -> None:
        typer.echo("ran go")

    @app.command("deploy")
    def deploy() -> None:
        typer.echo("ran deploy")

    return app


def _main(cmd, args: list[str], capsys) -> tuple[int, str]:
    """Run a built command as its console script would; return (code, out)."""
    code = 0
    try:
        cmd.main(args, prog_name="t")
    except SystemExit as e:
        code = e.code or 0
    out = capsys.readouterr()
    return code, _ANSI.sub("", out.out + out.err)


def _complete(cmd, line: str, capsys, monkeypatch) -> str:
    monkeypatch.setenv("_T_COMPLETE", "complete_zsh")
    monkeypatch.setenv("_TYPER_COMPLETE_ARGS", line)
    _, out = _main(cmd, [], capsys)
    return out


# -- flagregister -----------------------------------------------------------


def test_registered_flags_parse_on_kit_leaf(capsys):
    cmd = typer.main.get_command(_kit_app())
    leaf = cmd.commands["go"]
    sf = register_set_flag(leaf, "tag", "tags", FlagDisplay.BOTH)
    tf = register_text_flag(leaf, "desc", "description", FlagDisplay.BOTH)

    code, out = _main(
        cmd,
        [
            "go",
            "--tag",
            "a",
            "--tag",
            "+b",
            "--add-tag",
            "c",
            "--remove-tag",
            "a",
            "--desc",
            "hello",
            "--desc-append",
            "world",
        ],
        capsys,
    )

    assert code == 0, out
    assert "ran go" in out
    assert sf.values() == ["b", "c"]
    assert tf.value() == "hello\nworld"


def test_registered_flags_complete_by_name_on_kit_leaf(capsys, monkeypatch):
    cmd = typer.main.get_command(_kit_app())
    register_set_flag(cmd.commands["go"], "tag", "tags", FlagDisplay.BOTH)
    register_text_flag(cmd.commands["go"], "desc", "description", FlagDisplay.VERBOSE)

    out = _complete(cmd, "t go --add-t", capsys, monkeypatch)
    assert '"--add-tag":"Add to tags"' in out, out

    out = _complete(cmd, "t go --desc-app", capsys, monkeypatch)
    assert "--desc-append-inline" in out, out


def test_registered_flags_listed_in_rich_help(capsys, monkeypatch):
    """A plain typer app renders help through rich, which lists only its
    own layer's options."""
    monkeypatch.setenv("COLUMNS", "120")
    app = typer.Typer()

    @app.command()
    def go(x: str = typer.Option("", "--x")) -> None:
        pass

    cmd = typer.main.get_command(app)
    register_set_flag(cmd, "tag", "tags", FlagDisplay.BOTH)
    register_text_flag(cmd, "desc", "description", FlagDisplay.PREFIX)

    code, out = _main(cmd, ["--help"], capsys)

    assert code == 0, out
    for flag in ("--tag", "--add-tag", "--remove-tag", "--clear-tag", "--desc"):
        assert flag in out, f"{flag} missing from --help:\n{out}"


def test_registered_flags_on_click_command_stay_click():
    """Plain Click adopters keep getting ``click.Option``."""

    @click.command()
    def cmd() -> None:
        pass

    register_set_flag(cmd, "tag", "tags", FlagDisplay.BOTH)
    assert all(type(p) is click.Option for p in cmd.params)


# -- completion bridge ------------------------------------------------------


def test_completion_bridge_completes_through_kit_app(capsys, monkeypatch):
    app = _kit_app()

    @app.command("launch")
    def launch(
        mission: str = typer.Argument(
            ..., shell_complete=to_click_shell_complete(static_values("starman", "starlink"))
        ),
        orbit: str = typer.Option(
            None, "--orbit", shell_complete=to_click_shell_complete(static_values("LEO", "GTO"))
        ),
    ) -> None:
        pass

    cmd = typer.main.get_command(app)

    out = _complete(cmd, "t launch --orbit L", capsys, monkeypatch)
    assert '"LEO"' in out and "GTO" not in out, out

    out = _complete(cmd, "t launch sta", capsys, monkeypatch)
    assert '"starman"' in out and '"starlink"' in out, out


def test_completion_bridge_items_match_driving_layer():
    bridge = to_click_shell_complete(static_values("leo", "lunar"))

    typer_ctx = typer.main.get_command(_kit_app()).make_context("t", [], resilient_parsing=True)
    items = bridge(typer_ctx, None, "l")
    assert [i.value for i in items] == ["leo", "lunar"]
    assert all(type(i) is _LayerCompletionItem for i in items), [type(i) for i in items]

    click_ctx = click.Context(click.Command("c"))
    items = bridge(click_ctx, None, "l")
    assert all(type(i) is click.shell_completion.CompletionItem for i in items)


# -- alias bridge -----------------------------------------------------------


def test_alias_bridge_resolves_on_kit_app(tmp_path, capsys, monkeypatch):
    cmd = typer.main.get_command(_kit_app())
    store = tmp_path / "aliases.yaml"
    save_to(str(store), {"d": "deploy"})
    bridge_to_click(cmd, str(store))

    code, out = _main(cmd, ["d"], capsys)
    assert code == 0, out
    assert "ran deploy" in out

    out = _complete(cmd, "t ", capsys, monkeypatch)
    assert '"d"' in out and '"deploy"' in out, out


# -- guard ------------------------------------------------------------------

_CLICK_IMPORT = re.compile(r"^\s*(import click\b|from click\b)", re.MULTILINE)
_COMPAT = _SDK / "hop_top_kit" / "_click_compat.py"


@pytest.mark.parametrize(
    "root",
    [_SDK / "hop_top_kit", _SDK.parents[1] / "examples" / "spaced" / "py"],
    ids=["sdk", "spaced"],
)
def test_click_package_imported_only_by_compat_module(root: Path):
    """The click package stays behind one module; everything else takes
    Click-layer objects from it or from typer."""
    if not root.is_dir():
        pytest.skip(f"{root} not present")
    offenders = [
        str(p.relative_to(root))
        for p in sorted(root.rglob("*.py"))
        if ".venv" not in p.parts
        and not p.name.startswith("test_")
        and p != _COMPAT
        and _CLICK_IMPORT.search(p.read_text())
    ]
    assert offenders == [], offenders
