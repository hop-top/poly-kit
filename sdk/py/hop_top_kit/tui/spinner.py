"""
hop_top_kit.tui.spinner — themed spinner context manager.

Wraps rich.status.Status with theme accent color.
"""

from __future__ import annotations

from collections.abc import Generator
from contextlib import contextmanager

from rich.console import Console
from rich.status import Status

from hop_top_kit.cli import Theme


class _SpinnerHandle:
    """Handle returned inside the ``with spinner(...)`` block."""

    def __init__(self, status_obj: Status) -> None:
        self._status = status_obj

    def update(self, message: str) -> None:
        """Update spinner message."""
        self._status.update(message)


@contextmanager
def spinner(theme: Theme, message: str = "") -> Generator[_SpinnerHandle, None, None]:
    """Context manager wrapping rich.status.Status with theme accent color.

    Args:
        theme:   Theme providing the accent color for the spinner.
        message: Initial status message.

    Yields:
        _SpinnerHandle — call ``.update(msg)`` to change the message.

    Example::

        with spinner(theme, "loading...") as s:
            do_work()
            s.update("almost done...")
    """
    console = Console()
    with Status(message, spinner_style=theme.accent, console=console) as st:
        yield _SpinnerHandle(st)
