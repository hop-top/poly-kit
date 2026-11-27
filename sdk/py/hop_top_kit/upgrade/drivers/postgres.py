"""A schema driver for PostgreSQL over PEP 249 connections the caller opens.

Kit imports no Postgres client library: the caller passes a ``connect``
callable returning a DB-API connection (psycopg2, psycopg 3, an SQLAlchemy
``engine.raw_connection``, a pool's checkout). SQL uses the ``format``
paramstyle (``%s``) both psycopg generations share.

The table, its upsert, the advisory lock key and the unlock rules are the
Go driver's (``go/core/upgrade/driver/postgres``) text for text, so a Go and
a Python Migrator on one table and schema name exclude each other.
"""

from __future__ import annotations

import contextlib
import json
import re
import threading
from collections.abc import Callable
from pathlib import Path
from typing import Any

from hop_top_kit.upgrade.driver import Cancel
from hop_top_kit.upgrade.errors import BackupUnsupported, Canceled, UpgradeError

#: Returns a new PEP 249 connection on every call.
Connect = Callable[[], Any]

_VALID_TABLE = re.compile(r"[a-zA-Z_][a-zA-Z0-9_]{0,63}")

# Shared verbatim by every port: the key text is hashed by the server, so
# every process derives the same 64-bit key. The lock waits with
# pg_try_advisory_lock (same lock space as pg_advisory_lock, so it excludes a
# Go driver blocked in pg_advisory_lock) so a cancel event can end the wait.
SQL_LOCK = "SELECT pg_try_advisory_lock(hashtextextended(%s, 0))"
SQL_UNLOCK = "SELECT pg_advisory_unlock(hashtextextended(%s, 0))"
SQL_XACT_LOCK = "SELECT pg_advisory_xact_lock(hashtextextended(%s, 0))"
SQL_BACKEND_PID = "SELECT pg_backend_pid()"


def sql_create(table: str) -> str:
    return (
        f"CREATE TABLE IF NOT EXISTS {table} (\n"
        "\t\tschema_name TEXT PRIMARY KEY,\n"
        "\t\tversion TEXT NOT NULL DEFAULT ''\n"
        "\t)"
    )


def sql_select(table: str) -> str:
    return f"SELECT version FROM {table} WHERE schema_name = %s"


def sql_upsert(table: str) -> str:
    return (
        f"INSERT INTO {table} (schema_name, version) VALUES (%s, %s)\n"
        "\t\tON CONFLICT (schema_name) DO UPDATE SET version = EXCLUDED.version"
    )


def lock_key(table: str, name: str) -> str:
    """The text hashed into the advisory lock key for a run."""
    return f"kit-upgrade:{table}:{name}"


def ddl_key(table: str) -> str:
    """The text hashed into the key that serializes table creation."""
    return f"kit-upgrade:{table}:ddl"


def validate_table(name: str) -> None:
    if not isinstance(name, str) or _VALID_TABLE.fullmatch(name) is None:
        raise ValueError(f"invalid table name: {json.dumps(name, ensure_ascii=False)}")


def _discard(conn: Any) -> None:
    """End the physical connection behind *conn* (the server drops the
    session and every advisory lock it holds). ``invalidate()`` when the
    object has it (an SQLAlchemy pooled connection, whose ``close()`` would
    return the session to the pool), else ``close()``."""
    invalidate = getattr(conn, "invalidate", None)
    if callable(invalidate):
        try:
            invalidate()
            return
        except Exception:
            pass
    with contextlib.suppress(Exception):
        conn.close()


def _release(conn: Any) -> None:
    """Give *conn* back after a clean statement: never in a transaction, so
    a pool does not hand it out mid-transaction. A rollback that fails means
    the connection is not trustworthy: discard it."""
    try:
        conn.rollback()
    except Exception:
        _discard(conn)
        return
    with contextlib.suppress(Exception):
        conn.close()


def _scalar(conn: Any, sql: str, params: tuple[Any, ...] = ()) -> Any:
    cur = conn.cursor()
    try:
        cur.execute(sql, params)
        row = cur.fetchone()
    finally:
        cur.close()
    return None if row is None else row[0]


