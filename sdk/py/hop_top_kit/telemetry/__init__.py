"""Telemetry SDK for hop-top tools.

Implements the ADR-0035 / ADR-0038 contract: mode resolution from env,
install-id (32 raw bytes → SHA-256 hex) at XDG state, and consent file
(YAML at XDG config). Default-denied across the board.
"""

from .client import Client
from .consent import Consent, consent_path, load_consent
from .install_id import (
    get_install_id,
    install_id_path,
    reset_for_test,
    rotate,
)
from .mode import Mode, parse_mode, resolve_mode

# Re-export the redactor as `redact_string` only (string-only helper).
# We do NOT re-export the `redact()` function under the bare name here
# because that would shadow the `.redact` submodule on the package object,
# breaking `import hop_top_kit.telemetry.redact` for downstream callers.
# Callers who need the dict-walking variant import it explicitly:
#   from hop_top_kit.telemetry.redact import redact
from .redact import redact_string

__all__ = [
    "Client",
    "Consent",
    "Mode",
    "consent_path",
    "get_install_id",
    "install_id_path",
    "load_consent",
    "parse_mode",
    "redact_string",
    "reset_for_test",
    "resolve_mode",
    "rotate",
]
