"""commands/abort.py — abort <mission> --reason."""
from __future__ import annotations

import sys
from typing import Optional

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from data import find_mission  # noqa: E402

app = typer.Typer(help="Mission abort commands.", add_completion=False)

_ABORT_CODES = [
    "HOLD_HOLD_HOLD",
    "LCC_ABORT",
    "RANGE_ABORT",
    "VEHICLE_ABORT",
    "PROPELLANT_ABORT",
    "GUIDANCE_ABORT",
]


@app.command("abort")
def abort(
    mission: str = typer.Argument(..., help="Mission name"),
    reason: Optional[str] = typer.Option(
        None, "--reason", help="Abort reason (for the record)"
    ),
) -> None:
    """Abort a mission. Abort codes will be filed with the FAA."""
    m = find_mission(mission)
    if m is None:
        typer.echo(f"  ✗ Mission not found: {mission}", err=True)
        raise typer.Exit(1)

    import time
    code = _ABORT_CODES[int(time.time()) % len(_ABORT_CODES)]
    stated_reason = reason or "operator discretion (tweet-related)"

    typer.echo(f"""
  ✗ ABORT: {m.name}
  {"─" * 50}
  Vehicle:      {m.vehicle}
  Abort Code:   {code}
  Reason:       {stated_reason}
  FAA notified: yes (they were already watching)
  Elon notified: he'll find out via Twitter

  Crew status:   nominal (if applicable)
  Pad status:    clearing now
  Next window:   TBD (weather, FAA, vibes)

  This abort has been logged. The log will be ignored.
  A tweet will be sent regardless.
""")
