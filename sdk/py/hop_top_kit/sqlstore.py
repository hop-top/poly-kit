"""
hop_top_kit.sqlstore — SQLite-backed key-value store with optional TTL.

Schema
------
A single ``kv`` table is created on :func:`open`:

.. code-block:: sql

    CREATE TABLE IF NOT EXISTS kv (
        key        TEXT PRIMARY KEY,
        value      TEXT NOT NULL,
        expires_at INTEGER          -- UNIX epoch milliseconds; NULL = no expiry
    )

Values are JSON-serialised with :mod:`json` on :meth:`Store.put` and
deserialised on :meth:`Store.get`.

TTL semantics
-------------
Pass ``Options(ttl=timedelta(seconds=30))`` to :func:`open`.  On every
``put`` the ``expires_at`` column is set to::

    int(time.time() * 1000) + ttl_ms

On ``get``, if ``expires_at <= now_ms`` the entry is treated as missing and
``(False, None)`` is returned.  **Expired rows are not deleted automatically**;
callers that care about storage growth should periodically run:

.. code-block:: sql

    DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at <= <now_ms>

Migration
---------
Pass ``Options(migrate_sql="CREATE TABLE …")`` to add extra tables or indexes
in the same open step.  The SQL is executed after the ``kv`` table is created.

Usage example
-------------
.. code-block:: python

    from datetime import timedelta
    from hop_top_kit.sqlstore import open, Options

    store = open(":memory:", Options(ttl=timedelta(minutes=5)))
    store.put("session:abc", {"user_id": 42, "role": "admin"})

    found, data = store.get("session:abc", dict)
    if found:
        print(data["role"])   # admin

    store.close()

Thread safety
-------------
The underlying :class:`sqlite3.Connection` is created with
``check_same_thread=False``; callers are responsible for external locking if
multiple threads share the same :class:`Store` instance.
"""

from __future__ import annotations

import json
import sqlite3
import time
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Any, TypeVar

__all__ = ["Options", "Store", "open"]

T = TypeVar("T")


# ---------------------------------------------------------------------------
# Internal helpers (monkeypatch-friendly)
# ---------------------------------------------------------------------------


def _now() -> datetime:
    """Return current UTC time.  Replaced in tests via monkeypatch."""
    return datetime.now(tz=UTC)


def _now_ms() -> int:
    """Return current UNIX time in milliseconds."""
    return int(time.time() * 1000)


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


@dataclass
class Options:
    """Configuration for :func:`open`.

    Attributes:
        ttl: Maximum age of a stored value.  ``None`` (default) disables expiry.
        migrate_sql: SQL executed once after the ``kv`` table is created.
            Use it to add application-specific tables, indexes, or seed data.
            Multiple statements must be separated by semicolons.
    """

    ttl: timedelta | None = None
    migrate_sql: str = ""


_CREATE_KV = """
CREATE TABLE IF NOT EXISTS kv (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    expires_at INTEGER
)
"""

_UPSERT = """
INSERT INTO kv (key, value, expires_at)
VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE
    SET value      = excluded.value,
        expires_at = excluded.expires_at
"""


class Store:
    """Key-value store backed by a SQLite database.

    Do not instantiate directly; use :func:`open` instead.
    """

    def __init__(self, conn: sqlite3.Connection, opts: Options) -> None:
        self._conn = conn
        self._opts = opts
        self._migrate()

    # ------------------------------------------------------------------
    # Public methods
    # ------------------------------------------------------------------

    def put(self, key: str, value: Any) -> None:
        """Serialise *value* as JSON and upsert it under *key*.

        If *key* already exists, both the value and expiry are overwritten.

        Args:
            key: Lookup key (plain string; no namespacing applied).
            value: Any JSON-serialisable object.

        Raises:
            sqlite3.ProgrammingError: If :meth:`close` has already been called.
            TypeError: If *value* is not JSON-serialisable.
        """
        blob = json.dumps(value)
        expires_at = self._expires_at_ms()
        self._conn.execute(_UPSERT, (key, blob, expires_at))
        self._conn.commit()

    def get(self, key: str, type_: type[T]) -> tuple[bool, T | None]:
        """Retrieve and deserialise the value stored under *key*.

        Args:
            key: The key to look up.
            type_: The expected Python type.  Used only as documentation; the
                actual deserialisation is driven by the stored JSON structure.

        Returns:
            ``(True, value)`` on success.
            ``(False, None)`` if the key does not exist or the entry has expired.

        Raises:
            sqlite3.ProgrammingError: If :meth:`close` has already been called.
        """
        cur = self._conn.execute("SELECT value, expires_at FROM kv WHERE key = ?", (key,))
        row = cur.fetchone()
        if row is None:
            return False, None

        raw_value, expires_at = row

        if expires_at is not None:
            now_ms = int(_now().timestamp() * 1000)
            if expires_at <= now_ms:
                return False, None

        return True, json.loads(raw_value)

    def db(self) -> sqlite3.Connection:
        """Return the underlying :class:`sqlite3.Connection`.

        The connection must **not** be closed directly; call :meth:`close`
        instead.
        """
        return self._conn

    def close(self) -> None:
        """Close the underlying database connection.

        Subsequent calls to :meth:`put` or :meth:`get` will raise
        :class:`sqlite3.ProgrammingError`.
        """
        self._conn.close()

    # ------------------------------------------------------------------
    # Private helpers
    # ------------------------------------------------------------------

    def _migrate(self) -> None:
        self._conn.execute(_CREATE_KV)
        if self._opts.migrate_sql:
            self._conn.executescript(self._opts.migrate_sql)
        self._conn.commit()

    def _expires_at_ms(self) -> int | None:
        if self._opts.ttl is None:
            return None
        ttl_ms = int(self._opts.ttl.total_seconds() * 1000)
        return int(time.time() * 1000) + ttl_ms


# ---------------------------------------------------------------------------
# Factory
# ---------------------------------------------------------------------------


def open(path: str, opts: Options | None = None) -> Store:
    """Open (or create) a SQLite KV store at *path*.

    Pass ``":memory:"`` for an in-process ephemeral store (useful in tests).

    Args:
        path: Filesystem path to the SQLite file, or ``":memory:"``.
        opts: Optional :class:`Options`; defaults to ``Options()`` (no TTL,
            no extra migration).

    Returns:
        A ready-to-use :class:`Store` instance.

    Example::

        store = open("/var/cache/myapp/kv.db", Options(ttl=timedelta(hours=1)))
    """
    if opts is None:
        opts = Options()
    conn = sqlite3.connect(path, check_same_thread=False)
    return Store(conn, opts)
