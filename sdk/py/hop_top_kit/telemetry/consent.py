"""Consent-file reader.

Reads the persisted consent decision from the kit AppConfig at
``<XDG_CONFIG_HOME>/kit/config.yaml`` under the ``kit.telemetry.consent``
partition. A pre-refactor layout at ``<XDG_CONFIG_HOME>/kit/telemetry.yaml``
(bare ``telemetry.consent``) is read as a fallback for backward
compatibility with installs that have not been migrated yet.

Default-denied: any error, missing file, malformed YAML, or unknown
state value yields ``Consent.denied()``.

The Go-side ``core/consent/file_store.go`` owns the persisted schema:

    kit:
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
    def denied(cls) -> Consent:
        """Default-denied sentinel for missing/malformed input."""
        return cls(allowed=False)


def _xdg_config_home() -> Path:
    return Path(os.environ.get("XDG_CONFIG_HOME") or (Path.home() / ".config"))


def consent_path() -> Path:
    """Canonical path for the persisted consent YAML (config.yaml)."""
    return _xdg_config_home() / "kit" / "config.yaml"


def legacy_consent_path() -> Path:
    """Pre-refactor consent file path (telemetry.yaml).

    Read-only fallback used by :func:`load_consent` when the canonical
    config.yaml is missing or lacks the ``kit.telemetry.consent`` block.
    """
    return _xdg_config_home() / "kit" / "telemetry.yaml"


def _consent_from_block(block: object) -> Optional[Consent]:
    """Decode a consent: mapping into a Consent or None if unusable."""
    if not isinstance(block, dict):
        return None

    state = block.get("state", "denied")
    if state not in ("granted", "denied"):
        return None

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


def _load_yaml(path: Path) -> Optional[dict]:
    if not path.exists():
        return None
    try:
        data = yaml.safe_load(path.read_text())
    except Exception:
        return None
    return data if isinstance(data, dict) else None


def load_consent() -> Consent:
    """Read the consent decision. Never raises.

    Tries the canonical ``config.yaml`` (``kit.telemetry.consent``)
    first; falls back to the legacy ``telemetry.yaml``
    (``telemetry.consent``) when the canonical file is absent or lacks
    the consent block. Missing files, malformed YAML, unexpected types,
    or unknown ``state`` values all map to ``Consent.denied()``.
    """
    # Canonical path: <XDG_CONFIG_HOME>/kit/config.yaml under
    # kit.telemetry.consent.
    data = _load_yaml(consent_path())
    if data is not None:
        kit = data.get("kit")
        if isinstance(kit, dict):
            tel = kit.get("telemetry")
            if isinstance(tel, dict):
                got = _consent_from_block(tel.get("consent"))
                if got is not None:
                    return got

    # Legacy fallback: <XDG_CONFIG_HOME>/kit/telemetry.yaml under
    # bare telemetry.consent.
    legacy = _load_yaml(legacy_consent_path())
    if legacy is not None:
        tel = legacy.get("telemetry")
        if isinstance(tel, dict):
            got = _consent_from_block(tel.get("consent"))
            if got is not None:
                return got

    return Consent.denied()
