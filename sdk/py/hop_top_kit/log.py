"""Structured logger wrapping structlog — applies kit's charmtone theme
colors to stderr output.

Thin wrapper: kit owns the theme + quiet/no_color config, structlog
handles processors, formatters, context binding, and stdlib integration.
"""

from __future__ import annotations

import logging
import sys
from typing import Any, TypeAlias

import structlog

from hop_top_kit import parity

# ---------------------------------------------------------------------------
# Theme — matches Go charmtone palette
# ---------------------------------------------------------------------------

# TraceLevel sits below DEBUG (10) — matches Go's log.DebugLevel - 1.
TRACE = 5

CHERRY = (0xED, 0x4A, 0x5E)  # error
YAM = (0xE5, 0xA1, 0x4E)  # warn
SQUID = (0x85, 0x81, 0x83)  # info
SMOKE = (0xBF, 0xBC, 0xC8)  # debug / trace

RGB: TypeAlias = tuple[int, int, int]

_LEVEL_STYLES: dict[str, tuple[str, RGB, bool]] = {
    "trace": ("TRAC", SMOKE, False),
    "debug": ("DEBU", SMOKE, False),
    "info": ("INFO", SQUID, False),
    "warning": ("WARN", YAM, True),
    "error": ("ERRO", CHERRY, True),
    "critical": ("ERRO", CHERRY, True),
}


def _fg(rgb: RGB, text: str) -> str:
    return f"\x1b[38;2;{rgb[0]};{rgb[1]};{rgb[2]}m{text}\x1b[0m"


def _bold(text: str) -> str:
    return f"\x1b[1m{text}\x1b[22m"


# ---------------------------------------------------------------------------
# Custom structlog renderer matching Go kit/log output
# ---------------------------------------------------------------------------


class KitRenderer:
    """Render structlog events as `LEVEL msg key=val` lines."""

    def __init__(self, no_color: bool = False) -> None:
        self._no_color = no_color

    def __call__(
        self,
        logger: object,
        method_name: str,
        event_dict: dict[str, object],
    ) -> str:
        label, color, is_bold = _LEVEL_STYLES.get(
            method_name,
            ("INFO", SQUID, False),
        )

        if self._no_color:
            prefix = label
        else:
            prefix = _fg(color, label)
            if is_bold:
                prefix = _bold(prefix)

        msg = event_dict.pop("event", "")
        # Remove structlog internal keys.
        for k in ("_record", "_from_structlog", "level"):
            event_dict.pop(k, None)

        kv_parts: list[str] = []
        for k, v in event_dict.items():
            sv = str(v)
            kv_parts.append(f'{k}="{sv}"' if " " in sv else f"{k}={sv}")
        kv = (" " + " ".join(kv_parts)) if kv_parts else ""

        return f"{prefix} {msg}{kv}"


# ---------------------------------------------------------------------------
# Logger type alias for consumer code
# ---------------------------------------------------------------------------

Logger = structlog.stdlib.BoundLogger


# ---------------------------------------------------------------------------
# Factory
# ---------------------------------------------------------------------------


class _StderrLoggerFactory:
    """Resolves sys.stderr at log time, not configure time."""

    def __call__(self) -> structlog.PrintLogger:
        return structlog.PrintLogger(file=sys.stderr)


# Register TRACE level with stdlib logging.
logging.addLevelName(TRACE, "TRACE")

_LEVEL_ORDER = ["trace", "debug", "info", "warning", "error", "critical"]

# Contract level NAME → (stdlib level, structlog level name).
#
# The boundary between the contract's vocabulary and Python's. The contract
# owns which names the -V count and --quiet map to; this table owns how each
# name reaches stdlib logging. Two names need translating: "trace" has no
# stdlib constant (TRACE is kit-local, below DEBUG, matching Go's
# DebugLevel-1), and the contract's "warn" is stdlib/structlog "warning".
_LEVEL_BY_NAME: dict[str, tuple[int, str]] = {
    "trace": (TRACE, "trace"),
    "debug": (logging.DEBUG, "debug"),
    "info": (logging.INFO, "info"),
    "warn": (logging.WARNING, "warning"),
    "warning": (logging.WARNING, "warning"),
    "error": (logging.ERROR, "error"),
}


