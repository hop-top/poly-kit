"""PostgresDriver against a real Postgres 16 in Docker, over psycopg2 and
psycopg 3. Run with ``make test-py-upgrade-integration``; the default test
target deselects the ``integration`` marker. Docker missing is a failure
here, never a skip. The client libraries are the ``upgrade-pg-test`` extra
and are imported lazily so merely collecting this module needs neither."""

from __future__ import annotations

import os
import re
import secrets
import shutil
import subprocess
import sys
import threading
import time
from collections.abc import Callable, Iterator
from pathlib import Path
from typing import Any

import pytest

from hop_top_kit.upgrade import (
    Canceled,
    MigrateError,
    Migration,
    Migrator,
    Registry,
    UpgradeError,
    is_backup_unsupported,
    is_canceled,
    is_not_restored,
)
from hop_top_kit.upgrade.drivers import PostgresDriver

pytestmark = pytest.mark.integration

FLAVORS = ["psycopg2", "psycopg3"]
WORKER = Path(__file__).with_name("upgrade_pg_worker.py")
PASSWORD = "kit-test-" + secrets.token_hex(4)


# -- container ---------------------------------------------------------------------


def docker(*args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["docker", *args], capture_output=True, text=True, timeout=120)


@pytest.fixture(scope="session")
def pg_port() -> Iterator[int]:
    if shutil.which("docker") is None:
        pytest.fail("docker is required for the integration target and was not found")
    name = f"kit-upg-py-{os.getpid()}-{secrets.token_hex(3)}"
    r = docker(
        "run", "--rm", "-d", "--name", name, "-p", "127.0.0.1::5432",
        "-e", f"POSTGRES_PASSWORD={PASSWORD}", "postgres:16-alpine",
    )  # fmt: skip
    if r.returncode != 0:
        pytest.fail(f"docker run failed (docker is required): {r.stderr.strip()}")
    try:
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            logs = docker("logs", name)
            ready = (logs.stdout + logs.stderr).count("database system is ready to accept")
            if ready >= 2:  # the init server, then the real one
                break
            time.sleep(0.5)
        else:
            pytest.fail("postgres container did not become ready")
        port = docker("port", name, "5432/tcp").stdout.strip().rsplit(":", 1)[1]
        yield int(port)
    finally:
        docker("rm", "-f", name)


@pytest.fixture(scope="session")
def dsn(pg_port: int) -> str:
    return f"host=127.0.0.1 port={pg_port} user=postgres password={PASSWORD} dbname=postgres"


def raw_connect(flavor: str, dsn: str) -> Any:
    if flavor == "psycopg2":
        import psycopg2

        return psycopg2.connect(dsn)
    import psycopg

    return psycopg.connect(dsn)


@pytest.fixture(params=FLAVORS)
def flavor(request: pytest.FixtureRequest) -> str:
    return request.param


@pytest.fixture
def connect(flavor: str, dsn: str) -> Callable[[], Any]:
    return lambda: raw_connect(flavor, dsn)


@pytest.fixture
def table(request: pytest.FixtureRequest) -> str:
    base = re.sub(r"\W", "_", request.node.name.lower())[:30]
    return f"v_{base}_{secrets.token_hex(3)}"


class Probe:
    """A raw session for assertions and for holding locks by hand."""

    def __init__(self, flavor: str, dsn: str) -> None:
        self.conn = raw_connect(flavor, dsn)
        self.conn.autocommit = True

    def one(self, sql: str, *params: Any) -> Any:
        cur = self.conn.cursor()
        cur.execute(sql, params)
        row = cur.fetchone()
        cur.close()
        return row[0] if row else None

    def run(self, sql: str, *params: Any) -> None:
        cur = self.conn.cursor()
        cur.execute(sql, params)
        cur.close()

    def try_lock(self, text: str) -> bool:
        return self.one("SELECT pg_try_advisory_lock(hashtextextended(%s, 0))", text)

    def lock(self, text: str) -> None:
        self.run("SELECT pg_advisory_lock(hashtextextended(%s, 0))", text)

    def unlock(self, text: str) -> None:
        assert self.one("SELECT pg_advisory_unlock(hashtextextended(%s, 0))", text) is True

    def advisory_held(self) -> int:
        return self.one("SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted")

    def idle_in_transaction(self) -> int:
        return self.one(
            "SELECT count(*) FROM pg_stat_activity WHERE state LIKE 'idle in transaction%%' "
            "AND pid <> pg_backend_pid()"
        )


