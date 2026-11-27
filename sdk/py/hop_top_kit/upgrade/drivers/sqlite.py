"""A schema driver for SQLite database files, the Python port of
``go/core/upgrade/driver/sqlite``.

Two modes. Path only: ``SqliteDriver(db_path)`` opens its own short-lived
connection for ``backup`` and swaps the file on ``restore``. Connection:
``SqliteDriver(db_path, connect=...)`` backs up through a PEP 249
connection the caller's callable returns, never closes it, and cannot
restore (the file cannot be replaced under a live connection).

The driver is a :class:`~hop_top_kit.upgrade.Locker`: an exclusive
``flock(2)`` lock on ``<data dir>/<name>/.lock``, the file the Go driver
locks, so a Go and a Python Migrator on one database exclude each other.
"""

from __future__ import annotations

import contextlib
import os
import shutil
import sqlite3
import sys
import tempfile
import threading
from collections.abc import Callable
from pathlib import Path
from typing import Any
from urllib.parse import quote

from hop_top_kit.upgrade.driver import Cancel
from hop_top_kit.upgrade.errors import BackupUnsupported, Canceled, UpgradeError
from hop_top_kit.upgrade.versionfile import (
    read_version_file,
    read_version_file_in,
    resolve_data_dir,
)

if sys.platform == "win32":  # pragma: no cover - not testable on POSIX hosts
    import msvcrt
else:
    import fcntl

#: Files SQLite keeps beside a database. A ``-wal`` or hot ``-journal`` left
#: from the replaced database would be replayed onto the restored one when
#: it is next opened; ``-shm`` indexes that WAL.
SIDECARS = ("-wal", "-shm", "-journal")

#: Seconds the driver's own connection waits on a lock.
BUSY_TIMEOUT = 5.0
#: Seconds between non-blocking lock attempts of a waiting ``lock``.
LOCK_POLL = 0.05
LOCK_FILE = ".lock"

Connect = Callable[[], Any]  # returns a PEP 249 connection


