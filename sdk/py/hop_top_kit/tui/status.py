"""
hop_top_kit.tui.status — prefixed status line renderer.

Mirrors Go's tui.Status info/success/error/warn kinds.
Symbols loaded from tui/parity.json (single source of truth).
"""

from __future__ import annotations

import json
import pathlib

from rich.console import Console
from rich.text import Text

from hop_top_kit.cli import Theme

_PARITY = json.loads(
    (pathlib.Path(__file__).parents[4] / "contracts" / "parity" / "parity.json").read_text()
)
_SYMBOLS: dict[str, str] = _PARITY["status"]["symbols"]


def status(theme: Theme, text: str, kind: str = "info") -> str:
    """Render a prefixed status line.

    Args:
        theme: Theme providing semantic colors.
        text:  Message body.
        kind:  One of ``"info"``, ``"success"``, ``"error"``, ``"warn"``.
               Unknown values fall back to ``"info"``.

    Returns:
        ANSI-escaped string with a colored prefix symbol + message.
    """
    color_map: dict[str, str] = {
        "info": theme.accent,
        "success": theme.success,
        "error": theme.error,
        "warn": theme.secondary,
    }
    symbol = _SYMBOLS.get(kind, _SYMBOLS["info"])
    color = color_map.get(kind, theme.accent)

    t = Text()
    t.append(symbol + " ", style=f"bold {color}")
    t.append(text, style=color)

    console = Console(highlight=False, force_terminal=True)
    with console.capture() as cap:
        console.print(t, end="")
    return cap.get()