@pytest.fixture
def probe(flavor: str, dsn: str) -> Iterator[Probe]:
    p = Probe(flavor, dsn)
    yield p
    p.conn.close()


def key(table: str, name: str = "postgres") -> str:
    """The text the Go driver hashes; spelled out here, not imported."""
    return f"kit-upgrade:{table}:{name}"


def acquire_within(d: PostgresDriver, seconds: float) -> Callable[[], None]:
    """Lock *d* or fail: a session must get a free lock at once."""
    ev = threading.Event()
    timer = threading.Timer(seconds, ev.set)
    timer.start()
    try:
        return d.lock(ev)
    finally:
        timer.cancel()


def registry(*steps: tuple[str, Callable[[], None] | None], schema: str = "postgres") -> Registry:
    reg = Registry()
    for version, up in steps:
        reg.register(Migration(version=version, schema=schema, up=up or (lambda: None)))
    return reg


def boom() -> None:
    raise RuntimeError("boom")


def migrator(reg: Registry, target: str, tmp_path: Path, **kw: Any) -> Migrator:
    return Migrator("pgtest", target, registry=reg, data_dir=tmp_path, **kw)


# -- version table ---------------------------------------------------------------------


def test_version_roundtrip_and_upsert(connect, table, probe) -> None:
    d = PostgresDriver(connect, table, name="app")
    assert d.version() == ""
    d.set_version("1.0.0")
    assert d.version() == "1.0.0"
    d.set_version("1.1.0")
    assert d.version() == "1.1.0"
    other = PostgresDriver(connect, table, name="other")
    assert other.version() == ""
    other.set_version("9.9.9")
    assert d.version() == "1.1.0"
    assert probe.one(f"SELECT count(*) FROM {table}") == 2
    assert probe.one(f"SELECT version FROM {table} WHERE schema_name = 'app'") == "1.1.0"
    assert probe.idle_in_transaction() == 0


def test_concurrent_construction_creates_the_table_once(connect, table) -> None:
    errs: list[BaseException] = []

    def make() -> None:
        try:
            PostgresDriver(connect, table)
        except BaseException as e:
            errs.append(e)

    ts = [threading.Thread(target=make) for _ in range(8)]
    [t.start() for t in ts]
    [t.join() for t in ts]
    assert errs == []


def test_table_creation_waits_on_the_shared_ddl_key(connect, table, probe, flavor, dsn) -> None:
    holder = raw_connect(flavor, dsn)
    cur = holder.cursor()
    cur.execute(
        "SELECT pg_advisory_xact_lock(hashtextextended(%s, 0))", (f"kit-upgrade:{table}:ddl",)
    )
    done = threading.Event()
    t = threading.Thread(target=lambda: (PostgresDriver(connect, table), done.set()))
    t.start()
    assert not done.wait(0.6), "the Go driver's DDL key must serialise Python creation"
    holder.commit()
    holder.close()
    assert done.wait(10)
    t.join()


# -- lock: interop with the Go driver's key --------------------------------------------


def test_lock_excludes_a_raw_session_holding_the_go_key(connect, table, probe) -> None:
    d = PostgresDriver(connect, table, name="app")
    probe.lock(key(table, "app"))
    ev = threading.Event()
    threading.Timer(0.5, ev.set).start()
    began = time.monotonic()
    with pytest.raises(Canceled):
        d.lock(ev)
    assert time.monotonic() - began >= 0.4, "lock waited while the Go key was held"

    got: list[Callable[[], None]] = []
    t = threading.Thread(target=lambda: got.append(d.lock(threading.Event())))
    t.start()
    t.join(0.5)
    assert t.is_alive(), "still blocked"
    probe.unlock(key(table, "app"))
    t.join(10)
    assert len(got) == 1, "acquired as soon as the other session released"
    # and the reverse direction: while Python holds it, a Go-style session cannot
    assert probe.try_lock(key(table, "app")) is False
    got[0]()
    assert probe.try_lock(key(table, "app")) is True
    probe.unlock(key(table, "app"))


def test_lock_key_is_per_table_and_schema_name(connect, table, probe) -> None:
    d = PostgresDriver(connect, table, name="app")
    probe.lock(key(table, "other"))
    probe.lock(key(table + "x", "app"))
    unlock = acquire_within(d, 3)
    unlock()
    probe.unlock(key(table, "other"))
    probe.unlock(key(table + "x", "app"))


