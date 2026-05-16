"""
hop_top_kit.tui.anim — gradient animation context manager.

Wraps rich.live.Live with a cycling gradient from theme.accent → theme.secondary.
Constants loaded from tui/parity.json (single source of truth).
"""

from __future__ import annotations

import json
import pathlib
import threading
from collections.abc import Generator
from contextlib import contextmanager

from rich.live import Live
from rich.text import Text

from hop_top_kit.cli import Theme

_PARITY = json.loads(
    (pathlib.Path(__file__).parents[4] / "contracts" / "parity" / "parity.json").read_text()
)
_ANIM_CHARS = _PARITY["anim"]["runes"]
_INTERVAL = _PARITY["anim"]["interval_ms"] / 1000  # ms → seconds
_DEFAULT_WIDTH = _PARITY["anim"]["default_width"]


def _hex_to_rgb(hex_color: str) -> tuple[int, int, int]:
    """Parse ``#RRGGBB`` → (r, g, b) ints 0–255."""
    h = hex_color.lstrip("#")
    return int(h[0:2], 16), int(h[2:4], 16), int(h[4:6], 16)


def _lerp_color(a: tuple[int, int, int], b: tuple[int, int, int], t: float) -> str:
    """Linear interpolate between two RGB tuples; return ``#RRGGBB``."""
    r = int(a[0] + (b[0] - a[0]) * t)
    g = int(a[1] + (b[1] - a[1]) * t)
    bl = int(a[2] + (b[2] - a[2]) * t)
    return f"#{r:02x}{g:02x}{bl:02x}"


def _make_gradient(accent: str, secondary: str, steps: int) -> list[str]:
    """Build a list of *steps* hex colors blending accent → secondary."""
    if steps <= 1:
        return [accent]
    ca = _hex_to_rgb(accent)
    cb = _hex_to_rgb(secondary)
    return [_lerp_color(ca, cb, i / (steps - 1)) for i in range(steps)]


class _AnimHandle:
    """Handle returned inside the ``with anim(...)`` block."""

    def __init__(self, live: Live, label: str, width: int, theme: Theme) -> None:
        self._live = live
        self._label = label
        self._width = width
        self._theme = theme
        self._step = 0
        self._gradient = _make_gradient(theme.accent, theme.secondary, width)
        self._lock = threading.Lock()
        self._stopped = threading.Event()
        self._thread = threading.Thread(target=self._loop, daemon=True)

    def _render(self) -> Text:
        import random

        t = Text()
        grad = self._gradient
        for i in range(self._width):
            ch = random.choice(_ANIM_CHARS)
            ci = (i + self._step) % len(grad)
            t.append(ch, style=grad[ci])
        if self._label:
            t.append(" " + self._label)
        return t

    def _loop(self) -> None:
        while not self._stopped.wait(_INTERVAL):
            with self._lock:
                self._step += 1
                self._live.update(self._render())

    def set_label(self, label: str) -> None:
        """Update the label text shown after the animation."""
        with self._lock:
            self._label = label

    def _start(self) -> None:
        self._live.update(self._render())
        self._thread.start()

    def _stop(self) -> None:
        self._stopped.set()
        self._thread.join(timeout=1.0)


@contextmanager
def anim(
    theme: Theme,
    label: str = "",
    width: int = _DEFAULT_WIDTH,
) -> Generator[_AnimHandle, None, None]:
    """Context manager rendering a gradient cycling animation via rich.live.Live.

    Args:
        theme: Theme providing accent (gradient start) and secondary (gradient end).
        label: Optional text label shown after the animation block.
        width: Number of cycling characters wide (default from parity.json).

    Yields:
        _AnimHandle — call ``.set_label(text)`` to update the label.

    Example::

        with anim(theme, label="compiling", width=10) as a:
            do_work()
            a.set_label("linking")
    """
    live = Live(refresh_per_second=1 / _INTERVAL)
    handle = _AnimHandle(live, label, width, theme)
    with live:
        handle._start()
        try:
            yield handle
        finally:
            handle._stop()
