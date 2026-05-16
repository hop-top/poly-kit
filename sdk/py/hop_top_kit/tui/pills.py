"""
hop_top_kit.tui.pills — space-separated styled pill list renderer.

Mirrors Go's tui.Pill/PillBar display output using Rich markup.
"""

from __future__ import annotations

from rich.console import Console
from rich.text import Text

from hop_top_kit.cli import Theme


def pills(theme: Theme, items: list[str]) -> str:
    """Render a space-separated row of styled pills.

    Each item is displayed in ``theme.secondary`` color. Items are joined
    with a single space, matching the compact pill-bar layout in Go.

    Args:
        theme: Theme providing the secondary color for the pills.
        items: Pill labels to render.

    Returns:
        ANSI-escaped string of space-separated pills, or empty string
        when *items* is empty.
    """
    if not items:
        return ""

    color = theme.secondary
    t = Text()
    for i, item in enumerate(items):
        if i:
            t.append(" ")
        t.append(f" {item} ", style=f"bold {color}")

    console = Console(highlight=False, force_terminal=True)
    with console.capture() as cap:
        console.print(t, end="")
    return cap.get()
