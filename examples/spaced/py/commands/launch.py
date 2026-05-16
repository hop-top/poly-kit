"""commands/launch.py — launch <mission> with flags."""
from __future__ import annotations

import json
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional

import click
import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from hop_top_kit.bus import Bus, create_event  # noqa: E402
from hop_top_kit.log import create_logger  # noqa: E402
from hop_top_kit.setflag import SetFlag  # noqa: E402
from hop_top_kit.wizard import (  # noqa: E402
    Wizard, Option as WizOpt, text_input, select, confirm, summary,
)
from hop_top_kit.completion import (  # noqa: E402
    CompletionItem, CompletionRegistry, static_values, func_completer,
    to_click_shell_complete,
)
from data import find_mission, MISSIONS  # noqa: E402

log = None  # created lazily in command to read --quiet

# Module-level bus; set by spaced.py before command runs.
_bus: Bus | None = None

app = typer.Typer(help="Launch sequence commands.", add_completion=False)

# ---------------------------------------------------------------------------
# Completion registry for launch command
# ---------------------------------------------------------------------------

_orbit_completer = static_values("LEO", "GTO", "GEO", "SSO", "Heliocentric")


def _mission_complete(prefix: str) -> list[CompletionItem]:
    low = prefix.lower()
    return [
        CompletionItem(m.name, m.vehicle)
        for m in MISSIONS
        if m.name.lower().startswith(low)
    ]


launch_completions = CompletionRegistry()
launch_completions.register("--orbit", _orbit_completer)
launch_completions.register_arg("launch", 0, func_completer(_mission_complete))

# Module-level SetFlag instance; shared across callback + command.
_tags = SetFlag()


def _tag_callback(ctx: typer.Context, param: typer.CallbackParam, value: tuple) -> list[str]:
    # Typer passes all --tag values as a tuple; process each through SetFlag.
    for v in value:
        _tags.set(v)
    return _tags.values()


@app.command("launch")
def launch(
    mission: str = typer.Argument(
        ..., help="Mission name",
        shell_complete=to_click_shell_complete(
            launch_completions.for_arg("launch", 0),
        ),
    ),
    payload: Optional[str] = typer.Option(
        None, "--payload", help="Comma-separated payload items (e.g. cargo,crew)"
    ),
    orbit: Optional[str] = typer.Option(
        None, "--orbit", help="Target orbit (e.g. LEO, GTO)",
        shell_complete=to_click_shell_complete(_orbit_completer),
    ),
    dry_run: bool = typer.Option(False, "--dry-run", help="Simulate launch without committing"),
    output: Optional[Path] = typer.Option(
        None, "-o", "--output", help="Write launch report JSON to file"
    ),
    tag: Optional[list[str]] = typer.Option(
        None, "--tag", callback=_tag_callback, expose_value=False,
        help="Launch tags (+append/-remove/=replace)",
    ),
) -> None:
    """Initiate launch sequence for a mission."""
    global log
    quiet = False
    click_ctx = click.get_current_context(silent=True)
    while click_ctx:
        if "quiet" in click_ctx.params:
            quiet = bool(click_ctx.params["quiet"])
            break
        click_ctx = click_ctx.parent
    log = create_logger(quiet=quiet)

    if _bus is not None:
        _bus.publish(create_event(
            "kit.spaced.launch.initiated", "spaced", {"mission": mission},
        ))
    log.info("resolving mission", name=mission)
    m = find_mission(mission)
    if m is None:
        log.error("mission not found", name=mission)
        typer.echo(f"  ✗ Mission not found: {mission}", err=True)
        raise typer.Exit(1)

    payload_items = [p.strip() for p in payload.split(",")] if payload else [m.payload]
    target_orbit = orbit or m.orbit

    tag_list = _tags.values()
    tag_str = ", ".join(tag_list) if tag_list else "none"

    log.info("launch parameters", vehicle=m.vehicle, orbit=target_orbit, tags=tag_str)

    if dry_run:
        log.warn("dry run mode — no actual launch")
        typer.echo(f"""
  ── DRY RUN ────────────────────────────────────────────────────────
  Mission  : {m.name}
  Vehicle  : {m.vehicle}
  Orbit    : {target_orbit}
  Payload  : {", ".join(payload_items)}
  Tags     : {tag_str}
  Status   : Would have launched. Probably would have been fine.
  ──────────────────────────────────────────────────────────────────

  Dry run complete. No actual rockets were harmed.""")
        return

    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")
    typer.echo(f"""
  ▶ LAUNCH SEQUENCE INITIATED: {m.name}
  Vehicle  : {m.vehicle}
  Orbit    : {target_orbit}
  Payload  : {", ".join(payload_items)}
  Tags     : {tag_str}
  T-0      : {now}
  Outcome  : {m.outcome}
  Note     : {m.notes}
""")

    report = {
        "mission": m.name,
        "vehicle": m.vehicle,
        "orbit": target_orbit,
        "payload": payload_items,
        "tags": tag_list,
        "outcome": m.outcome,
        "note": m.notes,
        "ts": datetime.now(timezone.utc).isoformat(),
    }

    if output:
        output.write_text(json.dumps(report, indent=2))
        typer.echo(f"  Report written to {output}")

    if _bus is not None:
        _bus.publish(create_event("kit.spaced.launch.completed", "spaced", report))


def run_launch_wizard() -> None:
    """Headless wizard demo -- same steps/defaults as Go."""
    global log
    if log is None:
        log = create_logger(quiet=False)
    orbit_opts = [
        WizOpt(value="leo", label="LEO", description="Low Earth Orbit"),
        WizOpt(value="geo", label="GEO", description="Geostationary"),
        WizOpt(value="lunar", label="Lunar", description="Trans-lunar injection"),
        WizOpt(value="helio", label="Helio", description="Heliocentric"),
        WizOpt(value="tbd", label="TBD", description="To be determined"),
    ]

    w = Wizard(
        text_input("mission", "Mission name").with_required(),
        select("orbit", "Target orbit", orbit_opts),
        text_input("payload", "Payload manifest"),
        confirm("dry_run", "Dry run?").with_default(False),
        summary("Launch parameters"),
    )

    defaults: dict = {
        "mission": "Starlink-42",
        "orbit": "leo",
        "payload": "60x Starlink v2 Mini",
        "dry_run": True,
    }

    log.info("wizard: advancing through steps with defaults")
    while not w.done():
        s = w.current()
        if s is None:
            break

        val = defaults.get(s.key)
        if val is None:
            # Summary step -- advance with None.
            w.advance(None)
            continue
        _, err = w.advance(val)
        if err is not None:
            typer.echo(
                f'  error: wizard step "{s.key}": {err}', err=True,
            )
            raise typer.Exit(1)

    results = w.results()
    print()
    print("  ── WIZARD RESULTS ─────────────────────────────────────────────")
    for k, v in results.items():
        print(f"  {k:<12}: {v}")
    print("  ───────────────────────────────────────────────────────────────")
    print()
    print("  Wizard complete. In a real TUI, these would drive the launch.")
