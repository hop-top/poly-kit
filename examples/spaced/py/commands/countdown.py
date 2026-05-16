"""commands/countdown.py — countdown <mission>."""
from __future__ import annotations

import sys

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from data import find_mission  # noqa: E402

app = typer.Typer(help="Launch countdown display.", add_completion=False)

_HOLDS = [
    "LOX loading anomaly. Hold. Waiting on LCC.",
    "FAA range clear delay. Again. As expected.",
    "Elon tweeted something. Legal reviewing. Hold.",
    "Upper-level winds nominal. Lower-level winds also nominal. Hold anyway.",
    "Fueling complete. Holds complete. We think. Maybe one more.",
    "T-0 reached. Anomaly detected. Reset. Probably fine.",
]


@app.command("countdown")
def countdown(
    mission: str = typer.Argument(..., help="Mission name"),
) -> None:
    """Display launch countdown sequence for a mission."""
    m = find_mission(mission)
    if m is None:
        typer.echo(f"  ✗ Mission not found: {mission}", err=True)
        raise typer.Exit(1)

    import time
    hold = _HOLDS[int(time.time()) % len(_HOLDS)]

    typer.echo(f"""
  COUNTDOWN: {m.name}
  Vehicle: {m.vehicle}  |  Date: {m.date}
  {"─" * 50}

  T-60:00  Range safety: go. Weather: tolerable. Engineers: nervous.
  T-45:00  LOX loading initiated. Frost forming. Beautiful, actually.
  T-30:00  LH2 (if applicable) nominal. Pad crew retreating.
  T-20:00  Automated sequence handoff. Human override: still possible.
  T-10:00  Vehicle on internal power.
  T-05:00  Engine chill. Everyone holds breath.
  T-01:00  Terminal count. Prayers, tweets, and profanity commence.
  T-00:10  ...
  T-00:05  ...
  T-00:03  ...
  T-00:01  ...
  HOLD     {hold}
           (Rescheduled. Next window: TBD. Weather: cooperating less.)

  Outcome (historical): {m.outcome}
""")
