"""Telemetry mode enum + env-precedence resolver.

Mirrors `go/runtime/telemetry/mode.go`. SDK-side resolution honors:
  1. <APP>_TELEMETRY_MODE (when KIT_APP_PREFIX is set)
  2. KIT_TELEMETRY_MODE
  3. Mode.OFF (default)

DO_NOT_TRACK and KIT_TELEMETRY_CONSENT are NOT consulted here — that's the
consent layer's job (see consent.py). This module is pure mode resolution.
The client combines mode + consent before any emission.
"""

from __future__ import annotations

import os
from enum import StrEnum
from typing import Optional


class Mode(StrEnum):
    """Telemetry emission mode. String-valued for ergonomic comparison."""

    OFF = "off"
    ANON = "anon"
    FULL = "full"


def parse_mode(s: Optional[str]) -> tuple[Mode, bool]:
    """Parse a mode string.

    Returns (mode, ok). Empty / None maps to (OFF, True) — treated as
    "no opinion, default off". Unknown maps to (OFF, False).
    """
    if s is None or s == "":
        return Mode.OFF, True
    low = s.strip().lower()
    for m in Mode:
        if m.value == low:
            return m, True
    return Mode.OFF, False


def _resolve_app_prefix(env: dict) -> str:
    """SDKs adopt the app prefix from KIT_APP_PREFIX (rarely set; KIT-only mostly)."""
    return env.get("KIT_APP_PREFIX", "").strip().upper()


def resolve_mode(env: Optional[dict] = None) -> Mode:
    """Resolve the current telemetry mode per precedence.

    Precedence (highest wins):
      1. <APP>_TELEMETRY_MODE (if KIT_APP_PREFIX is set)
      2. KIT_TELEMETRY_MODE
      3. Mode.OFF (default)

    Malformed values fall through to the next layer rather than raising;
    fully malformed env → OFF.
    """
    env = env if env is not None else dict(os.environ)

    app = _resolve_app_prefix(env)
    if app:
        v = env.get(f"{app}_TELEMETRY_MODE", "").strip()
        if v:
            m, ok = parse_mode(v)
            if ok:
                return m

    v = env.get("KIT_TELEMETRY_MODE", "").strip()
    if v:
        m, ok = parse_mode(v)
        if ok:
            return m

    return Mode.OFF
