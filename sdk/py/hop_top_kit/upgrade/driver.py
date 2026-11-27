"""The driver protocol: what the Migrator needs from a store, and the
optional capabilities it checks for with ``isinstance``.

A driver is any object with these members; it inherits nothing. Capabilities
(:class:`Locker`, :class:`VersionStorer`) are separate runtime-checkable
protocols the Migrator tests per driver, so a new driver adds a capability
by defining its method and the Migrator does not change.
"""

from __future__ import annotations

import threading
from collections.abc import Callable
from pathlib import Path
from typing import Protocol, runtime_checkable

#: Cancellation: an event the caller sets to stop a run between steps and to
#: wake a waiting :meth:`Locker.lock`.
Cancel = threading.Event


@runtime_checkable
class SchemaDriver(Protocol):
    """Version tracking and backup/restore for one named schema.

    ``backup`` and ``restore`` return normally only when they really did it;
    they raise :class:`~hop_top_kit.upgrade.BackupUnsupported` when they
    cannot and any other exception when they failed. Every method may raise.
    """

    @property
    def name(self) -> str:
        """Schema identifier, matched against ``Migration.schema``."""
        ...

    def version(self) -> str:
        """The applied version, or ``""`` when none is set."""
        ...

    def set_version(self, version: str) -> None:
        """Record *version*: after each applied migration, and the earlier
        one again after a rollback or restore. A driver that is not a
        :class:`VersionStorer` is called after the Migrator wrote the version
        file."""
        ...

    def backup(self, dest: Path) -> None:
        """Copy the current state into the directory *dest* (it exists)."""
        ...

    def restore(self, src: Path) -> None:
        """Replace the current state from the backup directory *src*."""
        ...


@runtime_checkable
class VersionStorer(Protocol):
    """Optional marker for a driver that keeps its version itself (a database
    table). The Migrator then calls only ``set_version`` to record a version
    and writes no version file; ``version`` is the one authority."""

    def stores_version(self) -> bool: ...


@runtime_checkable
class Locker(Protocol):
    """Optional capability of a driver whose store several processes can
    migrate at once.

    The Migrator calls ``lock`` before it reads the driver's version and
    holds the lock for that schema's whole run (version read, baseline,
    backup, every ``up``, every version record, rollback, prune), then calls
    the returned unlock exactly once, on every path. ``lock`` blocks until it
    acquires or *cancel* is set; in the second case it raises
    :class:`~hop_top_kit.upgrade.Canceled` and holds nothing. A ``lock``
    error fails the schema before anything else; an unlock error is logged
    as a warning, never raised.
    """

    def lock(self, cancel: Cancel) -> Callable[[], None]: ...
