"""The Migrator: versioned migrations across several schema drivers.

Port of the Go ``core/upgrade`` Migrator, bound to ``migrator.json``.
Synchronous, like the rest of kit's Python surface: migrations and drivers
are blocking callables, events go through the synchronous
:class:`hop_top_kit.bus.Bus`, and cancellation is a :class:`threading.Event`
passed to :meth:`Migrator.run`.
"""

from __future__ import annotations

import logging
import os
import shutil
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass

from hop_top_kit.bus import Bus
from hop_top_kit.upgrade.backup import DEFAULT_RETENTION, BackupStore
from hop_top_kit.upgrade.driver import Cancel, Locker, SchemaDriver, VersionStorer
from hop_top_kit.upgrade.errors import (
    Canceled,
    MigrateError,
    NotRestored,
    is_backup_unsupported,
    wrap,
)
from hop_top_kit.upgrade.events import (
    Emitter,
    MigrationTopics,
    migration_payload,
    millis,
    run_payload,
)
from hop_top_kit.upgrade.migration import Migration, Registry, default_registry
from hop_top_kit.upgrade.steps import (
    ERR_NO_BACKUP,
    Backup,
    Failure,
    down_ok,
    record_error,
    restore_from,
)
from hop_top_kit.upgrade.versionfile import resolve_data_dir, write_version_file

log = logging.getLogger("hop_top_kit.upgrade")

#: ``verify(schema, version)``: the final check after a schema's last
#: pending migration; raises to fail.
FinalVerify = Callable[[str, str], None]
#: ``baseline(schema)``: the version an unversioned store is already at,
#: ``""`` to leave it unversioned; raises to fail.
Baseline = Callable[[str], str]


@dataclass(frozen=True)
class AppliedMigration:
    """A successfully applied migration."""

    version: str
    schema: str


@dataclass
class SchemaStatus:
    """The migration state of one schema."""

    schema: str
    current: str
    target: str
    pending: int
    error: Exception | None = None


