"""PostgresDriver behaviour that needs no database: table-name rule, the SQL
as sent, agreement with the Go driver's shared text, the import check, and
the lock/unlock/discard decisions over fake PEP 249 connections. Real Postgres
is in test_upgrade_postgres_integration.py."""

from __future__ import annotations

import re
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any

import pytest

from hop_top_kit.upgrade import (
    BackupUnsupported,
    Canceled,
    Locker,
    SchemaDriver,
    UpgradeError,
    VersionStorer,
)
from hop_top_kit.upgrade.drivers import PostgresDriver
from hop_top_kit.upgrade.drivers import postgres as pg

REPO = Path(__file__).resolve().parents[3]
GO_LOCK = REPO / "go/core/upgrade/driver/postgres/lock.go"
GO_DRIVER = REPO / "go/core/upgrade/driver/postgres/driver.go"


class FakeCursor:
    def __init__(self, conn: FakeConn) -> None:
        self.conn = conn
        self.row: tuple[Any, ...] | None = None

    def execute(self, sql: str, params: Any = None) -> None:
        self.conn.log.append(("execute", " ".join(sql.split()), params))
        self.row = self.conn.answer(sql, params)

    def fetchone(self) -> tuple[Any, ...] | None:
        return self.row

    def close(self) -> None:
        pass


class FakeConn:
    """Answers lock statements from ``lock_results`` (then True) and the
    unlock statement with ``unlock_result``; ``fail_on`` raises."""

    def __init__(self, pid: int = 1, **kw: Any) -> None:
        self.pid = pid
        self.log: list[tuple[Any, ...]] = []
        self.lock_results: list[bool] = kw.get("lock_results", [])
        self.unlock_result: bool = kw.get("unlock_result", True)
        self.fail_on: str | None = kw.get("fail_on")
        self.closed = False

    def answer(self, sql: str, params: Any) -> tuple[Any, ...] | None:
        if self.fail_on and self.fail_on in sql:
            raise RuntimeError("boom")
        if "pg_try_advisory_lock" in sql:
            return (self.lock_results.pop(0) if self.lock_results else True,)
        if "pg_advisory_unlock" in sql:
            return (self.unlock_result,)
        if "pg_backend_pid" in sql:
            return (self.pid,)
        if sql.lstrip().startswith("SELECT version"):
            return None
        return None

    def cursor(self) -> FakeCursor:
        return FakeCursor(self)

    def commit(self) -> None:
        self.log.append(("commit",))

    def rollback(self) -> None:
        self.log.append(("rollback",))

    def close(self) -> None:
        self.closed = True
        self.log.append(("close",))


class PoolishConn(FakeConn):
    """Like an SQLAlchemy pooled connection: close() hands it back, only
    invalidate() ends the session."""

    def __init__(self, **kw: Any) -> None:
        super().__init__(**kw)
        self.invalidated = False

    def invalidate(self) -> None:
        self.invalidated = True
        self.log.append(("invalidate",))


def make(conns: list[FakeConn]) -> tuple[PostgresDriver, list[FakeConn]]:
    """A driver whose connect hands out *conns* in order, after the table
    creation connection."""
    handed: list[FakeConn] = []
    queue = [FakeConn(pid=99), *conns]

    def connect() -> FakeConn:
        c = queue.pop(0) if len(queue) > 1 else queue[0]
        handed.append(c)
        return c

    return PostgresDriver(connect, "tbl", name="app", probe_timeout=0.3), handed


def sql_log(c: FakeConn) -> list[str]:
    return [e[1] for e in c.log if e[0] == "execute"]


# -- table name -------------------------------------------------------------


@pytest.mark.parametrize("name", ["t", "_t", "schema_versions", "T1", "a" * 64])
def test_valid_table_names(name: str) -> None:
    pg.validate_table(name)


@pytest.mark.parametrize(
    "name",
    ["", "1t", "a-b", "a b", "a.b", "a" * 65, "t\n", "tbl;drop", 'q"x', "é", "t\x00"],
)
def test_invalid_table_names_fail_before_any_io(name: str) -> None:
    calls: list[int] = []

    def connect() -> Any:
        calls.append(1)
        raise AssertionError("connect must not be called")

    with pytest.raises(ValueError, match=r'^invalid table name: ".*"$'):
        PostgresDriver(connect, name)
    assert calls == []


def test_invalid_table_error_text_matches_go() -> None:
    with pytest.raises(ValueError) as e:
        PostgresDriver(lambda: None, "bad-name")
    assert str(e.value) == 'invalid table name: "bad-name"'


# -- SQL as sent --------------------------------------------------------------


def test_table_creation_is_one_transaction_with_xact_lock_first() -> None:
    c = FakeConn()
    PostgresDriver(lambda: c, "tbl")
    assert c.log == [
        (
            "execute",
            "SELECT pg_advisory_xact_lock(hashtextextended(%s, 0))",
            ("kit-upgrade:tbl:ddl",),
        ),
        (
            "execute",
            "CREATE TABLE IF NOT EXISTS tbl ( schema_name TEXT PRIMARY KEY, "
            "version TEXT NOT NULL DEFAULT '' )",
            None,
        ),
        ("commit",),
        ("close",),
    ]


