"""commands/telemetry.py — telemetry get <mission>."""
from __future__ import annotations

import json
import sys
from typing import Optional

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from data import find_mission  # noqa: E402

app = typer.Typer(help="Mission telemetry streams", add_completion=False)


def _mock_telemetry(mission_name: str) -> dict:
    import hashlib
    seed = int(hashlib.md5(mission_name.encode()).hexdigest(), 16) % 1000
    return {
        "mission": mission_name,
        "velocity_ms": 7823 + seed % 400,
        "altitude_km": 220 + seed % 180,
        "downrange_km": 1200 + seed % 2000,
        "propellant_pct": max(0, 88 - seed % 30),
        "engine_status": "nominal" if seed % 5 != 0 else "watching closely",
        "comms_signal": "strong" if seed % 4 != 0 else "intermittent",
        "g_force": round(1.2 + (seed % 20) / 10, 1),
        "source": "ground_network",
        "note": "telemetry is real-ish. the mission may not be.",
    }


@app.command("get")
def telemetry_get(
    mission: str = typer.Argument(..., help="Mission name"),
    format: Optional[str] = typer.Option(None, "--format", help="Output format: table|json|yaml"),
) -> None:
    """Retrieve live (simulated) telemetry for a mission."""
    m = find_mission(mission)
    if m is None:
        typer.echo(f"  ✗ Mission not found: {mission}", err=True)
        raise typer.Exit(1)

    telem = _mock_telemetry(m.name)
    fmt = (format or "table").lower()

    if fmt == "json":
        typer.echo(json.dumps(telem, indent=2))
        return

    if fmt == "yaml":
        import yaml  # noqa: PLC0415
        typer.echo(yaml.dump(telem, default_flow_style=False).rstrip())
        return

    # table
    typer.echo(f"""
  TELEMETRY: {m.name}  ({m.vehicle})
  {"─" * 50}
  Velocity:     {telem["velocity_ms"]} m/s
  Altitude:     {telem["altitude_km"]} km
  Downrange:    {telem["downrange_km"]} km
  Propellant:   {telem["propellant_pct"]}%
  Engines:      {telem["engine_status"]}
  Comms:        {telem["comms_signal"]}
  G-Force:      {telem["g_force"]}g
  Source:       {telem["source"]}

  Note: {telem["note"]}
""")