def test_cancelled_wait_holds_nothing(connect, table, probe) -> None:
    d = PostgresDriver(connect, table)
    probe.lock(key(table))
    before = probe.advisory_held()
    ev = threading.Event()
    threading.Timer(0.3, ev.set).start()
    with pytest.raises(Canceled):
        d.lock(ev)
    assert probe.advisory_held() == before, "the cancelled waiter holds no advisory lock"
    assert probe.idle_in_transaction() == 0
    probe.unlock(key(table))
    acquire_within(d, 3)()


def test_cancel_through_a_run_reports_canceled(connect, table, probe, tmp_path) -> None:
    d = PostgresDriver(connect, table)
    probe.lock(key(table))
    m = migrator(registry(("1.1.0", None)), "1.1.0", tmp_path)
    m.add_driver(d)
    ev = threading.Event()
    threading.Timer(0.3, ev.set).start()
    with pytest.raises(MigrateError) as e:
        m.run(ev)
    assert is_canceled(e.value)
    assert d.version() == ""
    probe.unlock(key(table))


def test_lock_held_while_a_run_is_in_progress(connect, table, probe, tmp_path) -> None:
    d = PostgresDriver(connect, table)
    seen: list[bool] = []

    def up() -> None:
        seen.append(probe.try_lock(key(table)))
        assert probe.idle_in_transaction() == 0

    m = migrator(registry(("1.1.0", up)), "1.1.0", tmp_path)
    m.add_driver(d)
    m.run()
    assert seen == [False]
    assert probe.try_lock(key(table)) is True, "released after the run"
    probe.unlock(key(table))


# -- two processes ------------------------------------------------------------------------


@pytest.mark.parametrize(
    "pair", [("psycopg2", "psycopg2"), ("psycopg3", "psycopg3"), ("psycopg2", "psycopg3")]
)
def test_two_processes_racing_apply_each_migration_once(pair, dsn, table, probe, flavor) -> None:
    def start(fl: str, label: str, hold: float) -> subprocess.Popen[str]:
        return subprocess.Popen(
            [sys.executable, str(WORKER), fl, dsn, table, label, str(hold)],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )  # fmt: skip

    a = start(pair[0], "A", 0.6)
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:  # A is inside its first migration: B must wait
        if probe.one("SELECT to_regclass(%s)", f"{table}_applied") is not None:
            break
        time.sleep(0.05)
    b = start(pair[1], "B", 0.6)
    out_a, out_b = a.communicate(timeout=60)[0], b.communicate(timeout=60)[0]
    assert (a.returncode, b.returncode) == (0, 0), (out_a, out_b)
    cur = probe.conn.cursor()
    cur.execute(f"SELECT version, label FROM {table}_applied ORDER BY version")
    rows = cur.fetchall()
    assert [r[0] for r in rows] == ["1.1.0", "1.2.0", "1.3.0"], rows
    assert {r[1] for r in rows} == {"A"}, "B waited, then found nothing pending"
    assert probe.one(f"SELECT version FROM {table}") == "1.3.0"
    assert probe.advisory_held() == 0


# -- release ------------------------------------------------------------------------------


def test_lock_released_after_a_failed_run(connect, table, tmp_path) -> None:
    d = PostgresDriver(connect, table)
    m = migrator(registry(("1.1.0", None), ("1.2.0", boom)), "1.2.0", tmp_path)
    m.add_driver(d)
    with pytest.raises(MigrateError):
        m.run()
    acquire_within(PostgresDriver(connect, table), 2)()


def test_missing_down_reports_not_restored(connect, table, tmp_path) -> None:
    d = PostgresDriver(connect, table, name="app")
    d.set_version("1.0.0")
    reg = registry(("1.1.0", None), ("1.2.0", boom), schema="app")
    m = migrator(reg, "1.2.0", tmp_path)
    m.add_driver(d)
    with pytest.raises(MigrateError) as e:
        m.run()
    assert is_not_restored(e.value)
    assert is_backup_unsupported(e.value)
    assert d.version() == "1.1.0", "the row keeps the last step applied"


