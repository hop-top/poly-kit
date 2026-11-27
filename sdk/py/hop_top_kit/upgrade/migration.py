"""Versioned migrations and the registry that holds them."""

from __future__ import annotations

import threading
from collections.abc import Callable
from dataclasses import dataclass

from hop_top_kit.upgrade.semver import Semver, compare_semver, parse_semver

#: One migration step: raises to fail.
Step = Callable[[], None]


class VerifyError(Exception):
    """A verify step failed. Also the base of a failed document verify."""


@dataclass
class Migration:
    """A versioned schema migration.

    Attributes:
        version: Semver, pre-release included; ordered by kit's lenient rules.
        schema:  Matches the schema driver's name.
        up:      Applies the migration. Required.
        down:    Reverts it; ``None`` means no rollback.
        verify:  Runs after *up* and before the version is recorded; a
                 failure counts as a failed step and the rollback includes
                 this migration.
    """

    version: str
    schema: str
    up: Step
    down: Step | None = None
    verify: Step | None = None


def _key(version: str) -> Semver:
    return parse_semver(version.removeprefix("v"))


class Registry:
    """Migrations for one or more Migrators. :func:`register_migration`
    feeds :func:`default_registry`; a Migrator reads that unless it is
    given another registry."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._migrations: list[Migration] = []

    def register(self, m: Migration) -> None:
        """Add *m*; raises :class:`ValueError` when it has no ``up``."""
        if m.up is None:
            raise ValueError(f"upgrade: migration {m.version} has no up function")
        with self._lock:
            self._migrations.append(m)

    def reset(self) -> None:
        """Drop every migration."""
        with self._lock:
            self._migrations = []

    def pending(self, schema: str, from_: str, to: str) -> list[Migration]:
        """Migrations of *schema* with ``from_ < version <= to``, ascending.
        Equal versions have no defined order."""
        lo, hi = _key(from_), _key(to)
        with self._lock:
            found = [
                m
                for m in self._migrations
                if m.schema == schema
                and compare_semver(_key(m.version), lo) > 0
                and compare_semver(_key(m.version), hi) <= 0
            ]
        found.sort(key=_Ordered)
        return found


class _Ordered:
    """Sort key over :func:`compare_semver`."""

    __slots__ = ("v",)

    def __init__(self, m: Migration) -> None:
        self.v = _key(m.version)

    def __lt__(self, other: _Ordered) -> bool:
        return compare_semver(self.v, other.v) < 0


_default = Registry()


def default_registry() -> Registry:
    """The registry :func:`register_migration` adds to."""
    return _default


def register_migration(m: Migration) -> None:
    """Add *m* to the default registry."""
    _default.register(m)


def reset_registry_for_test() -> None:
    """Clear the default registry. For tests only."""
    _default.reset()
