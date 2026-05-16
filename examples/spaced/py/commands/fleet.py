"""commands/fleet.py — fleet list, fleet vehicle inspect <name>."""
from __future__ import annotations

import sys
from typing import Optional

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from data import VEHICLES, find_vehicle  # noqa: E402

app = typer.Typer(help="Inspect the SpaceX vehicle fleet", add_completion=False)
vehicle_app = typer.Typer(help="Vehicle-level commands.", add_completion=False)
app.add_typer(vehicle_app, name="vehicle")

_RULER = "─" * 58


@app.command("list")
def fleet_list() -> None:
    """List all vehicles in the SpaceX fleet."""
    typer.echo(f"\n  {'NAME':<18}{'STATUS':<12}{'FLIGHTS':<10}{'REUSABLE':<10}PAYLOAD (LEO)")
    typer.echo(f"  {_RULER}")
    for v in VEHICLES:
        reusable = "yes" if v.reusable else "no"
        typer.echo(f"  {v.name:<18}{v.status:<12}{v.flights:<10}{reusable:<10}{v.payload_leo_kg:,} kg")
    typer.echo("")


@vehicle_app.command("inspect")
def vehicle_inspect(
    name: str = typer.Argument(..., help="Vehicle name (case-insensitive)"),
    systems: Optional[str] = typer.Option(
        None, "--systems", help="Comma-separated system filters (e.g. propulsion,recovery)"
    ),
) -> None:
    """Inspect a vehicle's specs and systems."""
    v = find_vehicle(name)
    if v is None:
        typer.echo(f"  ✗ Vehicle not found: {name}", err=True)
        raise typer.Exit(1)

    system_filter = [s.strip().lower() for s in systems.split(",")] if systems else []

    reusable_str = "yes" if v.reusable else "no"
    typer.echo(f"""
  VEHICLE: {v.name}
  {"─" * 50}
  Status:       {v.status}
  First Flight: {v.first_flight}
  Total Flights:{v.flights}
  Payload (LEO):{v.payload_leo_kg:,} kg
  Reusable:     {reusable_str}

  Note: {v.notes}
""")

    if v.systems:
        typer.echo("  Systems:")
        for sys_name, sys_desc in v.systems.items():
            if system_filter and sys_name.lower() not in system_filter:
                continue
            typer.echo(f"    {sys_name:<18}{sys_desc}")
        typer.echo("")