def test_manual_rollback_latest_finds_no_backup_and_unlocks(
    connect, table, tmp_path, probe
) -> None:
    d = PostgresDriver(connect, table, name="app")
    d.set_version("1.0.0")
    reg = registry(("1.1.0", None), ("1.2.0", boom), schema="app")
    m = migrator(reg, "1.2.0", tmp_path, manual_rollback=True)
    m.add_driver(d)
    with pytest.raises(MigrateError):
        m.run()
    with pytest.raises(MigrateError) as e:
        m.rollback_latest()
    assert "no backups found" in str(e.value)
    assert is_not_restored(e.value) and is_backup_unsupported(e.value)
    assert d.version() == "1.1.0", "nothing was restored"
    assert probe.try_lock(key(table, "app")) is True, "rollback_latest released the lock"
    probe.unlock(key(table, "app"))


def test_additive_column_migration_with_baseline_on_a_preexisting_table(
    connect, table, probe, tmp_path
) -> None:
    items = f"{table}_items"
    probe.run(f"CREATE TABLE {items} (id INT PRIMARY KEY)")
    probe.run(f"INSERT INTO {items} VALUES (1), (2)")

    def add_column() -> None:
        conn = connect()
        try:
            cur = conn.cursor()
            cur.execute(f"ALTER TABLE {items} ADD COLUMN note TEXT NOT NULL DEFAULT ''")
            conn.commit()
        finally:
            conn.close()

    calls: list[str] = []

    def baseline(schema: str) -> str:
        calls.append(schema)
        return "1.0.0"

    d = PostgresDriver(connect, table, name="app")
    m = migrator(
        registry(("1.0.0", boom), ("1.1.0", add_column), schema="app"),
        "1.1.0",
        tmp_path,
        baseline=baseline,
    )
    m.add_driver(d)
    m.run()
    assert calls == ["app"], "baseline asked once: the table had no row for the schema"
    assert d.version() == "1.1.0"
    assert probe.one(f"SELECT count(*) FROM {items} WHERE note = ''") == 2
    m.run()
    assert calls == ["app"], "the stored version is the authority from now on"


# -- connections: who closes what ----------------------------------------------------------


class Tracked:
    """Forwards to a real connection; records open, close and the state a
    connection is closed in."""

    def __init__(self, inner: Any, ledger: Ledger) -> None:
        self._inner, self._ledger = inner, ledger

    def close(self) -> None:
        self._ledger.closed.append(self)
        self._ledger.states.append(self._ledger.state(self._inner))
        self._inner.close()

    def __getattr__(self, name: str) -> Any:
        return getattr(self._inner, name)


class Ledger:
    def __init__(self, flavor: str, dsn: str) -> None:
        self.flavor, self.dsn = flavor, dsn
        self.opened: list[Tracked] = []
        self.closed: list[Tracked] = []
        self.states: list[str] = []

    def state(self, conn: Any) -> str:
        if self.flavor == "psycopg2":
            return {0: "idle", 1: "active", 2: "in transaction", 3: "in error"}.get(
                conn.info.transaction_status, "other"
            )
        return conn.info.transaction_status.name.lower()

    def connect(self) -> Tracked:
        t = Tracked(raw_connect(self.flavor, self.dsn), self)
        self.opened.append(t)
        return t


def test_driver_closes_only_what_it_opened_and_returns_them_idle(
    flavor, dsn, table, tmp_path, probe
) -> None:
    mine = raw_connect(flavor, dsn)  # the caller's own connection, never given to the driver
    led = Ledger(flavor, dsn)
    d = PostgresDriver(led.connect, table)
    m = migrator(registry(("1.1.0", None), ("1.2.0", None)), "1.2.0", tmp_path)
    m.add_driver(d)
    m.run()
    assert led.opened and all(c in led.opened for c in led.closed)
    assert sorted(map(id, led.closed)) == sorted(map(id, led.opened)), "no leaks, no strangers"
    assert set(led.states) == {"idle"}, "no connection is closed inside a transaction"
    cur = mine.cursor()
    cur.execute("SELECT 1")
    assert cur.fetchone()[0] == 1, "the caller's connection is untouched"
    mine.close()


def test_failed_run_still_closes_everything_idle(flavor, dsn, table, tmp_path) -> None:
    led = Ledger(flavor, dsn)
    d = PostgresDriver(led.connect, table)
    m = migrator(registry(("1.1.0", boom)), "1.1.0", tmp_path)
    m.add_driver(d)
    with pytest.raises(MigrateError):
        m.run()
    assert sorted(map(id, led.closed)) == sorted(map(id, led.opened))
    assert set(led.states) == {"idle"}


# -- SQLAlchemy pooled connections ------------------------------------------------------------