def test_version_and_set_version_sql() -> None:
    c = FakeConn()
    d = PostgresDriver(lambda: c, "tbl", name="app")
    c.log.clear()
    assert d.version() == ""  # no row
    d.set_version("1.2.3")
    assert c.log == [
        ("execute", "SELECT version FROM tbl WHERE schema_name = %s", ("app",)),
        ("commit",),
        ("close",),
        (
            "execute",
            "INSERT INTO tbl (schema_name, version) VALUES (%s, %s) "
            "ON CONFLICT (schema_name) DO UPDATE SET version = EXCLUDED.version",
            ("app", "1.2.3"),
        ),
        ("commit",),
        ("close",),
    ]


def test_default_schema_name_is_postgres() -> None:
    assert PostgresDriver(lambda: FakeConn(), "tbl").name == "postgres"


def test_lock_and_unlock_sql_and_key() -> None:
    lock_conn = FakeConn(pid=1)
    d, _ = make([lock_conn, FakeConn(pid=2)])
    unlock = d.lock(threading.Event())
    unlock()
    assert sql_log(lock_conn)[-3:] == [
        "SELECT pg_backend_pid()",
        "SELECT pg_try_advisory_lock(hashtextextended(%s, 0))",
        "SELECT pg_advisory_unlock(hashtextextended(%s, 0))",
    ]
    keys = [e[2] for e in lock_conn.log if e[0] == "execute" and e[2]]
    assert keys == [("kit-upgrade:tbl:app",), ("kit-upgrade:tbl:app",)]
    assert lock_conn.closed


def test_driver_capabilities() -> None:
    d = PostgresDriver(lambda: FakeConn(), "tbl")
    assert isinstance(d, SchemaDriver)
    assert isinstance(d, Locker)
    assert isinstance(d, VersionStorer)
    assert d.stores_version() is True
    with pytest.raises(BackupUnsupported):
        d.backup(Path("x"))
    with pytest.raises(BackupUnsupported):
        d.restore(Path("x"))


# -- agreement with the Go driver ------------------------------------------------


def go_const(src: str, name: str) -> str:
    m = re.search(rf"{name}\s*=\s*`([^`]*)`", src)
    assert m, name
    return m.group(1)


def test_shared_text_matches_the_go_driver() -> None:
    lock_go, driver_go = GO_LOCK.read_text(), GO_DRIVER.read_text()
    assert go_const(lock_go, "sqlUnlock") == pg.SQL_UNLOCK.replace("%s", "$1")
    assert go_const(lock_go, "sqlXactLock") == pg.SQL_XACT_LOCK.replace("%s", "$1")
    # Go blocks in pg_advisory_lock; Python polls pg_try_advisory_lock on the
    # same key, so only the function name differs.
    assert go_const(lock_go, "sqlLock").replace("pg_advisory_lock", "pg_try_advisory_lock") == (
        pg.SQL_LOCK.replace("%s", "$1")
    )
    assert 'return "kit-upgrade:" + table + ":" + name' in lock_go
    assert 'return "kit-upgrade:" + table + ":ddl"' in lock_go
    assert pg.lock_key("t", "n") == "kit-upgrade:t:n"
    assert pg.ddl_key("t") == "kit-upgrade:t:ddl"
    assert "^[a-zA-Z_][a-zA-Z0-9_]{0,63}$" in driver_go
    squash = lambda s: " ".join(s.split())  # noqa: E731
    go = squash(re.sub(r"(FROM|INTO|EXISTS) %s", r"\1 t", driver_go))
    assert squash(pg.sql_create("t")) in go
    assert squash(pg.sql_select("t").replace("%s", "$1")) in go
    upsert = pg.sql_upsert("t").replace("%s", "$1", 1).replace("%s", "$2", 1)
    assert squash(upsert) in go


# -- import check ----------------------------------------------------------------


def test_package_imports_without_a_postgres_client() -> None:
    code = (
        "import sys\n"
        "class Block:\n"
        "    def find_spec(self, name, path=None, target=None):\n"
        "        if name.split('.')[0] in ('psycopg2', 'psycopg', 'psycopg_binary', 'sqlalchemy'):\n"
        "            raise ImportError('blocked ' + name)\n"
        "sys.meta_path.insert(0, Block())\n"
        "import hop_top_kit.upgrade.drivers.postgres as m\n"
        "import hop_top_kit.upgrade.drivers\n"
        "assert m.PostgresDriver\n"
        "assert not [n for n in sys.modules if n.split('.')[0] in "
        "('psycopg2', 'psycopg', 'sqlalchemy')]\n"
    )
    r = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=60)
    assert r.returncode == 0, r.stderr


# -- unlock / discard decisions ------------------------------------------------------


def test_unlock_failure_invalidates_a_pooled_connection() -> None:
    lock_conn = PoolishConn(pid=1, fail_on="pg_advisory_unlock")
    d, _ = make([lock_conn, FakeConn(pid=2)])
    unlock = d.lock(threading.Event())
    with pytest.raises(UpgradeError, match="unlock: boom"):
        unlock()
    assert lock_conn.invalidated
    assert not lock_conn.closed, "close() would return the session to the pool"


