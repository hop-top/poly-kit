"""commands/starship.py — starship status, starship history."""
from __future__ import annotations

import sys

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from data import MISSIONS, find_vehicle  # noqa: E402

app = typer.Typer(help="Starship program status and history", add_completion=False)

_HISTORY_SUMMARY = [
    ("SN1",  "2020-02-28", "Pressure test → RUD. Tradition established."),
    ("SN4",  "2020-06-04", "Static fire. Then: explosion. Not sequential."),
    ("SN5",  "2020-08-04", "150m hop. The one that worked. Much rejoicing."),
    ("SN8",  "2020-12-09", "Belly flop. Landed. Then exploded. Nominal."),
    ("SN9",  "2021-02-02", "SN8 but slightly more on fire. Also nominal."),
    ("SN10", "2021-03-03", "Landed. Then: detonated 8 min later. Nominaler."),
    ("SN11", "2021-03-30", "Flew in fog. Exploded in fog. Mystery."),
    ("SN15", "2021-05-05", "Landed. Stayed landed. Historic understatement."),
    ("IFT-1","2023-04-20", "Full stack. Stage sep failed. RUD. 420. Noted."),
    ("IFT-2","2023-11-18", "Stage sep ok. Vehicle lost. Progress."),
    ("IFT-3","2024-03-14", "Reached space. Reentry: partial. Pi Day."),
    ("IFT-4","2024-06-06", "Both survived. Ocean splashdown. First real win."),
    ("IFT-5","2024-10-13", "Mechazilla catch. Jaws: dropped. Globally."),
    ("IFT-6","2025-01-16", "Booster caught. Ship lost. 1.5/2. Partial."),
]


@app.command("status")
def starship_status() -> None:
    """Current Starship program status."""
    v = find_vehicle("Starship")
    typer.echo(f"""
  STARSHIP PROGRAM STATUS
  {"─" * 50}
  Vehicle:        Starship / Super Heavy
  Status:         {v.status if v else "TESTING"}
  Flights:        {v.flights if v else 6} (IFT-1 through IFT-6)
  Payload (LEO):  {v.payload_leo_kg:,} kg (design; not yet demonstrated)
  Reusability:    Fully reusable (goal); mostly surviving (current)
  Mechazilla:     Operational. It catches rockets. With arms.

  Current milestones:
    ✓ Stage separation
    ✓ Orbital velocity (approx)
    ✓ Controlled reentry (partial)
    ✓ Ocean splashdown (ship)
    ✓ Mechazilla booster catch
    ✗ Propellant transfer (upcoming)
    ✗ Lunar/Mars missions (pending several prerequisites)

  FAA status:  in contact (frequently)
  Elon mood:   tweets suggest: optimistic
  Note: {v.notes if v else "Largest rocket ever. Mostly works now."}
""")


@app.command("history")
def starship_history() -> None:
    """Full Starship flight/test history."""
    typer.echo(f"\n  {'VEHICLE':<10}{'DATE':<14}NOTES")
    typer.echo(f"  {'─' * 60}")
    for vehicle, date, notes in _HISTORY_SUMMARY:
        typer.echo(f"  {vehicle:<10}{date:<14}{notes}")
    typer.echo("")
    typer.echo("  * 'nominal' is a technical term. We use it loosely.")
    typer.echo("")
