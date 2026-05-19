"""
hop_top_kit — shared CLI utilities for hop-top tools (Python edition).

Requirements:
    Python >=3.11
    typer  >=0.12

Public API:
    create_app  — factory that returns a Typer app pre-configured to the
                  hop-top CLI contract.  Import from here or directly from
                  hop_top_kit.cli.
    config_dir  — XDG config directory for a named tool.
    data_dir    — XDG data directory for a named tool.
    cache_dir   — XDG cache directory for a named tool.
    state_dir   — XDG state directory for a named tool.
    must_ensure — create a directory (mode 0o750) and return its path.
    open_store   — open (or create) a SQLite KV store.
    Store        — the KV store class (hop_top_kit.sqlstore.Store).
    StoreOptions — options dataclass (hop_top_kit.sqlstore.Options).
    create_checker — factory for an upgrade Checker.
    CheckerOptions — options dataclass (hop_top_kit.upgrade.CheckerOptions).

Example:
    from hop_top_kit import create_app, config_dir, must_ensure

    app = create_app(name="mytool", version="1.2.3", help="Does things")
    cfg = must_ensure(config_dir("mytool"))
"""

from hop_top_kit import llm, telemetry, tui, uri
from hop_top_kit.cli import DARK, NEON, Disable, GlobalFlag, Palette, Theme, create_app
from hop_top_kit.telemetry import Client as TelemetryClient
from hop_top_kit.telemetry import Mode as TelemetryMode
from hop_top_kit.config import Options as ConfigOptions
from hop_top_kit.config import load as load_config
from hop_top_kit.output import Format, render
from hop_top_kit.sqlstore import Options as StoreOptions
from hop_top_kit.sqlstore import Store
from hop_top_kit.sqlstore import open as open_store
from hop_top_kit.upgrade import CheckerOptions, create_checker
from hop_top_kit.xdg import cache_dir, config_dir, data_dir, must_ensure, state_dir

__all__ = [
    "DARK",
    "NEON",
    "CheckerOptions",
    "ConfigOptions",
    "Disable",
    "Format",
    "GlobalFlag",
    "Palette",
    "Store",
    "StoreOptions",
    "TelemetryClient",
    "TelemetryMode",
    "Theme",
    "cache_dir",
    "config_dir",
    "create_app",
    "create_checker",
    "data_dir",
    "llm",
    "load_config",
    "must_ensure",
    "open_store",
    "render",
    "state_dir",
    "telemetry",
    "tui",
    "uri",
]
