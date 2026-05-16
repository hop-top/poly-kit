"""
hop_top_kit.tui — terminal display components.

Re-exports the public API for all TUI primitives.
"""

from hop_top_kit.tui.anim import anim
from hop_top_kit.tui.badge import badge
from hop_top_kit.tui.confirm import confirm
from hop_top_kit.tui.pills import pills
from hop_top_kit.tui.progress import progress
from hop_top_kit.tui.select import SelectItem, select
from hop_top_kit.tui.spinner import spinner
from hop_top_kit.tui.status import status

__all__ = [
    "SelectItem",
    "anim",
    "badge",
    "confirm",
    "pills",
    "progress",
    "select",
    "spinner",
    "status",
]
