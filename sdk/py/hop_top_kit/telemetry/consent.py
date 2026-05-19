"""Consent-file reader.

Reads the persisted consent decision from
``<XDG_CONFIG_HOME>/kit/telemetry.yaml``. Default-denied: any error, missing
file, malformed YAML, or unknown state value yields ``Consent.denied()``.

The Go-side ``core/consent/file_store.go`` owns the persisted schema:

    telemetry:
      consent:
        state: granted | denied
        decided_at: RFC3339
        prompt_version: int
        decision_source: prompt | flag | env | config

SDKs only read; they never write this file.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

import yaml


@dataclass(frozen=True)
class Consent:
    """In-memory representation of the persisted consent decision.

    The persisted file does NOT carry mode; that's env/context-only.
    """

    allowed: bool
    prompt_version: int = 0
    decision_source: str = "config"
    decided_at: Optional[str] = None

    @classmethod
    def denied(cls) -> "Consent":
        """Default-denied sentinel for missing/malformed input."""
        return cls(allowed=False)


def _xdg_config_home() -> Path:
    return Path(os.environ.get("XDG_CONFIG_HOME") or (Path.home() / ".config"))


def consent_path() -> Path:
    """Canonical path for the persisted consent YAML."""
    return _xdg_config_home() / "kit" / "telemetry.yaml"


def load_consent() -> Consent:
    """Read the consent decision. Never raises.

    Missing file, malformed YAML, unexpected types, or unknown ``state``
    values all map to ``Consent.denied()``.
    """
    p = consent_path()
    if not p.exists():
        return Consent.denied()

    try:
        data = yaml.safe_load(p.read_text())
    except Exception:
        return Consent.denied()

    if not isinstance(data, dict):
        return Consent.denied()

    tel = data.get("telemetry")
    if not isinstance(tel, dict):
        return Consent.denied()

    block = tel.get("consent")
    if not isinstance(block, dict):
        return Consent.denied()

    state = block.get("state", "denied")
    if state not in ("granted", "denied"):
        return Consent.denied()

    try:
        prompt_version = int(block.get("prompt_version", 0))
    except (TypeError, ValueError):
        prompt_version = 0

    decision_source = str(block.get("decision_source", "config"))
    decided_at = block.get("decided_at")
    if decided_at is not None and not isinstance(decided_at, str):
        decided_at = str(decided_at)

    return Consent(
        allowed=(state == "granted"),
        prompt_version=prompt_version,
        decision_source=decision_source,
        decided_at=decided_at,
    )
