"""safety — delegation safety guard (12-factor AI CLI, Factor 10).

Prevents dangerous operations without explicit --force. In non-TTY
environments, caution-level ops also require --force.
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
