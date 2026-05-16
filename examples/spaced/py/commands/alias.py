"""commands/alias.py — alias list/add/remove management commands."""
from __future__ import annotations

import os
import sys

import typer

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../../../sdk/py"))
from hop_top_kit.alias import load_from, save_to  # noqa: E402
from hop_top_kit.xdg import config_dir  # noqa: E402

app = typer.Typer(help="Manage command aliases.", add_completion=False)

_TOOL = "spaced"


def _config_path() -> str:
    return os.path.join(config_dir(_TOOL), "config.yaml")


@app.command("list")
def alias_list() -> None:
    """List all user-defined aliases."""
    try:
        aliases = load_from(_config_path())
    except FileNotFoundError:
        aliases = {}

    if not aliases:
        typer.echo("  No aliases defined.")
        return

    width = max(len(k) for k in aliases)
    for name in sorted(aliases):
        typer.echo(f"  {name:<{width}}  →  {aliases[name]}")


@app.command("add")
def alias_add(
    name: str = typer.Argument(..., help="Alias name"),
    target: str = typer.Argument(..., help="Target command expansion"),
) -> None:
    """Add or update an alias."""
    p = _config_path()
    try:
        aliases = load_from(p)
    except FileNotFoundError:
        aliases = {}

    aliases[name] = target
    save_to(p, aliases)
    typer.echo(f"  alias {name!r} → {target!r}")


@app.command("remove")
def alias_remove(
    name: str = typer.Argument(..., help="Alias name to remove"),
) -> None:
    """Remove an alias."""
    p = _config_path()
    try:
        aliases = load_from(p)
    except FileNotFoundError:
        aliases = {}

    if name not in aliases:
        typer.echo(f"  alias {name!r} not found", err=True)
        raise typer.Exit(1)

    del aliases[name]
    save_to(p, aliases)
    typer.echo(f"  removed alias {name!r}")
