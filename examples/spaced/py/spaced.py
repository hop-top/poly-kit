#!/usr/bin/env python3
"""
spaced — satirical SpaceX CLI historian and hop-top-kit parity test vehicle.

Usage:
    python examples/spaced/py/spaced.py [OPTIONS] COMMAND [ARGS]...
"""
from __future__ import annotations

import json
import os
import sys

# Inject sdk/py/ into path so hop_top_kit is importable without install.
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../../sdk/py"))
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../../../../../uri-schemes/py/src"))
# Also ensure commands can find data.py via their own sys.path injection.
sys.path.insert(0, os.path.dirname(__file__))

import typer  # noqa: E402
from hop_top_kit.alias import bridge_to_click  # noqa: E402
from hop_top_kit.bus import create_bus  # noqa: E402
from hop_top_kit.cli import create_app, GroupConfig, HelpConfig, set_command_group  # noqa: E402

from commands.alias import app as alias_app  # noqa: E402
from commands.abort import app as abort_app  # noqa: E402
from commands.competitor import app as competitor_app  # noqa: E402
from commands.config import app as config_app  # noqa: E402
from commands.countdown import app as countdown_app  # noqa: E402
from commands.daemon import app as daemon_app  # noqa: E402
from commands.elon import app as elon_app  # noqa: E402
from commands.fleet import app as fleet_app  # noqa: E402
from commands.ipo import app as ipo_app  # noqa: E402
from commands.launch import app as launch_app, _tags as _launch_tags  # noqa: E402
from commands.mission import app as mission_app  # noqa: E402
from commands.starship import app as starship_app  # noqa: E402
from commands.telemetry import app as telemetry_app  # noqa: E402
from commands.uri_cmd import app as uri_app  # noqa: E402

_DISCLAIMER = """\
Not affiliated with, endorsed by, or in any way authorized by SpaceX,
Elon Musk, DOGE, NASA, the FAA, or the Starman mannequin currently past Mars.
We would, however, accept a sponsorship (https://github.com/sponsors/hop-top).
Cash, Starlink credits, or a ride on the next Crew Dragon all acceptable.\
"""

app, theme = create_app(
    name="spaced",
    version="0.1.0",
    help="satirical SpaceX CLI historian — every launch, every RUD, every daemon",
    help_config=HelpConfig(
        disclaimer=_DISCLAIMER,
        groups=[
            GroupConfig(id="commands", title="COMMANDS"),
            GroupConfig(id="management", title="MANAGEMENT", hidden=True),
        ],
    ),
)

set_command_group("alias", "management")
set_command_group("config", "management")
set_command_group("toolspec", "management")
set_command_group("compliance", "management")
set_command_group("uri", "management")

# ---------------------------------------------------------------------------
# Bus — in-memory pub/sub matching Go reference
# ---------------------------------------------------------------------------

bus = create_bus()

bus.subscribe("kit.spaced.launch.#", lambda e: print(
    f"  [bus] {e.topic} → {json.dumps(e.payload)}"
))
bus.subscribe("kit.spaced.daemon.#", lambda e: print(
    f"  [bus] {e.topic} → {json.dumps(e.payload)}"
))

# Wire bus into command modules.
from commands import launch as _launch_mod, daemon as _daemon_mod  # noqa: E402
_launch_mod._bus = bus
_daemon_mod._bus = bus

# ---------------------------------------------------------------------------
# Register subcommand groups in alphabetical order
# ---------------------------------------------------------------------------

app.add_typer(alias_app, name="alias")
app.add_typer(competitor_app, name="competitor")
app.add_typer(config_app, name="config")
app.add_typer(daemon_app, name="daemon")
app.add_typer(elon_app, name="elon")
app.add_typer(fleet_app, name="fleet")
app.add_typer(ipo_app, name="ipo")
app.add_typer(mission_app, name="mission")
app.add_typer(starship_app, name="starship")
app.add_typer(telemetry_app, name="telemetry")
app.add_typer(uri_app, name="uri")


# ---------------------------------------------------------------------------
# Single-command groups (Typer wraps single @app.command into group)
# The launch, abort, countdown commands are registered with their command
# name matching the Typer app's single command — add directly.
# ---------------------------------------------------------------------------

