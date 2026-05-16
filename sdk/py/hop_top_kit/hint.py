"""
hop_top_kit.hint — Contextual next-step hints.

Mirrors Go's output/hint.go contract.

Hints guide users (and agents) toward the logical next action without
burying them in a wall of text.  They are suppressed when output is
machine-formatted (JSON/YAML), written to a non-TTY, or explicitly disabled
via flag/env.

Public symbols:
    Hint              — dataclass: message + optional condition callable
    HintSet           — registry mapping command names → hints
    active            — filter hints whose condition passes
    hints_enabled     — check whether hints should render
    render_hints      — write active hints to a file-like stream
    register_upgrade_hints — standard "run version to verify" hint
    register_version_hints — standard "run upgrade to get latest" hint
"""

from __future__ import annotations

import os
from collections.abc import Callable
from dataclasses import dataclass, field
from typing import IO

# ---------------------------------------------------------------------------
# Types
# ---------------------------------------------------------------------------


@dataclass
class Hint:
    """A single next-step suggestion attached to a command."""

    message: str
    """Human-readable hint text (e.g. "Run `hop version` to verify.")."""
    condition: Callable[[], bool] | None = field(default=None, repr=False)
    """When provided, hint is only rendered if this returns True.
    None means the hint always applies."""


# ---------------------------------------------------------------------------
# HintSet
# ---------------------------------------------------------------------------


class HintSet:
    """Registry mapping command names to lists of Hint objects.

    Mirrors Go's ``output.HintSet``.
    """

    def __init__(self) -> None:
        self._m: dict[str, list[Hint]] = {}

    def register(self, cmd: str, *hints: Hint) -> None:
        """Add one or more hints for the given command name."""
        self._m.setdefault(cmd, []).extend(hints)

    def lookup(self, cmd: str) -> list[Hint]:
        """Return a copy of the hints registered for *cmd*.

        Returns an empty list when none are registered.
        """
        return list(self._m.get(cmd, []))


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def active(hints: list[Hint]) -> list[Hint]:
    """Return only the hints whose condition is None or returns True."""
    return [h for h in hints if h.condition is None or h.condition()]


def hints_enabled(
    *,
    no_hints: bool = False,
    quiet: bool = False,
    hints_config_enabled: bool | None = None,
) -> bool:
    """Report whether hints should be rendered.

    Mirrors Go's ``output.HintsEnabled``.

    Hints are disabled when any of:
    - ``no_hints`` is True
    - ``hints_config_enabled`` is explicitly False
    - ``quiet`` is True
    - ``HOP_QUIET_HINTS`` env var is set to ``"1"``, ``"true"``, or ``"yes"``
    """
    if no_hints:
        return False
    if hints_config_enabled is False:
        return False
    if quiet:
        return False
    env = os.environ.get("HOP_QUIET_HINTS", "")
    return env not in ("1", "true", "yes")


def render_hints(
    w: IO[str],
    hints: list[Hint],
    fmt: str,
    *,
    no_hints: bool = False,
    quiet: bool = False,
    hints_config_enabled: bool | None = None,
    muted: str = "#858183",
    no_color: bool = False,
) -> None:
    """Write active hints to *w* with dimmed styling.

    No-op when:
    - *fmt* is not ``"table"``
    - *w* is not a TTY (``hasattr(w, 'isatty') and w.isatty()`` is False)
    - hints are disabled via ``hints_enabled``
    - the active hint list is empty

    Mirrors Go's ``output.RenderHints``.

    Args:
        w:                    File-like stream (e.g. ``sys.stdout``).
        hints:                Hints to consider (from ``HintSet.lookup``).
        fmt:                  Current output format (``"table"``, ``"json"``, ``"yaml"``).
        no_hints:             Value of ``--no-hints`` flag.
        quiet:                Value of ``--quiet`` flag.
        hints_config_enabled: Value of ``hints.enabled`` config key (None = unset).
        muted:                Hex color for hint prefix/text.
        no_color:             When True (or ``NO_COLOR`` env set), strip ANSI.
    """
    if fmt != "table":
        return
    if not hints_enabled(no_hints=no_hints, quiet=quiet, hints_config_enabled=hints_config_enabled):
        return
    if not (hasattr(w, "isatty") and w.isatty()):
        return

    visible = active(hints)
    if not visible:
        return

    _no_color = no_color or bool(os.environ.get("NO_COLOR"))

    w.write("\n")
    for h in visible:
        text = f"→ {h.message}"
        if _no_color:
            w.write(text + "\n")
        else:
            h_str = muted.lstrip("#")
            r = int(h_str[0:2], 16)
            g = int(h_str[2:4], 16)
            b = int(h_str[4:6], 16)
            w.write(f"\x1b[38;2;{r};{g};{b}m{text}\x1b[0m\n")


# ---------------------------------------------------------------------------
# Standard hint factories
# ---------------------------------------------------------------------------


def register_upgrade_hints(
    hints: HintSet,
    binary: str,
    upgraded: Callable[[], bool],
) -> None:
    """Add standard "run version to verify" hint for the upgrade command.

    Active only when *upgraded()* returns True.
    Mirrors Go's ``output.RegisterUpgradeHints``.
    """
    hints.register(
        "upgrade",
        Hint(
            message=f"Run `{binary} version` to verify.",
            condition=upgraded,
        ),
    )


def register_version_hints(
    hints: HintSet,
    binary: str,
    update_avail: Callable[[], bool],
) -> None:
    """Add standard "run upgrade to get latest" hint for the version command.

    Active only when *update_avail()* returns True.
    Mirrors Go's ``output.RegisterVersionHints``.
    """
    hints.register(
        "version",
        Hint(
            message=f"Run `{binary} upgrade` to get latest.",
            condition=update_avail,
        ),
    )