@pytest.fixture
def engine(flavor: str, pg_port: int) -> Iterator[Any]:
    from sqlalchemy import create_engine

    scheme = "postgresql+psycopg2" if flavor == "psycopg2" else "postgresql+psycopg"
    e = create_engine(
        f"{scheme}://postgres:{PASSWORD}@127.0.0.1:{pg_port}/postgres",
        pool_size=2, max_overflow=0, pool_timeout=20,
    )  # fmt: skip
    yield e
    e.dispose()


def test_pooled_connection_release_unlocks_before_returning_it(engine, table, probe) -> None:
    """close() on a pooled connection returns it with the session lock still
    held; the explicit unlock is what frees it. The mutation target."""
    d = PostgresDriver(engine.raw_connection, table)
    unlock = d.lock(threading.Event())
    assert probe.try_lock(key(table)) is False
    unlock()
    assert engine.pool.checkedout() == 0
    assert probe.try_lock(key(table)) is True, "a second session acquires at once"
    probe.unlock(key(table))
    assert probe.advisory_held() == 0


def test_run_over_raw_connection_releases_after_success_and_failure(
    engine, table, probe, tmp_path
) -> None:
    d = PostgresDriver(engine.raw_connection, table)
    m = migrator(registry(("1.1.0", None)), "1.1.0", tmp_path)
    m.add_driver(d)
    m.run()
    assert probe.try_lock(key(table)) is True
    probe.unlock(key(table))

    m2 = migrator(registry(("1.2.0", boom)), "1.2.0", tmp_path)
    m2.add_driver(PostgresDriver(engine.raw_connection, table))
    with pytest.raises(MigrateError):
        m2.run()
    assert probe.try_lock(key(table)) is True
    probe.unlock(key(table))
    assert engine.pool.checkedout() == 0


def test_failed_unlock_invalidates_the_pooled_session(engine, table, probe) -> None:
    d = PostgresDriver(engine.raw_connection, table)
    unlock = d.lock(threading.Event())
    pid = probe.one(
        "SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND granted "
        "AND pid <> pg_backend_pid() LIMIT 1"
    )
    probe.run("SELECT pg_terminate_backend(%s)", pid)
    time.sleep(0.3)
    with pytest.raises(UpgradeError, match="unlock"):
        unlock()
    assert engine.pool.checkedout() == 0
    assert probe.advisory_held() == 0
    again = engine.raw_connection()  # the pool recovers with a fresh session
    cur = again.cursor()
    cur.execute("SELECT pg_backend_pid()")
    assert cur.fetchone()[0] != pid
    again.close()


def test_cancelled_wait_returns_a_clean_pooled_connection(engine, table, probe) -> None:
    d = PostgresDriver(engine.raw_connection, table)
    probe.lock(key(table))
    ev = threading.Event()
    threading.Timer(0.3, ev.set).start()
    with pytest.raises(Canceled):
        d.lock(ev)
    assert engine.pool.checkedout() == 0
    assert probe.idle_in_transaction() == 0
    probe.unlock(key(table))
    acquire_within(d, 3)()


# -- at least two connections ---------------------------------------------------------------------


def test_a_pool_of_one_is_refused_not_deadlocked(flavor, pg_port, table, probe) -> None:
    from sqlalchemy import create_engine

    scheme = "postgresql+psycopg2" if flavor == "psycopg2" else "postgresql+psycopg"
    e = create_engine(
        f"{scheme}://postgres:{PASSWORD}@127.0.0.1:{pg_port}/postgres",
        pool_size=1, max_overflow=0, pool_timeout=30,
    )  # fmt: skip
    try:
        d = PostgresDriver(e.raw_connection, table, probe_timeout=1.0)
        began = time.monotonic()
        with pytest.raises(UpgradeError, match="at least two connections"):
            d.lock(threading.Event())
        assert time.monotonic() - began < 5
        assert probe.advisory_held() == 0, "nothing was locked"
        time.sleep(0.5)  # the abandoned checkout gets the connection and hands it back
        assert e.pool.checkedout() == 0
    finally:
        e.dispose()


def test_a_connect_returning_one_shared_connection_is_refused(flavor, dsn, table, probe) -> None:
    shared = raw_connect(flavor, dsn)
    d = PostgresDriver(lambda: shared, table, probe_timeout=1.0)
    with pytest.raises(UpgradeError, match="at least two connections"):
        d.lock(threading.Event())
    assert probe.advisory_held() == 0
