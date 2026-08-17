"""
hop_top_kit.parity — cross-language TUI parity constants.

Single loader for the canonical ``contracts/parity/parity.json``. Every
Python consumer imports from here rather than re-deriving the path: three
independent ``parents[N]`` walks to the same file drift apart the moment a
module moves, and each one breaks differently.

Mirrors the Go loader in ``contracts/parity/parity.go`` (``parity.Values``
and ``parity.Blocks``) and the TypeScript loader in
``sdk/ts/src/tui/parity.ts``.
"""

from __future__ import annotations

import json
import pathlib
from typing import Any

# hop_top_kit/parity.py -> hop_top_kit -> sdk/py -> sdk -> <repo root>
PARITY_PATH = pathlib.Path(__file__).resolve().parents[3] / "contracts" / "parity" / "parity.json"
"""Canonical parity contract path, resolved once for every consumer."""

PARITY: dict[str, Any] = json.loads(PARITY_PATH.read_text())
"""Parsed contents of ``contracts/parity/parity.json``."""

BLOCKS: tuple[str, ...] = (
    "description",
    "status",
    "spinner",
    "anim",
    "help",
    "verbosity",
    "streams",
    "extends",
)
"""Registry of top-level ``parity.json`` keys this loader knows.

Every content block in ``parity.json`` MUST appear here and MUST be reachable
through one of the accessors below — ``tests/test_parity.py`` enforces both
directions so a new block cannot be added as decoration.

Keys starting with ``$`` are JSON Schema metadata, not content.
"""

# ─── Block accessors ────────────────────────────────────────────────────────
#
# Each accessor is the Python counterpart of a field on Go's parity.Data.
# They read by the exact JSON key, so a renamed key yields an empty value
# rather than a silent fallback — which is what the drift guard asserts on.

STATUS_SYMBOLS: dict[str, str] = PARITY.get("status", {}).get("symbols", {})
"""Prefix symbols for status lines, keyed by kind."""

SPINNER_FRAMES: list[str] = PARITY.get("spinner", {}).get("frames", [])
"""Braille spinner frames in render order."""

SPINNER_INTERVAL_MS: int = PARITY.get("spinner", {}).get("interval_ms", 0)
"""Spinner frame interval, milliseconds."""

ANIM_RUNES: str = PARITY.get("anim", {}).get("runes", "")
"""Character pool for the scramble animation."""

ANIM_INTERVAL_MS: int = PARITY.get("anim", {}).get("interval_ms", 0)
"""Animation frame interval, milliseconds."""

ANIM_DEFAULT_WIDTH: int = PARITY.get("anim", {}).get("default_width", 0)
"""Default scramble width, in characters."""

HELP_SECTION_ORDER: list[str] = PARITY.get("help", {}).get("section_order", [])
"""Fang-vocabulary help section names in render order."""

HELP_SECTIONS: dict[str, dict[str, str]] = PARITY.get("help", {}).get("sections", {})
"""Help section display metadata, keyed by fang section name."""

VERBOSITY_FLAG: str = PARITY.get("verbosity", {}).get("flag", "")
"""Stackable verbosity flag, e.g. ``-V``."""

VERBOSITY_LEVELS: dict[str, str] = PARITY.get("verbosity", {}).get("levels", {})
"""Stacked ``-V`` count (decimal string) mapped to a log level name."""

VERBOSITY_QUIET_OVERRIDE: str = PARITY.get("verbosity", {}).get("quiet_override", "")
"""Log level ``--quiet`` forces, overriding the ``-V`` count."""

STREAMS_FLAG: str = PARITY.get("streams", {}).get("flag", "")
"""Flag that toggles named output streams."""

STREAMS_LABEL_FORMAT: str = PARITY.get("streams", {}).get("label_format", "")
"""Template each port reproduces when prefixing stream lines."""

STREAMS_OUTPUT: str = PARITY.get("streams", {}).get("output", "")
"""Destination stream for labelled stream output."""

EXTENDS: list[str] = PARITY.get("extends", [])
"""Sibling contract files the parity suite also covers."""