class Migrator:
    """Orchestrates versioned migrations across schema drivers.

    Args:
        tool: Tool name; part of the default data dir and of every event.
        version: Target version (a leading ``v`` is dropped).
        manual_rollback: Do not roll back on failure; :meth:`rollback_latest`
            does it on request.
        backup_retention: Backups kept per schema (default 5).
        registry: Migrations to read; default :func:`default_registry`.
        data_dir: Version files and backups live here instead of
            ``xdg.data_dir("hop/<tool>")``.
        verify: Final check after a schema's last pending migration, once
            that migration's version is recorded. A failure rolls back every
            migration applied to that schema in the run.
        baseline: What an unversioned store is already at; see :data:`Baseline`.
            Called only when ``version()`` returned ``""``, never by
            :meth:`status`; a failure is the schema's error (``baseline: ...``)
            before any backup or ``up``.
        bus: Where lifecycle events go; without one the Migrator is silent.
        topics: Event topics; default :class:`MigrationTopics`.
    """

    def __init__(
        self,
        tool: str,
        version: str,
        *,
        manual_rollback: bool = False,
        backup_retention: int = DEFAULT_RETENTION,
        registry: Registry | None = None,
        data_dir: str | os.PathLike[str] | None = None,
        verify: FinalVerify | None = None,
        baseline: Baseline | None = None,
        bus: Bus | None = None,
        topics: MigrationTopics | None = None,
    ) -> None:
        self._tool = tool
        self._version = version.removeprefix("v")
        self._auto_rollback = not manual_rollback
        self._retention = backup_retention
        self._registry = registry if registry is not None else default_registry()
        self._data_dir = data_dir
        self._verify = verify
        self._baseline = baseline
        self._emitter = Emitter(bus, topics or MigrationTopics())
        self._drivers: list[SchemaDriver] = []
        self._history: list[AppliedMigration] = []

    @property
    def tool(self) -> str:
        return self._tool

    @property
    def version(self) -> str:
        """The target version."""
        return self._version

    def add_driver(self, driver: SchemaDriver) -> None:
        self._drivers.append(driver)

    def history(self) -> list[AppliedMigration]:
        """Every migration this Migrator applied, in order (a copy)."""
        return list(self._history)

    def status(self) -> list[SchemaStatus]:
        """Pending counts per driver. Takes no lock and no baseline."""
        out: list[SchemaStatus] = []
        for d in self._drivers:
            stored, err = "", None
            try:
                stored = d.version()
            except Exception as e:
                err = e
            pending = self._registry.pending(d.name, stored, self._version)
            out.append(SchemaStatus(d.name, stored, self._version, len(pending), err))
        return out

    # -- run ---------------------------------------------------------------

    def run(self, cancel: Cancel | None = None) -> None:
        """Execute pending migrations for every driver, in order.

        Per schema: take the driver's lock when it is a :class:`Locker` (held
        until that schema's run ends, rollback included; a failed unlock is
        logged), read the version (when none is stored, ask the baseline and
        record its answer), collect pending migrations, back up (saving the
        version the run starts at beside the backup), then per migration
        ``up``, ``verify`` and the version record, then the final verify.
        A backup that raises :class:`BackupUnsupported` lets the run go on
        with no backup. A crash re-applies at most the one migration whose
        ``up`` was not recorded, so migrations should stay idempotent.

        On failure, in automatic mode: roll back (``down`` in reverse,
        recording the version before each, else restore and record the
        starting version). When a ``down`` is missing or fails and no backup
        can restore the schema, nothing is reverted: the error matches
        :func:`is_not_restored` and the stored version stays at the last step
        still applied. A *cancel* set between steps stops before the next
        ``up``. Raises :class:`MigrateError` (``migrate <schema>: ...``).
        An exception that is not an :class:`Exception` (``KeyboardInterrupt``)
        propagates untouched, after the lock is released.
        """
        cancel = cancel if cancel is not None else threading.Event()
        start = time.monotonic()
        em, tool, target = self._emitter, self._tool, self._version
        em.emit("run_started", run_payload(tool, target))
        applied = 0
        for d in self._drivers:
            try:
                applied += self._run_schema(cancel, d)
            except Exception as e:
                err = wrap(f"migrate {d.name}", e)
                em.emit(
                    "run_failed",
                    run_payload(
                        tool, target, schema=d.name, error=str(err), duration_ms=millis(start)
                    ),
                )
                raise err from e
        em.emit(
            "run_completed", run_payload(tool, target, applied=applied, duration_ms=millis(start))
        )

    def _lock(self, cancel: Cancel, d: SchemaDriver) -> Callable[[], None]:
        """The driver's lock, or a release that does nothing. A failed lock
        is ``lock: <err>``; a failed unlock is logged."""
        if not isinstance(d, Locker):
            return lambda: None
        try:
            unlock = d.lock(cancel)
        except Exception as e:
            raise wrap("lock", e) from e

        def release() -> None:
            try:
                unlock()
            except Exception as e:
                log.warning("unlock %s: %s", d.name, e)

        return release

    def _run_schema(self, cancel: Cancel, d: SchemaDriver) -> int:
        release = self._lock(cancel, d)
        try:
            try:
                current = d.version()
            except Exception as e:
                raise wrap("read version", e) from e
            if current == "" and self._baseline is not None:
                current = self._stamp_baseline(d)
            pending = self._registry.pending(d.name, current, self._version)
            if not pending:
                return 0
            store = self._store(d.name)
            bk = self._take_backup(store, d, pending[0].version, current)
            fail, n = self._apply_all(cancel, d, current, pending)
            if fail is not None:
                raise self._fail(d, current, bk, fail)
            if bk.path is not None:
                try:
                    store.prune()
                except OSError as e:
                    log.warning("prune old backups for %s: %s", d.name, e)
            return n
        finally:
            release()

    def _store(self, schema: str) -> BackupStore:
        return BackupStore(resolve_data_dir(self._tool, self._data_dir), schema, self._retention)

    def _take_backup(self, store: BackupStore, d: SchemaDriver, first: str, current: str) -> Backup:
        """Make the backup directory, ask the driver to fill it and save the
        starting version beside it. A backup that raises
        :class:`BackupUnsupported` is no failure: the directory this call
        created is removed again, nothing is saved, the run goes on."""
        existed = store.has_backup(first)
        try:
            path = store.backup_dir(first)
        except OSError as e:
            raise wrap("prepare backup", e) from e
        try:
            d.backup(path)
        except Exception as e:
            if not is_backup_unsupported(e):
                raise wrap("backup", e) from e
            if not existed:
                shutil.rmtree(path, ignore_errors=True)
            return Backup(unsupported=e)
        try:
            store.save_pre_run(path, current)
        except OSError as e:
            raise wrap("prepare backup", e) from e
        return Backup(path=path)

    def _stamp_baseline(self, d: SchemaDriver) -> str:
        """Ask the baseline what the unversioned store is at and record a
        non-empty answer; returns the version the run continues from. Runs
        under the schema's lock, between the version read and pending
        selection."""
        assert self._baseline is not None
        try:
            v = self._baseline(d.name)
        except Exception as e:
            raise wrap("baseline", e) from e
        if v == "":
            return ""
        try:
            self._set_version(d, v)
        except Exception as e:
            raise wrap("baseline", record_error(v, e)) from e
        self._emitter.emit("baselined", migration_payload(self._tool, d.name, "", v))
        return v

    def _apply_all(
        self, cancel: Cancel, d: SchemaDriver, current: str, pending: list[Migration]
    ) -> tuple[Failure | None, int]:
        """``up``, ``verify`` and the version record per pending migration,
        then the final verify: the failure, if any, and how many applied."""
        em, tool, schema = self._emitter, self._tool, d.name
        from_ = current
        applied: list[Migration] = []

        def failed(ev: dict, stage: str, err: Exception, undo: list[Migration]) -> Failure:
            ev.update(stage=stage, error=str(err))
            em.emit("failed", ev)
            return Failure(stage, ev["to"], ev["from"], err, undo)

        def ev(to: str, f: str, since: float | None = None, **kw: object) -> dict:
            dur = None if since is None else millis(since)
            return migration_payload(tool, schema, f, to, duration_ms=dur, **kw)  # type: ignore[arg-type]

        for mig in pending:
            if cancel.is_set():
                return failed(ev(mig.version, from_), "canceled", Canceled(), applied), len(applied)
            em.emit("started", ev(mig.version, from_))
            t = time.monotonic()
            try:
                mig.up()
            except Exception as e:
                return failed(ev(mig.version, from_, t), "up", e, applied), len(applied)
            em.emit("applied", ev(mig.version, from_, t))
            if mig.verify is not None:
                t = time.monotonic()
                try:
                    mig.verify()
                except Exception as e:
                    # up completed: the rollback must undo this step too.
                    f = failed(ev(mig.version, from_, t), "verify", e, [*applied, mig])
                    return f, len(applied)
                em.emit("verified", ev(mig.version, from_, t))
            t = time.monotonic()
            try:
                self._set_version(d, mig.version)
            except Exception as e:
                f = failed(ev(mig.version, from_, t), "record", e, [*applied, mig])
                return f, len(applied)
            applied.append(mig)
            self._history.append(AppliedMigration(mig.version, mig.schema))
            from_ = mig.version
        if self._verify is not None:
            t = time.monotonic()
            try:
                self._verify(schema, from_)
            except Exception as e:
                return failed(ev(from_, current, t), "final", e, applied), len(applied)
            em.emit("verified", ev(from_, current, t, final=True))
        return None, len(applied)

    def _fail(self, d: SchemaDriver, current: str, bk: Backup, f: Failure) -> Exception:
        """Roll back (automatic mode) and build the schema's error."""
        err = f.wrap()
        if not self._auto_rollback or not f.undo:
            return err
        t = time.monotonic()
        try:
            method = self._rollback(d, current, f.undo, bk)
        except Exception as rb:
            return MigrateError(f"{err} (rollback also failed: {rb})", err, rb)
        self._emitter.emit(
            "reverted",
            migration_payload(
                self._tool, d.name, f.version, current, method=method, duration_ms=millis(t)
            ),
        )
        return err

    def _rollback(self, d: SchemaDriver, current: str, applied: list[Migration], bk: Backup) -> str:
        """``down`` in reverse order, recording the version before each
        migration once its ``down`` succeeded; falls back to restore (and
        records the starting version) if a ``down`` is missing or fails.
        Returns which of the two undid the changes. A failed record stops
        the rollback: the store would not describe the data. With no
        backup, or a restore that is unsupported, nothing is restored:
        :class:`NotRestored`, and the version stays at the last record."""
        for i in range(len(applied) - 1, -1, -1):
            mig = applied[i]
            if not down_ok(mig):
                if bk.path is None:
                    raise NotRestored(ERR_NO_BACKUP, bk.unsupported)
                restore_from(d, bk.path)
                try:
                    self._set_version(d, current)
                except Exception as e:
                    raise record_error(current, e) from e
                return "restore"
            prev = applied[i - 1].version if i > 0 else current
            try:
                self._set_version(d, prev)
            except Exception as e:
                raise record_error(prev, e) from e
        return "down"

    # -- manual rollback ------------------------------------------------------

    def rollback_latest(self) -> None:
        """Restore the latest backup for each driver, then record the version
        the run that took it started at (saved beside the backup). A backup
        without that record is still restored, but the version is left as it
        is and the error says so. A :class:`Locker` is locked for its
        schema's rollback (lock, backup lookup, restore, version record,
        unlock); the version is not read and no event is published. A schema
        with no backup, or whose restore is unsupported, fails with an error
        matching :func:`is_not_restored`; nothing is recorded for it. Only
        works in manual rollback mode."""
        if self._auto_rollback:
            raise MigrateError("rollback: only available in manual rollback mode")
        for d in self._drivers:
            try:
                self._rollback_one(d)
            except Exception as e:
                raise wrap(f"rollback {d.name}", e) from e

    def _rollback_one(self, d: SchemaDriver) -> None:
        release = self._lock(threading.Event(), d)
        try:
            latest = self._store(d.name).latest_backup()
            pre_run, recorded = BackupStore.read_pre_run(latest)
            restore_from(d, latest)
            if not recorded:
                raise MigrateError(
                    f"restored {latest.name} but it records no pre-run version; version left as is"
                )
            try:
                self._set_version(d, pre_run)
            except Exception as e:
                raise record_error(pre_run, e) from e
        finally:
            release()

    # -- version record --------------------------------------------------------

    def _set_version(self, d: SchemaDriver, version: str) -> None:
        """Record *version*. A :class:`VersionStorer` owns its version: only
        ``set_version`` is called, no version file is read or written. Any
        other driver gets the version file first (the Migrator's canonical
        copy; drivers read it back with ``read_version_file``) and then
        ``set_version`` as a notification, so a refused ``set_version``
        leaves the file at the new version."""
        if isinstance(d, VersionStorer) and d.stores_version():
            d.set_version(version)
            return
        base = resolve_data_dir(self._tool, self._data_dir)
        try:
            write_version_file(base, d.name, version)
        except OSError as e:
            raise MigrateError(f"write version file: {e}", e) from e
        d.set_version(version)


__all__ = [
    "AppliedMigration",
    "Baseline",
    "FinalVerify",
    "Migrator",
    "SchemaStatus",
]
