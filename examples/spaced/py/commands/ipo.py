"""commands/ipo.py — ipo status."""
from __future__ import annotations

import time
import typer

app = typer.Typer(help="Spacex IPO status tracker", add_completion=False)

_IPO_UPDATES = [
    "Elon said 'maybe' in 2025. Analysts noted this is consistent with 'maybe' in 2024.",
    "Still private. Starship must reach orbit first. Progress: see `starship status`.",
    "Morgan Stanley prepared deck. Deck updated annually. IPO: not annual.",
    "Valuation: $200B+ (private). IPO would be largest ever. That's the plan.",
    "Elon: 'SpaceX will IPO Starlink first'. Starlink IPO: also TBD.",
]


@app.command("status")
def ipo_status() -> None:
    """Display SpaceX IPO status (current: none)."""
    idx = int(time.time()) % len(_IPO_UPDATES)
    update = _IPO_UPDATES[idx]

    typer.echo(f"""
  SPACEX IPO STATUS
  {"─" * 50}
  Company:          Space Exploration Technologies Corp. (SpaceX)
  Type:             Private
  IPO status:       NOT FILED
  Exchange:         N/A
  Ticker:           $SPACE (reserved in hearts, not markets)
  Valuation:        ~$210B (Dec 2024 secondary round)
  Last funding:     Series Y (yes, Y — they ran out of letters)
  Investors:        Google, Fidelity, various sovereign wealth funds

  Starlink IPO:
    Status:         also not filed
    Elon comment:   "eventually" (multiple occasions, multiple years)
    SEC filings:    0

  Latest update:
    {update}

  Projection:
    Q1 optimism → Q2 delay → Q3 "focusing on Mars" → repeat

  Note: This is not financial advice. It is, however, accurate.
""")