def _exec(conn: Any, sql: str, params: tuple[Any, ...] | None = None) -> None:
    cur = conn.cursor()
    try:
        if params is None:
            cur.execute(sql)
        else:
            cur.execute(sql, params)
    finally:
        cur.close()


class PostgresDriver:
    """A :class:`~hop_top_kit.upgrade.SchemaDriver` that keeps the schema
    version in a PostgreSQL table, plus :class:`~hop_top_kit.upgrade.Locker`
    and :class:`~hop_top_kit.upgrade.VersionStorer`.

    Args:
        connect: Returns a new PEP 249 connection per call. The driver closes
            only connections it got from it; it never touches any other.
        table: Version table; must match ``^[a-zA-Z_][a-zA-Z0-9_]{0,63}$``.
        name: Schema name: the ``schema_name`` row key and part of the lock
            key. Default ``"postgres"``.
        lock_poll: Seconds between lock attempts while another session holds
            the lock; the wait ends within it once the cancel event is set.
        probe_timeout: Seconds ``lock`` waits for the second connection it
            needs before refusing (see below).

    ``connect`` must be able to supply at least two connections at once: the
    lock holds one for the whole run and the version reads and writes use
    another. ``lock`` opens a second one first and refuses when ``connect``
    hands back the lock's own connection or does not return one within
    *probe_timeout* (a pool capped at one). It cannot see a pool that is
    large enough for that but exhausted by the migrations themselves. No
    transaction-mode pooler (PgBouncer ``transaction``) may sit in front: a
    session lock does not survive it.

    Raises:
        ValueError: *table* is invalid (before any I/O).
    """

    def __init__(
        self,
        connect: Connect,
        table: str,
        *,
        name: str = "postgres",
        lock_poll: float = 0.1,
        probe_timeout: float = 10.0,
    ) -> None:
        validate_table(table)
        if not callable(connect):
            raise TypeError("postgres upgrade: connect must be callable")
        self._connect = connect
        self._table = table
        self._name = name
        self._poll = lock_poll
        self._probe_timeout = probe_timeout
        try:
            self._ensure_table()
        except Exception as e:
            raise UpgradeError(f"postgres upgrade: migrate: {e}") from e

    @property
    def name(self) -> str:
        return self._name

    def stores_version(self) -> bool:
        """The row is the only version: the Migrator writes no version file."""
        return True

    # -- version table ---------------------------------------------------------

    def _in_tx(self, fn: Callable[[Any], Any]) -> Any:
        """Run *fn* on a fresh connection in one transaction, commit, and
        hand the connection back outside any transaction."""
        conn = self._connect()
        try:
            result = fn(conn)
            conn.commit()
        except BaseException:
            _release(conn)
            raise
        with contextlib.suppress(Exception):
            conn.close()
        return result

    def _ensure_table(self) -> None:
        """Create the table under a transaction-scoped advisory lock:
        concurrent CREATE TABLE IF NOT EXISTS can still fail with a catalog
        unique violation when two sessions create the same table at once."""

        def tx(conn: Any) -> None:
            _exec(conn, SQL_XACT_LOCK, (ddl_key(self._table),))
            _exec(conn, sql_create(self._table))

        self._in_tx(tx)

    def version(self) -> str:
        """The stored version; no row means ``""``."""
        try:
            v = self._in_tx(lambda c: _scalar(c, sql_select(self._table), (self._name,)))
        except Exception as e:
            raise UpgradeError(f"postgres upgrade: version: {e}") from e
        return "" if v is None else v

    def set_version(self, version: str) -> None:
        """Upsert the version row for this schema."""
        try:
            self._in_tx(lambda c: _exec(c, sql_upsert(self._table), (self._name, version)))
        except Exception as e:
            raise UpgradeError(f"postgres upgrade: set version: {e}") from e

    def backup(self, dest: Path) -> None:
        """Always :class:`BackupUnsupported`: kit does not back up a server
        database. Take one outside kit when a failed migration must be
        recoverable."""
        raise BackupUnsupported

    def restore(self, src: Path) -> None:
        """Always :class:`BackupUnsupported`: nothing is restored, so a
        rollback that needs it ends as ``NotRestored``."""
        raise BackupUnsupported

    # -- lock -------------------------------------------------------------------

    def lock(self, cancel: Cancel) -> Callable[[], None]:
        """Take the session advisory lock on one dedicated connection and
        hold it until the returned unlock runs. Waits until acquired or
        *cancel* is set (then raises :class:`Canceled`, holding nothing).

        The unlock runs ``pg_advisory_unlock`` on that same connection before
        releasing it: ``close()`` on a pooled connection returns it to the
        pool with the session lock still held. If the unlock statement fails
        or reports the lock not held, the connection is discarded
        (``invalidate()`` when it has one, else ``close()``), which ends the
        session and the lock. A lock statement that fails discards its
        connection too.
        """
        if cancel.is_set():
            raise Canceled()
        try:
            conn = self._connect()
        except Exception as e:
            raise UpgradeError(f"postgres upgrade: lock: {e}") from e
        try:
            self._require_second_connection(conn)
        except BaseException:
            _release(conn)
            raise
        key = lock_key(self._table, self._name)
        try:
            while True:
                held = _scalar(conn, SQL_LOCK, (key,))
                conn.commit()
                if held is True:
                    break
                if cancel.wait(self._poll):
                    # Every attempt returned false: nothing is held.
                    _release(conn)
                    raise Canceled()
        except Canceled:
            raise
        except BaseException as e:
            _discard(conn)
            if isinstance(e, Exception):
                raise UpgradeError(f"postgres upgrade: lock: {e}") from e
            raise

        done = threading.Lock()
        state = {"done": False}

        def unlock() -> None:
            with done:
                if state["done"]:
                    return
                state["done"] = True
            self._unlock(conn, key)

        return unlock

    @staticmethod
    def _unlock(conn: Any, key: str) -> None:
        try:
            held = _scalar(conn, SQL_UNLOCK, (key,))
            conn.commit()
            if held is not True:
                raise UpgradeError("lock was not held by this session")
        except BaseException as e:
            _discard(conn)
            if isinstance(e, Exception):
                raise UpgradeError(f"postgres upgrade: unlock: {e}") from e
            raise
        # The statement released the session lock: the connection can go
        # back to a pool.
        try:
            conn.close()
        except Exception as e:
            raise UpgradeError(f"postgres upgrade: unlock: {e}") from e

    def _require_second_connection(self, first: Any) -> None:
        """Refuse when ``connect`` cannot supply a second connection beside
        *first*: it would deadlock the run (lock on one, version reads on
        the other). Holds nothing yet."""
        mu = threading.Lock()
        done = threading.Event()
        box: list[tuple[bool, Any]] = []
        abandoned: list[bool] = []

        def fetch() -> None:
            try:
                got: tuple[bool, Any] = (True, self._connect())
            except BaseException as e:
                got = (False, e)
            with mu:
                if abandoned:
                    if got[0]:
                        _release(got[1])  # late arrival, unused: give it back
                    return
                box.append(got)
            done.set()

        threading.Thread(target=fetch, daemon=True, name="kit-upgrade-probe").start()
        if not done.wait(self._probe_timeout):
            with mu:
                if not box:
                    abandoned.append(True)
        if abandoned:
            raise UpgradeError(self._refusal(f"no second connection within {self._probe_timeout}s"))
        ok, second = box[0]
        if not ok:
            raise UpgradeError(self._refusal(f"second connection failed: {second}"))
        same = second is first
        try:
            if not same:
                same = _scalar(second, SQL_BACKEND_PID) == _scalar(first, SQL_BACKEND_PID)
        finally:
            if second is not first:
                _release(second)
        if same:
            raise UpgradeError(self._refusal("connect returned the lock's own session"))
        first.rollback()

    @staticmethod
    def _refusal(why: str) -> str:
        return (
            "postgres upgrade: lock needs a connect callable that supplies at least two "
            "connections: the lock holds one for the whole run and the version reads and "
            f"writes need another ({why}; a pool capped at one would deadlock)"
        )