class SqliteDriver:
    """Version tracking, WAL-safe backup, restore and a file lock for one
    SQLite database file.

    Args:
        db_path: The database file. With *connect* it only names the backup
            file and may be omitted.
        connect: Returns a PEP 249 connection to the database (an
            SQLAlchemy user passes ``engine.raw_connection``). ``backup``
            calls it, runs ``VACUUM INTO`` and never closes what it got;
            ``restore`` raises :class:`BackupUnsupported`. The connection
            must not be inside a transaction (``VACUUM`` cannot run in one):
            ``backup`` then raises :class:`UpgradeError` and touches nothing.
        name: Schema name; default ``"sqlite"``.
        tool: Tool name, for the default data dir ``xdg.data_dir("hop/<tool>")``.
        data_dir: The Migrator's ``data_dir``, when it has one.
    """

    def __init__(
        self,
        db_path: str | os.PathLike[str] | None = None,
        *,
        connect: Connect | None = None,
        name: str = "sqlite",
        tool: str = "",
        data_dir: str | os.PathLike[str] | None = None,
    ) -> None:
        if db_path is None and connect is None:
            raise ValueError("SqliteDriver needs a db_path or a connect callable")
        self._db_path = os.fspath(db_path) if db_path is not None else ""
        self._connect = connect
        self._name = name
        self._tool = tool
        self._data_dir = data_dir

    @property
    def name(self) -> str:
        return self._name

    def version(self) -> str:
        if self._data_dir:
            return read_version_file_in(self._data_dir, self._name)
        return read_version_file(self._tool, self._name)

    def set_version(self, version: str) -> None:
        """No-op: the Migrator writes the version file."""

    def _state_dir(self) -> Path:
        return resolve_data_dir(self._tool, self._data_dir) / self._name

    # -- backup / restore ---------------------------------------------------

    def _backup_name(self) -> str:
        return os.path.basename(self._db_path) if self._db_path else self._name + ".db"

    def backup(self, dest: Path) -> None:
        """Write a consistent snapshot to ``<dest>/<database file name>``
        with ``VACUUM INTO``, which reads through the WAL.

        Path only: a missing database file is "nothing to back up" (returns,
        no file). A leftover target is replaced; the snapshot goes to a
        ``.tmp`` file first and is renamed.
        """
        if self._connect is None and not os.path.exists(self._db_path):
            return
        dest = Path(dest)
        dest.mkdir(mode=0o750, parents=True, exist_ok=True)
        target = dest / self._backup_name()
        tmp = Path(str(target) + ".tmp")
        # VACUUM INTO refuses an existing file; a leftover is stale.
        tmp.unlink(missing_ok=True)
        target.unlink(missing_ok=True)
        try:
            self._vacuum_into(tmp)
            os.replace(tmp, target)
        except BaseException:
            tmp.unlink(missing_ok=True)
            raise

    def _vacuum_into(self, file: Path) -> None:
        # The file name is a bound parameter: no path needs quoting.
        if self._connect is not None:
            _vacuum(self._connect(), file)
            return
        own = sqlite3.connect(_file_uri(self._db_path), uri=True, timeout=BUSY_TIMEOUT)
        try:
            _vacuum(own, file)
        finally:
            own.close()

    def restore(self, src: Path) -> None:
        """Replace the database file with the backup in *src*, then remove
        the replaced database's ``-wal``, ``-shm`` and ``-journal`` files.

        Precondition: no open connection on the file, in this process or
        another. With *connect* it restores nothing and raises
        :class:`BackupUnsupported`; the backup file stays for manual
        recovery. The backup is copied to a temp file beside the database,
        synced and renamed over it, so a failure before the rename leaves the
        database and its sidecars untouched.
        """
        if self._connect is not None:
            raise BackupUnsupported(
                f"restore {self._db_path} under a caller-owned connection: "
                "upgrade: backup unsupported"
            )
        backup = Path(src) / self._backup_name()
        db = Path(self._db_path)
        db.parent.mkdir(mode=0o750, parents=True, exist_ok=True)
        mode = 0o600
        with contextlib.suppress(FileNotFoundError):
            mode = db.stat().st_mode & 0o777
        fd, tmp_name = tempfile.mkstemp(prefix=db.name + ".restore-", dir=db.parent)
        try:
            with open(backup, "rb") as inp, os.fdopen(fd, "wb") as out:
                shutil.copyfileobj(inp, out)
                out.flush()
                os.fsync(out.fileno())
            os.chmod(tmp_name, mode)
            os.replace(tmp_name, db)
        except BaseException:
            Path(tmp_name).unlink(missing_ok=True)
            raise
        errs: list[str] = []
        for suffix in SIDECARS:
            p = self._db_path + suffix
            try:
                os.remove(p)
            except FileNotFoundError:
                pass
            except OSError as e:
                errs.append(f"remove stale {p}: {e}")
        if sys.platform != "win32":
            try:
                _fsync_dir(db.parent)
            except OSError as e:
                errs.append(f"fsync dir {db.parent}: {e}")
        if errs:
            raise OSError(f"restore: {self._db_path} replaced: " + "; ".join(errs))

    # -- lock ---------------------------------------------------------------

    def lock(self, cancel: Cancel) -> Callable[[], None]:
        """Take an exclusive advisory file lock on
        ``<data dir>/<name>/.lock``, creating the directory, and block until
        it is held or *cancel* is set (then raise :class:`Canceled`, holding
        nothing). It excludes other processes, other drivers in this process
        and a Go Migrator on the same data dir. The file is never deleted.
        Returns the unlock.
        """
        directory = self._state_dir()
        directory.mkdir(mode=0o750, parents=True, exist_ok=True)
        fd = os.open(directory / LOCK_FILE, os.O_RDWR | os.O_CREAT, 0o600)
        try:
            while not _try_lock(fd):
                if cancel.wait(LOCK_POLL):
                    raise Canceled
        except BaseException:
            os.close(fd)
            raise
        return _unlocker(fd)


def _vacuum(conn: Any, file: Path) -> None:
    if getattr(conn, "in_transaction", False):
        raise UpgradeError(
            "backup db: connection has an open transaction (VACUUM cannot run in one): "
            "commit or roll it back before migrating; for autocommit=False use autocommit=True"
        )
    cur = conn.cursor()
    try:
        cur.execute("VACUUM INTO ?", (os.fspath(file),))
    except Exception as e:
        raise UpgradeError(f"backup db: {e}") from e
    finally:
        cur.close()


def _file_uri(path: str) -> str:
    """A read-write, never-create URI for *path*, escaped so spaces, ``?``,
    ``#`` and ``%`` stay part of the file name."""
    p = os.path.abspath(path).replace(os.sep, "/")
    if not p.startswith("/"):
        p = "/" + p  # a Windows drive path
    return "file:" + quote(p) + "?mode=rw"


def _fsync_dir(directory: Path) -> None:
    fd = os.open(directory, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _try_lock(fd: int) -> bool:
    if sys.platform == "win32":  # pragma: no cover
        try:
            os.lseek(fd, 0, os.SEEK_SET)
            msvcrt.locking(fd, msvcrt.LK_NBLCK, 1)
        except OSError:
            return False
        return True
    try:
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        return False
    return True


def _unlocker(fd: int) -> Callable[[], None]:
    once = threading.Lock()
    done = False

    def unlock() -> None:
        nonlocal done
        with once:
            if done:
                return
            done = True
        try:
            if sys.platform == "win32":  # pragma: no cover
                os.lseek(fd, 0, os.SEEK_SET)
                msvcrt.locking(fd, msvcrt.LK_UNLCK, 1)
            else:
                fcntl.flock(fd, fcntl.LOCK_UN)
        finally:
            os.close(fd)

    return unlock
