"""commands/config.py — config show: XDG paths + user preferences."""
from __future__ import annotations

import os
import sys

import typer

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "../../../../sdk/py"))
from hop_top_kit.xdg import config_dir, data_dir  # noqa: E402
from hop_top_kit.config import Options, load  # noqa: E402

app = typer.Typer(help="Configuration commands.", add_completion=False)

_TOOL = "spaced"


def _val_or_dash(s: str) -> str:
    return s if s else "\u2014"


def _load_config() -> dict:
    cfg: dict = {}
    user_path = os.path.join(config_dir(_TOOL), "config.yaml")
    load(cfg, Options(user_config_path=user_path))
    return cfg


@app.command("show")
def show() -> None:
    """Show spaced configuration and paths."""
    cfg = _load_config()

    typer.echo(f"""
  ── SPACED CONFIG ──────────────────────────────────
  Default Pad      : {_val_or_dash(cfg.get("default_pad", ""))}
  Default Vehicle  : {_val_or_dash(cfg.get("default_vehicle", ""))}
  Default Orbit    : {_val_or_dash(cfg.get("default_orbit", ""))}
  Favorite Mission : {_val_or_dash(cfg.get("favorite_mission", ""))}

  ── PATHS ──────────────────────────────────────────
  Config : {config_dir(_TOOL)}
  Data   : {data_dir(_TOOL)}
""")
