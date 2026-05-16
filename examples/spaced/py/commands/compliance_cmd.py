"""commands/compliance_cmd.py — 12-factor AI CLI compliance."""
from __future__ import annotations

import os
import sys

import typer

sys.path.insert(
    0,
    os.path.join(os.path.dirname(__file__), "../../../../sdk/py"),
)
from hop_top_kit.compliance import (  # noqa: E402
    run,
    format_report,
)

app = typer.Typer(
    help="12-factor compliance checks.", add_completion=False,
)


@app.command("compliance")
def compliance(
    static: bool = typer.Option(
        False, "--static", help="Run static checks only",
    ),
    fmt: str = typer.Option(
        "text", "--format", help="Output format (text, json)",
    ),
    spec: str = typer.Option(
        "", "--spec", help="Path to toolspec YAML",
    ),
) -> None:
    """Run 12-factor AI CLI compliance checks."""
    spec_path = spec or os.path.join(
        os.path.dirname(__file__),
        "../../spaced.toolspec.yaml",
    )

    binary_path = "" if static else sys.executable
    report = run(
        "" if static else binary_path,
        spec_path,
    )
    print(format_report(report, fmt))
