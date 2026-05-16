"""commands/competitor.py — competitor compare <name>."""
from __future__ import annotations

import sys
from typing import Optional

import typer

sys.path.insert(0, __import__("os").path.join(__import__("os").path.dirname(__file__), "../../../../sdk/py"))
from data import COMPETITORS, find_competitor  # noqa: E402

app = typer.Typer(help="Compare SpaceX against its competitors", add_completion=False)


@app.command("compare")
def competitor_compare(
    name: str = typer.Argument(..., help="Competitor name (Boeing, Blue Origin, ULA, ...)"),
    metric: Optional[str] = typer.Option(
        None, "--metric", help="Comma-separated metrics to show (e.g. reliability,cost_per_kg)"
    ),
) -> None:
    """Compare a competitor against SpaceX. Spoiler: SpaceX wins."""
    c = find_competitor(name)
    if c is None:
        available = ", ".join(comp.name for comp in COMPETITORS)
        typer.echo(f"  ✗ Competitor not found: {name}", err=True)
        typer.echo(f"  Available: {available}", err=True)
        raise typer.Exit(1)

    metric_filter = [m.strip().lower() for m in metric.split(",")] if metric else []

    rockets_str = ", ".join(c.rockets)
    crewed_str = "yes" if c.crewed else "no"

    typer.echo(f"""
  COMPETITOR ANALYSIS: {c.name}
  {"─" * 56}
  Founded:    {c.founded}
  Status:     {c.status}
  Tagline:    {c.tagline}
  Rockets:    {rockets_str}
  Crewed:     {crewed_str}

  Notable failure:
    {c.notable_failure}

  Metrics (vs SpaceX: dominant):
""")

    for key, val in c.metrics.items():
        if metric_filter and key.lower() not in metric_filter:
            continue
        typer.echo(f"    {key:<28}{val}")

    typer.echo(f"""
  SpaceX comparison:
    Launches/year:  50+ (SpaceX) vs. {_cadence_guess(c.name)}
    Cost/kg (LEO):  ~$2,700 (F9) — competitors: more
    Reusability:    yes — competitors: mostly no

  Verdict: {_verdict(c.name)}
""")


def _cadence_guess(name: str) -> str:
    return {
        "Boeing": "aspirational (Starliner still on pad, spiritually)",
        "Blue Origin": "~6/year (improving)",
        "Virgin Galactic": "0 (suspended)",
        "ULA": "~6/year",
        "Roscosmos": "~15/year (declining)",
    }.get(name, "fewer")


def _verdict(name: str) -> str:
    return {
        "Boeing": "Historically great. Currently: cautionary tale. Root cause: culture.",
        "Blue Origin": "Better than headline. Worse than SpaceX. Bezos still funding.",
        "Virgin Galactic": "Pioneered space tourism concept. Currently: not operating it.",
        "ULA": "Reliable, expensive, government-dependent. Vulcan: the sequel.",
        "Roscosmos": "Gagarin would not recognize it. Sanctions didn't help.",
    }.get(name, "Competing. That's something.")