def _tag_callback(ctx: typer.Context, param: typer.CallbackParam, value: tuple) -> list[str]:
    # Typer passes all --tag values as a tuple; process each through SetFlag.
    for v in (value or ()):
        _launch_tags.set(v)
    return _launch_tags.values()


@app.command("launch")
def launch_cmd(
    mission: str | None = typer.Argument(None, help="Mission name"),
    payload: str | None = typer.Option(
        None, "--payload", help="Comma-separated payload (e.g. cargo,crew)"
    ),
    orbit: str | None = typer.Option(None, "--orbit", help="Target orbit"),
    dry_run: bool = typer.Option(False, "--dry-run", help="Simulate launch without committing"),
    output: str | None = typer.Option(
        None, "-o", "--output", help="Write launch report JSON to file"
    ),
    interactive: bool = typer.Option(
        False, "-i", "--interactive", help="Run interactive launch wizard"
    ),
    tag: list[str] | None = typer.Option(
        None, "--tag", callback=_tag_callback, expose_value=False,
        help="Launch tags (+append/-remove/=replace)",
    ),
) -> None:
    """Launch a mission"""
    if interactive:
        from commands.launch import run_launch_wizard
        run_launch_wizard()
        return

    if not mission:
        typer.echo(
            "  error: mission name required (or use --interactive)",
            err=True,
        )
        raise typer.Exit(1)

    from pathlib import Path
    from commands.launch import launch as _launch
    _launch(
        mission=mission,
        payload=payload,
        orbit=orbit,
        dry_run=dry_run,
        output=Path(output) if output else None,
    )


@app.command("abort")
def abort_cmd(
    mission: str = typer.Argument(..., help="Mission name"),
    reason: str | None = typer.Option(None, "--reason", help="Abort reason"),
) -> None:
    """Abort a mission"""
    from commands.abort import abort as _abort
    _abort(mission=mission, reason=reason)


@app.command("countdown")
def countdown_cmd(
    mission: str = typer.Argument(..., help="Mission name"),
) -> None:
    """Show countdown status for a mission"""
    from commands.countdown import countdown as _countdown
    _countdown(mission=mission)


@app.command("status")
def status_cmd(
    fmt: str = typer.Option(
        "text", "--format", help="Output format (text, json)",
    ),
) -> None:
    """Show spaced runtime status."""
    report = {
        "name": "spaced",
        "version": "0.1.0",
        "runtime": "python",
        "status": "ok",
        "env": sorted(key for key in os.environ if key.startswith("SPACED_")),
    }
    if fmt == "json":
        typer.echo(json.dumps(report, indent=2))
        return
    typer.echo(f"""
  -- SPACED STATUS ----------------------------------
  Name    : {report["name"]}
  Version : {report["version"]}
  Runtime : {report["runtime"]}
  Status  : {report["status"]}
  Env     : {", ".join(report["env"]) if report["env"] else "-"}
""")


@app.command("toolspec")
def toolspec_cmd() -> None:
    """Load and validate spaced.toolspec.yaml"""
    from commands.toolspec_cmd import toolspec as _toolspec
    _toolspec()


@app.command("compliance")
def compliance_cmd(
    static: bool = typer.Option(
        False, "--static", help="Run static checks only",
    ),
    fmt: str = typer.Option(
        "text", "--format", help="Output format (text, json)",
    ),
    spec: str = typer.Option(
        "", "--spec", help="Path to toolspec YAML",
    ),
) -> None:
    """Run 12-factor AI CLI compliance checks"""
    from commands.compliance_cmd import compliance as _compliance
    _compliance(static=static, fmt=fmt, spec=spec)


if __name__ == "__main__":
    import typer.main as _typer_main

    _cmd = _typer_main.get_command(app)

    # Sort subcommands alphabetically so help output order matches Go reference.
    if hasattr(_cmd, "commands") and isinstance(_cmd.commands, dict):
        _cmd.commands = dict(sorted(_cmd.commands.items()))

    # Bridge aliases into Click's get_command/list_commands so tab
    # completion resolves them natively.
    _alias_path = os.path.expanduser(
        "~/.config/spaced/config.yaml"
    )
    bridge_to_click(_cmd, _alias_path)

    # prog_name="spaced" ensures usage line shows "spaced" not "spaced.py".
    _cmd.main(prog_name="spaced", args=sys.argv[1:])
