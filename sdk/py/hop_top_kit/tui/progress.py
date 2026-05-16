"""
hop_top_kit.tui.progress — themed progress bar context manager.

Wraps rich.progress.Progress with theme accent/muted colors.
"""

from __future__ import annotations

from collections.abc import Generator
from contextlib import contextmanager

from rich.progress import (
    BarColumn,
    Progress,
    TaskID,
    TextColumn,
    TimeElapsedColumn,
)

from hop_top_kit.cli import Theme


class _ProgressHandle:
    """Handle returned inside the ``with progress(...)`` block."""

    def __init__(self, prog: Progress, task_id: TaskID, total: int) -> None:
        self._prog = prog
        self._task_id = task_id
        self._total = total

    def advance(self, amount: float = 1) -> None:
        """Advance the progress bar by *amount* steps."""
        self._prog.advance(self._task_id, amount)

    @property
    def total(self) -> int:
        """Total steps configured for this bar."""
        return self._total


@contextmanager
def progress(theme: Theme, total: int = 100) -> Generator[_ProgressHandle, None, None]:
    """Context manager wrapping rich.progress.Progress with theme colors.

    Args:
        theme: Theme providing accent (completed) and muted (remaining) colors.
        total: Total step count for the progress bar.

    Yields:
        _ProgressHandle — call ``.advance(n)`` to advance the bar.

    Example::

        with progress(theme, total=50) as p:
            for _ in range(50):
                do_work()
                p.advance(1)
    """
    bar = Progress(
        TextColumn("[progress.description]{task.description}"),
        BarColumn(
            complete_style=theme.accent,
            finished_style=theme.accent,
            style=theme.muted,
        ),
        TimeElapsedColumn(),
    )
    with bar:
        task_id = bar.add_task("", total=total)
        yield _ProgressHandle(bar, task_id, total)
