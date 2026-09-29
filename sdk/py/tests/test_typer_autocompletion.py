"""``to_typer_autocompletion`` feeds kit completers into typer's
``autocompletion`` parameter.

typer deprecates ``shell_complete`` on ``typer.Option`` / ``typer.Argument``.
Each test drives a real ``create_app`` tree through typer's shell completion
the way an adopter's binary does, so it holds on every typer in the supported
range.
"""

from __future__ import annotations

import contextlib
import warnings

import typer
import typer.main

from hop_top_kit.cli import GroupConfig, HelpConfig, create_app
from hop_top_kit.completion import (
    CompletionItem,
    func_completer,
    static_completer,
    static_values,
    to_typer_autocompletion,
)

_ORBITS = static_completer(
    CompletionItem("leo", "Low Earth Orbit"),
    CompletionItem("lunar", "Moon"),
    CompletionItem("geo"),
)


def _missions(prefix: str) -> list[CompletionItem]:
    names = {"apollo": "Saturn V", "artemis": "SLS", "gemini": ""}
    return [CompletionItem(n, v) for n, v in names.items() if n.startswith(prefix)]


def _kit_app() -> typer.Typer:
    # Groups make create_app register typer's shell completion classes.
    app, _ = create_app(
        name="t",
        version="1.0.0",
        help="t",
        help_config=HelpConfig(groups=[GroupConfig(id="commands", title="COMMANDS")]),
    )

    @app.command("launch")
    def launch(
        mission: str = typer.Argument(
            ..., autocompletion=to_typer_autocompletion(func_completer(_missions))
        ),
        orbit: str = typer.Option("", "--orbit", autocompletion=to_typer_autocompletion(_ORBITS)),
    ) -> None:
        typer.echo(f"launch {mission} {orbit}")

    @app.command("abort")
    def abort() -> None:
        typer.echo("abort")

    return app


def _complete(line: str, shell: str, capsys, monkeypatch) -> str:
    monkeypatch.setenv("_T_COMPLETE", f"complete_{shell}")
    monkeypatch.setenv("_TYPER_COMPLETE_ARGS", line)
    monkeypatch.setenv("COMP_WORDS", line)
    monkeypatch.setenv("COMP_CWORD", str(len(line.split()) - (0 if line.endswith(" ") else 1)))
    with contextlib.suppress(SystemExit):
        typer.main.get_command(_kit_app()).main([], prog_name="t")
    return capsys.readouterr().out


def test_bridge_maps_descriptions_to_pairs():
    fn = to_typer_autocompletion(_ORBITS)
    assert fn("") == [("leo", "Low Earth Orbit"), ("lunar", "Moon"), "geo"]
    assert fn("g") == ["geo"]


def test_option_values_complete_with_descriptions(capsys, monkeypatch):
    out = _complete("t launch x --orbit ", "zsh", capsys, monkeypatch)
    assert '"leo":"Low Earth Orbit"' in out, out
    assert '"lunar":"Moon"' in out, out
    assert '"geo"' in out, out


def test_option_values_filter_by_prefix(capsys, monkeypatch):
    out = _complete("t launch x --orbit l", "bash", capsys, monkeypatch)
    assert out.split() == ["leo", "lunar"], out


def test_argument_values_complete_from_dynamic_completer(capsys, monkeypatch):
    out = _complete("t launch a", "zsh", capsys, monkeypatch)
    assert '"apollo":"Saturn V"' in out, out
    assert '"artemis":"SLS"' in out, out
    assert "gemini" not in out, out


def test_building_the_command_emits_no_deprecation_warning():
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        typer.main.get_command(_kit_app())
        to_typer_autocompletion(static_values("a"))("")

    deprecations = [w for w in caught if issubclass(w.category, DeprecationWarning)]
    assert deprecations == [], [str(w.message) for w in deprecations]