def level_for_name(name: str) -> tuple[int, str]:
    """Resolve a contract level name to (stdlib level, structlog level name).

    Unknown names fall back to INFO rather than raising: the contract is the
    source of the name, and a CLI must still start if it gains a level this
    port has no stdlib counterpart for.
    """
    return _LEVEL_BY_NAME.get(name, (logging.INFO, "info"))


def verbosity_level(
    verbose: int,
    quiet: bool = False,
    data: dict[str, Any] | None = None,
) -> tuple[int, str]:
    """Map a ``-V`` count to (stdlib level, structlog level name).

    Pure in *data*: the caller supplies the parity contract so tests can
    inject a constructed one. Defaults to the loaded contract.

    The count-to-name mapping comes from ``verbosity.levels``; counts above
    the highest declared key saturate at that key's level. ``quiet`` short-
    circuits to ``verbosity.quiet_override``.
    """
    if quiet:
        return quiet_level(data)

    levels = _verbosity_block(data).get("levels", {})
    if not levels:
        return logging.INFO, "info"

    # Keys are decimal strings; saturate at the highest declared count.
    counts = sorted(int(k) for k in levels)
    chosen = counts[0]
    for c in counts:
        if verbose >= c:
            chosen = c
    return level_for_name(levels[str(chosen)])


def quiet_level(data: dict[str, Any] | None = None) -> tuple[int, str]:
    """Level ``--quiet`` forces, from ``verbosity.quiet_override``."""
    return level_for_name(_verbosity_block(data).get("quiet_override", ""))


def _verbosity_block(data: dict[str, Any] | None) -> dict[str, Any]:
    """The contract's ``verbosity`` block, defaulting to the loaded contract."""
    if data is None:
        data = parity.PARITY
    block = data.get("verbosity", {})
    return block if isinstance(block, dict) else {}


def with_verbose(
    verbose: int,
    quiet: bool = False,
    no_color: bool = False,
) -> Logger:
    """Create a logger at the level implied by verbose count.

    Count mapping and the ``--quiet`` override both come from the
    ``verbosity`` block of ``contracts/parity/parity.json``.
    """
    level, level_name = verbosity_level(verbose, quiet)
    return _configure_and_get(level, level_name, no_color)


def create_logger(*, quiet: bool = False, no_color: bool = False) -> Logger:
    """Create a kit-themed structured logger.

    Uses structlog wrapping stdlib logging. All output goes to stderr.

    ``quiet`` resolves through ``verbosity.quiet_override``; the verbose
    default (DEBUG) is this factory's own, not a contract value.
    """
    level, level_name = quiet_level() if quiet else (logging.DEBUG, "debug")
    return _configure_and_get(level, level_name, no_color)


def _configure_and_get(level: int, level_name: str, no_color: bool) -> Logger:
    """Shared structlog configuration."""

    def _level_filter(
        logger: object,
        method_name: str,
        event_dict: dict,
    ) -> dict:
        if _LEVEL_ORDER.index(method_name) < _LEVEL_ORDER.index(level_name):
            raise structlog.DropEvent
        return event_dict

    structlog.configure(
        processors=[
            structlog.stdlib.add_log_level,
            _level_filter,
            structlog.dev.set_exc_info,
            KitRenderer(no_color=no_color),
        ],
        wrapper_class=structlog.stdlib.BoundLogger,
        context_class=dict,
        logger_factory=_StderrLoggerFactory(),
        cache_logger_on_first_use=False,
    )

    handler = logging.StreamHandler(sys.stderr)
    handler.setLevel(level)
    root = logging.getLogger()
    root.setLevel(level)
    root.handlers = [handler]

    return structlog.get_logger()
