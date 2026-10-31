"""safety — delegation safety guard (12-factor AI CLI, Factor 10).

Prevents dangerous operations without explicit --force. In non-TTY
environments, caution-level ops also require --force.

This is a library call the command author makes explicitly. It is
independent of the root-enforced delegation gate (``--confirm``,
``--confirm-token``, ``--policy``, ``--max-ops``, ``--dry-run``) in
:mod:`hop_top_kit.gate`, which acts on ``kit/side-effect`` annotations
without the command calling anything; a destructive leaf may pass both.
"""

from __future__ import annotations

import sys
from enum import Enum


class SafetyLevel(Enum):
    READ = "read"
    CAUTION = "caution"
    DANGEROUS = "dangerous"


def safety_guard(level: SafetyLevel, *, force: bool = False) -> None:
    """Raise SystemExit if operation not permitted at given safety level."""
    if level == SafetyLevel.READ:
        return

    if level == SafetyLevel.DANGEROUS and not force:
        raise SystemExit(f"Refusing {level.value} operation without --force")

    if level == SafetyLevel.CAUTION and not force and not sys.stdin.isatty():
        raise SystemExit(f"Refusing {level.value} operation in non-TTY without --force")
