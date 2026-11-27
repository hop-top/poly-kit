"""The SQLite driver under the real Migrator: rollback in both modes, two
processes on one file, and the baseline-on-an-old-database case."""

from __future__ import annotations

import sqlite3
import subprocess
import sys
import threading
from pathlib import Path

import pytest

from hop_top_kit.upgrade import (
    MigrateError,
    Migration,
    Migrator,
    Registry,
    is_backup_unsupported,
    is_not_restored,
    read_version_file_in,
)
from hop_top_kit.upgrade.drivers import SqliteDriver
from tests.sqlite_helpers import Proxy, open_wal, rows

SCHEMA = "appdb"


def bump(path: Path, value: int):
    """An ``up`` that opens its own connection, as an app's migration does."""

    def up() -> None:
        conn = sqlite3.connect(path)
        try:
            conn.execute("INSERT INTO t VALUES (?)", (value,))
            conn.commit()
        finally:
            conn.close()

    return up


def boom() -> None:
    raise RuntimeError("boom")


def new_db(path: Path) -> None:
    conn = open_wal(path, autocheckpoint=True)
    conn.execute("CREATE TABLE t (x INTEGER)")
    conn.execute("INSERT INTO t VALUES (0)")
    conn.close()


def registry(*migs: Migration) -> Registry:
    reg = Registry()
    for m in migs:
        reg.register(m)
    return reg


def migrator(data: Path, reg: Registry, driver: SqliteDriver, **kw) -> Migrator:
    m = Migrator("tool", "1.2.0", registry=reg, data_dir=data, **kw)
    m.add_driver(driver)
    return m


