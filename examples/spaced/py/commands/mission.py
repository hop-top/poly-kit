"""commands/mission.py — mission list, inspect, search."""
from __future__ import annotations

import json
import os
import sys
from typing import Optional

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from data import MISSIONS, find_mission  # noqa: E402 — relative import via sys.path

app = typer.Typer(help="Query mission history", add_completion=False)

_COL_SEP = "  "
_RULER = "─" * 68


def _outcome_icon(outcome: str) -> str:
    return {"SUCCESS": "✓", "RUD*": "✗", "PARTIAL": "~", "CANCELLED": "✗"}.get(outcome, "?")


def _table_row(name: str, vehicle: str, date: str, outcome: str, mood: str) -> str:
    return (
        f"  {name:<18}{vehicle:<15}{date:<12}{outcome:<10}{mood}"
    )


@app.command("list")
def mission_list(
    format: Optional[str] = typer.Option(None, "--format", help="Output format: table|json|yaml"),
) -> None:
    """List all known SpaceX missions."""
    fmt = (format or "table").lower()

    if fmt == "json":
        from datetime import datetime, timezone  # noqa: PLC0415
        from hop_top_kit.provenance import Provenance, with_provenance  # noqa: PLC0415

        data = [
            {
                "name": m.name,
                "vehicle": m.vehicle,
                "date": m.date,
                "outcome": m.outcome,
                "market_mood": m.market_mood,
                "orbit": m.orbit,
                "payload": m.payload,
            }
            for m in MISSIONS
        ]
        prov = Provenance(
            source="spaced-cli",
            timestamp=datetime.now(timezone.utc).isoformat(),
            method="mission.list",
        )
        typer.echo(json.dumps(with_provenance(data, prov), indent=2))
        return

    if fmt == "yaml":
        import yaml  # noqa: PLC0415
        data = [
            {
                "name": m.name,
                "vehicle": m.vehicle,
                "date": m.date,
                "outcome": m.outcome,
                "market_mood": m.market_mood,
                "orbit": m.orbit,
                "payload": m.payload,
            }
            for m in MISSIONS
        ]
        typer.echo(yaml.dump(data, default_flow_style=False).rstrip())
        return

    # table (default)
    header = _table_row("MISSION", "VEHICLE", "DATE", "OUTCOME", "MARKET MOOD")
    typer.echo(f"\n{header}")
    typer.echo(f"  {_RULER}")
    for m in MISSIONS:
        typer.echo(_table_row(m.name, m.vehicle, m.date, m.outcome, m.market_mood))
    typer.echo("")
    typer.echo("  * RUD = Rapid Unscheduled Disassembly  (company terminology, not ours)")
    typer.echo("")


@app.command("inspect")
def mission_inspect(
    name: str = typer.Argument(..., help="Mission name (case-insensitive, prefix ok)"),
) -> None:
    """Inspect a single mission in detail."""
    m = find_mission(name)
    if m is None:
        from hop_top_kit.errcorrect import CorrectedError  # noqa: PLC0415

        available = [ms.name for ms in MISSIONS]
        # prefix matches for alternatives
        alts = [n for n in available if n.lower().startswith(name.lower())]
        err = CorrectedError(
            code="NOT_FOUND",
            message=f"mission not found: {name}",
            cause="no mission matches the given name",
            fix="use 'mission list' to see available missions",
            alternatives=alts or available[:5],
        )
        typer.echo(err.format_terminal(no_color=os.environ.get("NO_COLOR") is not None), err=True)
        raise typer.Exit(1)

    icon = _outcome_icon(m.outcome)
    typer.echo(f"""
  Mission:    {m.name}
  Vehicle:    {m.vehicle}
  Date:       {m.date}
  Orbit:      {m.orbit}
  Payload:    {m.payload}
  Outcome:    {icon} {m.outcome}
  Market:     {m.market_mood}

  Notes:
    {m.notes}
""")


@app.command("search")
def mission_search(
    query: str = typer.Argument(..., help="Search term (name, vehicle, outcome)"),
) -> None:
    """Search missions by name, vehicle, or outcome."""
    q = query.lower()
    results = [
        m for m in MISSIONS
        if q in m.name.lower() or q in m.vehicle.lower() or q in m.outcome.lower()
        or q in m.orbit.lower() or q in m.payload.lower()
    ]
    if not results:
        typer.echo(f"  No missions matched: {query!r}", err=True)
        raise typer.Exit(1)

    typer.echo(f"\n  Found {len(results)} mission(s) matching {query!r}:\n")
    header = _table_row("MISSION", "VEHICLE", "DATE", "OUTCOME", "MARKET MOOD")
    typer.echo(f"{header}")
    typer.echo(f"  {_RULER}")
    for m in results:
        typer.echo(_table_row(m.name, m.vehicle, m.date, m.outcome, m.market_mood))
    typer.echo("")
