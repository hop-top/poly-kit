"""Structured logger wrapping structlog — applies kit's charmtone theme
colors to stderr output.

Thin wrapper: kit owns the theme + quiet/no_color config, structlog
handles processors, formatters, context binding, and stdlib integration.
"""

from __future__ import annotations

import logging
import sys
from typing import TypeAlias

import structlog

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


def _verbose_to_level(verbose: int, quiet: bool = False) -> tuple[int, str]:
    """Map verbose count → (stdlib level, structlog level name).

    0=INFO, 1=DEBUG, 2+=TRACE. quiet overrides to WARNING.
    """
    if quiet:
        return logging.WARNING, "warning"
    if verbose >= 2:
        return TRACE, "trace"
    if verbose == 1:
        return logging.DEBUG, "debug"
    return logging.INFO, "info"


def with_verbose(
    verbose: int,
    quiet: bool = False,
    no_color: bool = False,
) -> Logger:
    """Create a logger at the level implied by verbose count.

    Count mapping: 0=INFO, 1=DEBUG, 2+=TRACE. quiet overrides to WARNING.
    """
    level, level_name = _verbose_to_level(verbose, quiet)
    return _configure_and_get(level, level_name, no_color)


def create_logger(*, quiet: bool = False, no_color: bool = False) -> Logger:
    """Create a kit-themed structured logger.

    Uses structlog wrapping stdlib logging. All output goes to stderr.
    """
    level = logging.WARNING if quiet else logging.DEBUG
    level_name = "warning" if quiet else "debug"
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