def test_path_mode_failed_run_restores_the_file(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    new_db(db)
    reg = registry(
        Migration("1.1.0", SCHEMA, bump(db, 11)),
        Migration("1.2.0", SCHEMA, boom),
    )
    m = migrator(tmp_path / "data", reg, SqliteDriver(db, name=SCHEMA, data_dir=tmp_path / "data"))
    with pytest.raises(MigrateError):
        m.run(threading.Event())
    assert rows(db) == [0]  # 1.1.0's row rolled back by restore, no Down needed
    assert read_version_file_in(tmp_path / "data", SCHEMA) == ""
    assert not (tmp_path / "app.db-wal").exists()


def test_connect_mode_missing_down_ends_not_restored(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    new_db(db)
    live = sqlite3.connect(db, isolation_level=None)
    proxy = Proxy(live)
    reg = registry(
        Migration("1.1.0", SCHEMA, bump(db, 11)),
        Migration("1.2.0", SCHEMA, boom),
    )
    d = SqliteDriver(db, connect=lambda: proxy, name=SCHEMA, data_dir=tmp_path / "data")
    m = migrator(tmp_path / "data", reg, d)
    with pytest.raises(MigrateError) as ei:
        m.run(threading.Event())
    assert is_not_restored(ei.value)
    assert is_backup_unsupported(ei.value)
    assert rows(db) == [0, 11]  # nothing restored
    assert read_version_file_in(tmp_path / "data", SCHEMA) == "1.1.0"  # last record
    assert proxy.closed == 0
    live.execute("SELECT 1")  # the caller's connection is still open
    live.close()


def test_connect_mode_with_down_rolls_back_through_down(tmp_path: Path) -> None:
    db = tmp_path / "app.db"
    new_db(db)
    live = sqlite3.connect(db, isolation_level=None)

    def down() -> None:
        live.execute("DELETE FROM t WHERE x = 11")

    reg = registry(
        Migration("1.1.0", SCHEMA, lambda: live.execute("INSERT INTO t VALUES (11)"), down),
        Migration("1.2.0", SCHEMA, boom, None),
    )
    d = SqliteDriver(db, connect=lambda: live, name=SCHEMA, data_dir=tmp_path / "data")
    m = migrator(tmp_path / "data", reg, d)
    with pytest.raises(MigrateError):
        m.run(threading.Event())
    assert rows(db) == [0]
    assert read_version_file_in(tmp_path / "data", SCHEMA) == ""  # down recorded the earlier one
    live.close()


def test_manual_rollback_latest_path_mode(tmp_path: Path) -> None:
    db, data = tmp_path / "app.db", tmp_path / "data"
    new_db(db)
    reg = registry(
        Migration("1.1.0", SCHEMA, bump(db, 11)), Migration("1.2.0", SCHEMA, bump(db, 12))
    )
    m = migrator(data, reg, SqliteDriver(db, name=SCHEMA, data_dir=data), manual_rollback=True)
    m.run(threading.Event())
    assert rows(db) == [0, 11, 12]
    assert read_version_file_in(data, SCHEMA) == "1.2.0"

    m.rollback_latest()

    assert rows(db) == [0]
    assert read_version_file_in(data, SCHEMA) == ""  # the version the run started at


def test_manual_rollback_latest_connect_mode_is_not_restored(tmp_path: Path) -> None:
    db, data = tmp_path / "app.db", tmp_path / "data"
    new_db(db)
    live = sqlite3.connect(db, isolation_level=None)
    reg = registry(Migration("1.1.0", SCHEMA, lambda: live.execute("INSERT INTO t VALUES (11)")))
    d = SqliteDriver(db, connect=lambda: live, name=SCHEMA, data_dir=data)
    m = migrator(data, reg, d, manual_rollback=True)
    m.run(threading.Event())
    backup = next((data / "backups" / SCHEMA).glob("pre-*"))
    assert rows(backup / "app.db") == [0]  # the backup was taken on the live handle

    with pytest.raises(MigrateError) as ei:
        m.rollback_latest()

    assert is_not_restored(ei.value) or is_backup_unsupported(ei.value)
    assert rows(db) == [0, 11]
    assert read_version_file_in(data, SCHEMA) == "1.1.0"
    assert (backup / "app.db").exists()  # kept for manual recovery
    live.execute("SELECT 1")
    live.close()


# -- the first adopter's case: an additive column, a baseline, an old db ------

CREATE_NEW = "CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT, note TEXT)"


def run_additive(db: Path, data: Path, calls: list[str]) -> Migrator:
    def add_note() -> None:
        calls.append("add note")
        conn = sqlite3.connect(db)
        try:
            conn.execute("ALTER TABLE items ADD COLUMN note TEXT")
            conn.commit()
        finally:
            conn.close()

    def baseline(schema: str) -> str:
        conn = sqlite3.connect(db)
        try:
            cols = {r[1] for r in conn.execute("PRAGMA table_info(items)")}
        finally:
            conn.close()
        if not cols:
            return ""
        return "1.1.0" if "note" in cols else "1.0.0"

    m = Migrator(
        "tool",
        "1.1.0",
        registry=registry(Migration("1.1.0", SCHEMA, add_note)),
        data_dir=data,
        baseline=baseline,
    )
    m.add_driver(SqliteDriver(db, name=SCHEMA, data_dir=data))
    m.run(threading.Event())
    return m


def test_additive_column_runs_once_on_an_old_database(tmp_path: Path) -> None:
    db, data = tmp_path / "old.db", tmp_path / "data"
    conn = sqlite3.connect(db)  # an existing install, before versioning
    conn.execute("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)")
    conn.execute("INSERT INTO items (name) VALUES ('keep')")
    conn.commit()
    conn.close()
    calls: list[str] = []

    run_additive(db, data, calls)
    run_additive(db, data, calls)  # a second start does nothing

    assert calls == ["add note"]
    assert rows(db, "SELECT name FROM items") == ["keep"]
    assert rows(db, "SELECT name FROM pragma_table_info('items') ORDER BY cid") == [
        "id",
        "name",
        "note",
    ]
    assert read_version_file_in(data, SCHEMA) == "1.1.0"
    assert (data / "backups" / SCHEMA / "pre-1.1.0" / "old.db").exists()


def test_additive_column_never_runs_on_a_fresh_database(tmp_path: Path) -> None:
    db, data = tmp_path / "fresh.db", tmp_path / "data"
    conn = sqlite3.connect(db)
    conn.execute(CREATE_NEW)  # the app's current schema, column included
    conn.commit()
    conn.close()
    calls: list[str] = []

    run_additive(db, data, calls)

    assert calls == []
    assert read_version_file_in(data, SCHEMA) == "1.1.0"


# -- two processes on one file --------------------------------------------------


def child_main(db: str, data: str) -> None:
    import time

    def up_for(v: str):
        def up() -> None:
            time.sleep(0.2)  # a window for the other process
            conn = sqlite3.connect(db, timeout=10)
            conn.execute("INSERT INTO applied VALUES (?, ?)", (v, __import__("os").getpid()))
            conn.commit()
            conn.close()

        return up

    reg = registry(*(Migration(v, SCHEMA, up_for(v)) for v in ("1.1.0", "1.2.0")))
    m = Migrator("tool", "1.2.0", registry=reg, data_dir=data)
    m.add_driver(SqliteDriver(db, name=SCHEMA, data_dir=data))
    m.run(threading.Event())


def test_two_processes_apply_each_migration_once(tmp_path: Path) -> None:
    db, data = tmp_path / "app.db", tmp_path / "data"
    conn = open_wal(db, autocheckpoint=True)
    conn.execute("CREATE TABLE applied (version TEXT, pid INTEGER)")
    conn.close()
    procs = [
        subprocess.Popen(
            [sys.executable, __file__, str(db), str(data)],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        for _ in range(2)
    ]
    outs = [p.communicate(timeout=60)[0] for p in procs]
    assert [p.returncode for p in procs] == [0, 0], outs

    got = rows(db, "SELECT version FROM applied ORDER BY version")
    assert got == ["1.1.0", "1.2.0"]
    assert read_version_file_in(data, SCHEMA) == "1.2.0"


if __name__ == "__main__":
    child_main(sys.argv[1], sys.argv[2])
