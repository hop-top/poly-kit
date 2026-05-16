"""hop_top_kit.auth — Factor 12: auth and credential lifecycle.

Provides a protocol for credential introspection and a NoAuth
sentinel for unauthenticated contexts. Mirrors Go kit/auth design.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Protocol, runtime_checkable


@dataclass
class Credential:
    source: str
    identity: str
    scopes: list[str]
    expires_at: str | None = None
    renewable: bool = False


@runtime_checkable
class AuthIntrospector(Protocol):
    def inspect(self) -> Credential: ...
    def refresh(self) -> None: ...


class NoAuth:
    """Sentinel: no authentication configured."""

    def inspect(self) -> Credential:
        return Credential(
            source="none",
            identity="",
            scopes=[],
        )

    def refresh(self) -> None:
        pass
