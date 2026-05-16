"""provenance — data provenance metadata (12-factor AI CLI, Factor 11).

Wraps output data with _meta containing source, timestamp, and method.
"""

from __future__ import annotations

from dataclasses import asdict, dataclass
from typing import Any


@dataclass
class Provenance:
    source: str
    timestamp: str  # ISO 8601
    method: str


def with_provenance(data: Any, p: Provenance) -> dict:
    """Wrap data with provenance metadata."""
    return {"data": data, "_meta": asdict(p)}
