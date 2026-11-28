"""Step bookkeeping shared by the Migrator's run and rollback: what a failed
step looks like, what a schema's run can restore from, and the helpers that
undo one step."""

from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path

from hop_top_kit.upgrade.driver import SchemaDriver
from hop_top_kit.upgrade.errors import MigrateError, NotRestored, is_backup_unsupported, wrap
from hop_top_kit.upgrade.migration import Migration, VerifyError

#: Texts of a rollback that restored nothing; ports pin the same words.
ERR_NO_BACKUP = "not restored: no backup was taken"
ERR_RESTORE_UNSUPPORTED = "not restored: restore unsupported"


@dataclass
class Failure:
    """What stopped a schema's run, and what to roll back."""

    stage: str  # up, verify, record, final, canceled
    version: str
    from_: str
    err: Exception
    undo: list[Migration] = field(default_factory=list)
    #: The newest ``undo`` entry is a step whose ``up`` failed: it counts as
    #: applied, but its ``down`` is never called.
    up_failed: bool = False

    def wrap(self) -> MigrateError:
        v, e = self.version, self.err
        if self.stage == "verify":
            return verify_failure(f"verify {v} failed", e)
        if self.stage == "final":
            return verify_failure(f"final verify {v} failed", e)
        if self.stage == "record":
            return record_error(v, e)
        if self.stage == "canceled":
            return wrap(f"up {v} canceled", e)
        return wrap(f"up {v} failed", e)


@dataclass
class Backup:
    """What a schema's run can restore from: a directory, or none (path
    ``None``) with why: the :class:`BackupUnsupported` the driver raised."""

    path: Path | None = None
    unsupported: Exception | None = None


def verify_failure(prefix: str, err: Exception) -> MigrateError:
    v = VerifyError(str(err))
    v.__cause__ = err
    return wrap(prefix, v)


def record_error(version: str, err: Exception) -> MigrateError:
    """``record <version> failed: <err>``; an empty version is quoted."""
    quoted = version or '""'
    return wrap(f"record {quoted} failed", err)


def down_ok(mig: Migration) -> bool:
    if mig.down is None:
        return False
    try:
        mig.down()
    except Exception:
        return False
    return True


def restore_from(d: SchemaDriver, path: Path) -> None:
    """``d.restore(path)``; an unsupported restore becomes
    :class:`NotRestored` with the driver's error as its cause."""
    try:
        d.restore(path)
    except Exception as e:
        if is_backup_unsupported(e):
            raise NotRestored(ERR_RESTORE_UNSUPPORTED, e) from e
        raise
