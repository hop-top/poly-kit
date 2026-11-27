"""The SQLite schema driver: backup, restore, transactions and the lock."""

from __future__ import annotations

import os
import sqlite3
import subprocess
import sys
import textwrap
import threading
import time
from pathlib import Path

import pytest

from hop_top_kit.upgrade import (
    BackupUnsupported,
    Canceled,
    Locker,
    SchemaDriver,
    UpgradeError,
)
from hop_top_kit.upgrade.drivers import SqliteDriver
from tests.sqlite_helpers import Proxy, open_wal, rows

ODD = "a b'\"?#%&;.db"


def seed(path: Path, values: tuple[int, ...] = (1, 2, 3)) -> sqlite3.Connection:
    conn = open_wal(path)
    conn.execute("CREATE TABLE t (x INTEGER)")
    conn.executemany("INSERT INTO t VALUES (?)", [(v,) for v in values])
    return conn


def test_driver_satisfies_the_protocols(tmp_path: Path) -> None:
    d = SqliteDriver(tmp_path / "a.db", data_dir=tmp_path)
    assert isinstance(d, SchemaDriver)
    assert isinstance(d, Locker)
    assert d.name == "sqlite"
    assert d.version() == ""
    d.set_version("9.9.9")  # a no-op
    assert d.version() == ""


def test_needs_a_path_or_a_connect() -> None:
    with pytest.raises(ValueError):
        SqliteDriver()


@pytest.mark.parametrize("mode", ["path", "connect"])
def test_backup_has_rows_still_in_the_wal(tmp_path: Path, mode: str) -> None:
    db = tmp_path / "app.db"
    live = seed(db)  # open, never checkpointed
    try:
        assert (tmp_path / "app.db-wal").stat().st_size > 0
        assert rows(Path(db)) == [1, 2, 3]  # sanity: a reader sees the WAL
        dest = tmp_path / "bk"
        d = SqliteDriver(db) if mode == "path" else SqliteDriver(db, connect=lambda: live)
        d.backup(dest)
        # The main file alone does not hold the rows: a file copy would lose them.
        assert rows(dest / "app.db") == [1, 2, 3]
        assert not (dest / "app.db.tmp").exists()
    finally:
        live.close()


def test_backup_of_a_missing_file_is_nothing_to_back_up(tmp_path: Path) -> None:
    dest = tmp_path / "bk"
    SqliteDriver(tmp_path / "none.db").backup(dest)
    assert not (dest / "none.db").exists()
    assert not (tmp_path / "none.db").exists()  # never created


