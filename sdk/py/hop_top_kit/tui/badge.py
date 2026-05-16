"""
hop_top_kit.tui.badge — styled badge/pill renderer.

Mirrors Go's tui.Badge display component using Rich markup.
"""

from __future__ import annotations

from rich.console import Console
from rich.text import Text

from hop_top_kit.cli import Theme


def badge(theme: Theme, text: str, color: str | None = None) -> str:
    """Render a styled pill/badge.

    Args:
        theme: Theme providing default accent color.
        text:  Label to display inside the badge.
        color: Hex color override; defaults to ``theme.accent``.

    Returns:
        ANSI-escaped string suitable for printing to a terminal.
    """
    fg = color or theme.accent
    t = Text(f" {text} ", style=f"bold {fg}")
    console = Console(highlight=False, force_terminal=True)
    with console.capture() as cap:
        console.print(t, end="")
    return cap.get()