def test_unlock_failure_closes_a_plain_connection() -> None:
    lock_conn = FakeConn(pid=1, fail_on="pg_advisory_unlock")
    d, _ = make([lock_conn, FakeConn(pid=2)])
    unlock = d.lock(threading.Event())
    with pytest.raises(UpgradeError, match="unlock: boom"):
        unlock()
    assert lock_conn.closed


def test_unlock_reporting_not_held_is_a_failure_and_discards() -> None:
    lock_conn = PoolishConn(pid=1, unlock_result=False)
    d, _ = make([lock_conn, FakeConn(pid=2)])
    unlock = d.lock(threading.Event())
    with pytest.raises(UpgradeError, match="lock was not held by this session"):
        unlock()
    assert lock_conn.invalidated


def test_successful_unlock_runs_the_statement_before_close_and_is_once() -> None:
    lock_conn = PoolishConn(pid=1)
    d, _ = make([lock_conn, FakeConn(pid=2)])
    unlock = d.lock(threading.Event())
    unlock()
    unlock()
    stmts = [e for e in lock_conn.log if e[0] == "execute" and "pg_advisory_unlock" in e[1]]
    assert len(stmts) == 1
    assert lock_conn.log.index(stmts[0]) < lock_conn.log.index(("close",))
    assert not lock_conn.invalidated


def test_failed_lock_statement_discards_its_connection() -> None:
    lock_conn = PoolishConn(pid=1, fail_on="pg_try_advisory_lock")
    d, _ = make([lock_conn, FakeConn(pid=2)])
    with pytest.raises(UpgradeError, match="lock: boom"):
        d.lock(threading.Event())
    assert lock_conn.invalidated


def test_lock_connections_are_never_left_in_a_transaction() -> None:
    lock_conn = FakeConn(pid=1)
    d, _ = make([lock_conn, FakeConn(pid=2)])
    d.lock(threading.Event())
    assert lock_conn.log[-1] == ("commit",), "the held connection sits idle, not in a transaction"


# -- cancellation ----------------------------------------------------------------------


def test_cancel_already_set_raises_before_connecting() -> None:
    d, handed = make([FakeConn(pid=1)])
    n = len(handed)
    ev = threading.Event()
    ev.set()
    with pytest.raises(Canceled):
        d.lock(ev)
    assert len(handed) == n


def test_cancel_ends_a_wait_and_holds_nothing() -> None:
    lock_conn = PoolishConn(pid=1, lock_results=[False] * 1000)
    d, _ = make([lock_conn, FakeConn(pid=2)])
    ev = threading.Event()
    threading.Timer(0.25, ev.set).start()
    began = time.monotonic()
    with pytest.raises(Canceled):
        d.lock(ev)
    assert 0.2 <= time.monotonic() - began < 1.5
    assert not [e for e in lock_conn.log if e[0] == "execute" and "unlock" in e[1]]
    assert lock_conn.log[-2:] == [("rollback",), ("close",)]
    assert not lock_conn.invalidated, "nothing was granted: a normal release is enough"


# -- two connections ---------------------------------------------------------------------


def test_connect_returning_the_same_connection_is_refused() -> None:
    only = FakeConn(pid=1)
    d = PostgresDriver(lambda: only, "tbl", probe_timeout=0.3)
    with pytest.raises(UpgradeError, match="at least two connections"):
        d.lock(threading.Event())
    assert not [e for e in only.log if e[0] == "execute" and "try_advisory" in e[1]]


def test_two_handles_on_one_session_are_refused() -> None:
    d, _ = make([FakeConn(pid=7), FakeConn(pid=7)])
    with pytest.raises(UpgradeError, match="at least two connections"):
        d.lock(threading.Event())


def test_a_connect_that_blocks_forever_is_refused_not_deadlocked() -> None:
    first = FakeConn(pid=1)
    gate = threading.Event()
    calls = []

    def connect() -> FakeConn:
        calls.append(1)
        if len(calls) == 1:
            return FakeConn(pid=99)  # table creation
        if len(calls) == 2:
            return first
        gate.wait(5)  # an exhausted pool: the second checkout never returns
        return FakeConn(pid=2)

    d = PostgresDriver(connect, "tbl", probe_timeout=0.2)
    began = time.monotonic()
    with pytest.raises(UpgradeError, match="at least two connections"):
        d.lock(threading.Event())
    assert time.monotonic() - began < 2
    assert first.closed and not [
        e for e in first.log if e[0] == "execute" and "try_advisory" in e[1]
    ]
    gate.set()


def test_second_connection_failure_is_a_refusal() -> None:
    calls = []

    def connect() -> FakeConn:
        calls.append(1)
        if len(calls) >= 3:
            raise RuntimeError("pool exhausted")
        return FakeConn(pid=len(calls))

    d = PostgresDriver(connect, "tbl", probe_timeout=0.3)
    with pytest.raises(UpgradeError, match=r"at least two connections.*pool exhausted"):
        d.lock(threading.Event())