def test_backup_replaces_a_stale_target_and_tmp(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    seed(db).close()
    dest = tmp_path / "bk"
    dest.mkdir()
    (dest / "app.db").write_text("stale")
    (dest / "app.db.tmp").write_text("stale")
    SqliteDriver(db).backup(dest)
    assert rows(dest / "app.db") == [1, 2, 3]
    assert not (dest / "app.db.tmp").exists()


def test_backup_without_a_path_is_named_for_the_schema(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    live = seed(db)
    try:
        SqliteDriver(connect=lambda: live, name="appdb").backup(tmp_path / "bk")
    finally:
        live.close()
    assert rows(tmp_path / "bk" / "appdb.db") == [1, 2, 3]


@pytest.mark.parametrize("mode", ["path", "connect"])
def test_odd_paths_round_trip(tmp_path: Path, mode: str) -> None:
    root = tmp_path / "dir with 'quotes' & spaces?#"
    root.mkdir()
    db = root / ODD
    live = seed(db)
    d = SqliteDriver(db) if mode == "path" else SqliteDriver(db, connect=lambda: live)
    dest = tmp_path / "backup dir 'x'" / "pre-1.0.0"
    try:
        d.backup(dest)
    finally:
        live.close()
    assert rows(dest / ODD) == [1, 2, 3]
    if mode == "path":
        d.restore(dest)
        assert rows(db) == [1, 2, 3]


def test_path_restore_drops_stale_sidecars_and_swaps_the_file(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    seed(db, (1, 2)).close()
    dest = tmp_path / "bk"
    d = SqliteDriver(db)
    d.backup(dest)
    live = open_wal(db)  # change the live db, leave -wal/-shm behind
    live.execute("INSERT INTO t VALUES (99)")
    assert (tmp_path / "app.db-wal").exists()
    live.close()
    for suffix in ("-wal", "-shm", "-journal"):
        (tmp_path / f"app.db{suffix}").write_bytes(b"stale")
    os.chmod(db, 0o640)

    d.restore(dest)

    for suffix in ("-wal", "-shm", "-journal"):  # before any open can clean them
        assert not (tmp_path / f"app.db{suffix}").exists()
    assert rows(db) == [1, 2]
    assert (db.stat().st_mode & 0o777) == 0o640
    assert [p.name for p in tmp_path.iterdir() if ".restore-" in p.name] == []


def test_restore_of_a_missing_backup_leaves_the_database_alone(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    seed(db).close()
    (tmp_path / "app.db-wal").write_bytes(b"keep")
    with pytest.raises(FileNotFoundError):
        SqliteDriver(db).restore(tmp_path / "nobackup")
    assert (tmp_path / "app.db-wal").read_bytes() == b"keep"
    assert (tmp_path / "app.db").stat().st_size > 0
    assert [p.name for p in tmp_path.iterdir() if ".restore-" in p.name] == []


def test_connect_restore_is_unsupported_and_touches_nothing(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    live = seed(db)
    bk = tmp_path / "bk"
    bk.mkdir()
    (bk / "app.db").write_text("backup bytes")
    d = SqliteDriver(db, connect=lambda: live)
    try:
        with pytest.raises(BackupUnsupported):
            d.restore(bk)
        assert rows(db) == [1, 2, 3]
        assert (bk / "app.db").read_text() == "backup bytes"
    finally:
        live.close()


def test_connect_is_not_closed_and_stays_usable(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    proxy = Proxy(seed(db))
    d = SqliteDriver(db, connect=lambda: proxy)
    d.backup(tmp_path / "bk")
    d.backup(tmp_path / "bk")  # again over the same proxy
    assert proxy.closed == 0
    proxy.cursor().execute("INSERT INTO t VALUES (4)")  # still open
    proxy.close()
    assert rows(tmp_path / "bk" / "app.db") == [1, 2, 3]


def test_connection_inside_a_transaction_fails_clearly(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    seed(db).close()
    live = sqlite3.connect(db)  # legacy mode: DML opens a transaction
    live.execute("INSERT INTO t VALUES (7)")
    assert live.in_transaction
    d = SqliteDriver(db, connect=lambda: live)
    with pytest.raises(UpgradeError, match="open transaction"):
        d.backup(tmp_path / "bk")
    # The caller's work is neither committed nor lost.
    assert live.in_transaction
    assert not (tmp_path / "bk" / "app.db").exists()
    assert not (tmp_path / "bk" / "app.db.tmp").exists()
    live.rollback()
    assert [r[0] for r in live.execute("SELECT x FROM t")] == [1, 2, 3]
    d.backup(tmp_path / "bk")  # fine once the transaction ended
    live.close()
    assert rows(tmp_path / "bk" / "app.db") == [1, 2, 3]


@pytest.mark.skipif(sys.version_info < (3, 12), reason="sqlite3 autocommit attribute is 3.12+")
def test_autocommit_false_connection_fails_clearly(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    seed(db).close()
    live = sqlite3.connect(db, autocommit=False)  # a transaction is always open
    try:
        with pytest.raises(UpgradeError, match="autocommit"):
            SqliteDriver(db, connect=lambda: live).backup(tmp_path / "bk")
    finally:
        live.close()


# -- lock ------------------------------------------------------------------


def lock_path(data: Path, name: str = "sqlite") -> Path:
    return data / name / ".lock"


def test_lock_file_lives_in_the_version_dir_and_is_never_deleted(tmp_path: Path) -> None:
    d = SqliteDriver(tmp_path / "a.db", name="appdb", data_dir=tmp_path / "data")
    unlock = d.lock(threading.Event())
    assert lock_path(tmp_path / "data", "appdb").is_file()
    unlock()
    unlock()  # idempotent
    assert lock_path(tmp_path / "data", "appdb").is_file()


def test_default_data_dir_follows_the_tool_name(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("XDG_DATA_HOME", str(tmp_path))
    d = SqliteDriver(tmp_path / "a.db", tool="mytool")
    d.lock(threading.Event())()
    assert (tmp_path / "hop" / "mytool" / "sqlite" / ".lock").is_file()


def test_two_drivers_in_one_process_exclude_each_other(tmp_path: Path) -> None:
    a = SqliteDriver(tmp_path / "a.db", data_dir=tmp_path)
    b = SqliteDriver(tmp_path / "a.db", data_dir=tmp_path)
    unlock_a = a.lock(threading.Event())
    got: list[object] = []
    t = threading.Thread(target=lambda: got.append(b.lock(threading.Event())))
    t.start()
    time.sleep(0.2)
    assert got == []  # still waiting
    unlock_a()
    t.join(5)
    assert len(got) == 1
    got[0]()


def test_cancelled_wait_raises_canceled_and_holds_nothing(tmp_path: Path) -> None:
    a = SqliteDriver(tmp_path / "a.db", data_dir=tmp_path)
    b = SqliteDriver(tmp_path / "a.db", data_dir=tmp_path)
    unlock_a = a.lock(threading.Event())
    cancel = threading.Event()
    threading.Timer(0.15, cancel.set).start()
    start = time.monotonic()
    with pytest.raises(Canceled):
        b.lock(cancel)
    assert time.monotonic() - start < 2
    # Nothing is left held by b: once a lets go, a third taker gets it at once.
    unlock_a()
    c = SqliteDriver(tmp_path / "a.db", data_dir=tmp_path)
    t0 = time.monotonic()
    c.lock(threading.Event())()
    assert time.monotonic() - t0 < 0.5


def test_a_lock_already_cancelled_still_takes_a_free_lock(tmp_path: Path) -> None:
    cancel = threading.Event()
    cancel.set()
    SqliteDriver(tmp_path / "a.db", data_dir=tmp_path).lock(cancel)()


HOLDER = textwrap.dedent(
    """
    import fcntl, os, sys, time
    fd = os.open(sys.argv[1], os.O_RDWR | os.O_CREAT, 0o600)
    fcntl.flock(fd, fcntl.LOCK_EX)  # plain flock(2): what the Go driver takes
    print("held", flush=True)
    time.sleep(float(sys.argv[2]))
    """
)


@pytest.mark.skipif(sys.platform == "win32", reason="flock holder is POSIX")
def test_a_flock_held_by_another_process_blocks_the_driver(tmp_path: Path) -> None:
    """Interop evidence: a process holding flock(2) on the file the Go driver
    locks (``<data dir>/<name>/.lock``) excludes this driver."""
    path = lock_path(tmp_path, "appdb")
    path.parent.mkdir(parents=True)
    holder = subprocess.Popen(
        [sys.executable, "-c", HOLDER, str(path), "1.0"],
        stdout=subprocess.PIPE,
        text=True,
    )
    try:
        assert holder.stdout is not None
        assert holder.stdout.readline().strip() == "held"
        d = SqliteDriver(tmp_path / "a.db", name="appdb", data_dir=tmp_path)
        cancel = threading.Event()
        threading.Timer(0.3, cancel.set).start()
        with pytest.raises(Canceled):
            d.lock(cancel)
        t0 = time.monotonic()
        unlock = d.lock(threading.Event())  # after the holder exits
        assert time.monotonic() - t0 < 5
        unlock()
    finally:
        holder.kill()
        holder.wait()


@pytest.mark.skipif(sys.platform == "win32", reason="flock probe is POSIX")
def test_the_driver_lock_blocks_a_plain_flock_in_another_process(tmp_path: Path) -> None:
    d = SqliteDriver(tmp_path / "a.db", name="appdb", data_dir=tmp_path)
    unlock = d.lock(threading.Event())
    probe = (
        "import fcntl, os, sys\n"
        "fd = os.open(sys.argv[1], os.O_RDWR)\n"
        "try:\n"
        "    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)\n"
        "except BlockingIOError:\n"
        "    sys.exit(7)\n"
    )
    held = subprocess.run([sys.executable, "-c", probe, str(lock_path(tmp_path, "appdb"))])
    assert held.returncode == 7
    unlock()
    free = subprocess.run([sys.executable, "-c", probe, str(lock_path(tmp_path, "appdb"))])
    assert free.returncode == 0
