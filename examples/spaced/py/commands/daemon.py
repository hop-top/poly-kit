"""commands/daemon.py — daemon list, status <id>, stop <id>, stop --all."""
from __future__ import annotations

import sys
from typing import Optional

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from hop_top_kit.bus import Bus, create_event  # noqa: E402
from data import DAEMONS, find_daemon  # noqa: E402

app = typer.Typer(help="Manage background controversy processes", add_completion=False)

# Module-level bus; set by spaced.py before command runs.
_bus: Bus | None = None

_NEW_DAEMON_NAME = "musk-response-to-this-cli"

_STATUS_ICONS = {
    "RUNNING": "●",
    "STOPPED": "○",
    "PAUSED": "◐",
}


@app.command("list")
def daemon_list() -> None:
    """List all known background controversy daemons."""
    typer.echo(f"\n  {'ID':<35}{'STATUS':<10}STARTED")
    typer.echo(f"  {'─' * 60}")
    for d in DAEMONS:
        icon = _STATUS_ICONS.get(d.status, "?")
        typer.echo(f"  {icon} {d.id:<33}{d.status:<10}{d.started}")
    typer.echo("")
    typer.echo(f"  {len(DAEMONS)} daemons running. 0 resolved. Trend: upward.")
    typer.echo("")


@app.command("status")
def daemon_status(
    daemon_id: str = typer.Argument(..., help="Daemon ID (see daemon list)"),
) -> None:
    """Display status and details for a specific daemon."""
    d = find_daemon(daemon_id)
    if d is None:
        typer.echo(f"  ✗ Daemon not found: {daemon_id}", err=True)
        raise typer.Exit(1)

    icon = _STATUS_ICONS.get(d.status, "?")
    refs_str = "\n".join(f"    → {r}" for r in d.refs)

    typer.echo(f"""
  {icon} DAEMON: {d.name}
  {"─" * 56}
  ID:       {d.id}
  Status:   {d.status}
  Started:  {d.started}

  Description:
    {d.description}

  References:
{refs_str}

  Note: {d.notes}
""")


@app.command("stop")
def daemon_stop(
    daemon_id: Optional[str] = typer.Argument(None, help="Daemon ID to stop (omit with --all)"),
    all: bool = typer.Option(False, "--all", help="Attempt to stop all daemons"),
    force: bool = typer.Option(False, "--force", help="Bypass safety guard"),
) -> None:
    """Attempt to stop a controversy daemon. Results may vary."""
    from hop_top_kit.safety import SafetyLevel, safety_guard  # noqa: PLC0415

    level = SafetyLevel.DANGEROUS if all else SafetyLevel.CAUTION
    safety_guard(level, force=force)

    if all:
        count = len(DAEMONS)
        typer.echo(f"""
  ✗ STOP FAILED: all daemons ({count}/{count})
  {"─" * 50}
  Stopped:                           0
  Still running:                     {count}
  New daemons spawned during attempt: 1
    → {_NEW_DAEMON_NAME}  [RUNNING since just now]

  Diagnosis:
    Controversies are self-sustaining processes. SIGTERM ignored.
    SIGKILL not permitted (First Amendment, probably).
    FAA, SEC, NLRB, and WSJ all notified. All still watching.

  Recommendation:
    Close this terminal. Touch grass. It will not help. But try.
""")
        return

    if not daemon_id:
        typer.echo("  ✗ Provide a daemon ID or use --all", err=True)
        raise typer.Exit(1)

    d = find_daemon(daemon_id)
    if d is None:
        typer.echo(f"  ✗ Daemon not found: {daemon_id}", err=True)
        raise typer.Exit(1)

    if _bus is not None:
        _bus.publish(create_event(
            "kit.spaced.daemon.stopped", "spaced", {"daemon": daemon_id},
        ))

    typer.echo(f"""
  ✗ STOP FAILED: {d.id}
  {"─" * 50}
  Daemon:   {d.name}
  Status:   {d.status} (unchanged)
  Signal:   SIGTERM (ignored)

  Stop attempt log:
    → sent SIGTERM... daemon tweeted in response
    → sent SIGKILL... daemon cited free speech
    → sent regulatory inquiry... added to queue (est. 18 months)
    → filed court motion... appeal pending

  The daemon will continue running.
  Refs: {d.refs[0] if d.refs else "see public record"}
""")
