"""Shared helpers for the SQLite driver tests."""

from __future__ import annotations

import sqlite3
from pathlib import Path
from typing import Any


def open_wal(path: Path, *, autocheckpoint: bool = False) -> sqlite3.Connection:
    """An autocommit WAL connection; by default it never checkpoints, so
    committed rows stay in the ``-wal`` file while it is open."""
    conn = sqlite3.connect(path, isolation_level=None)
    conn.execute("PRAGMA journal_mode=WAL")
    if not autocheckpoint:
        conn.execute("PRAGMA wal_autocheckpoint=0")
    return conn


def rows(path: Path, sql: str = "SELECT x FROM t ORDER BY x") -> list[Any]:
    conn = sqlite3.connect(path)
    try:
        return [r[0] for r in conn.execute(sql).fetchall()]
    finally:
        conn.close()


class Proxy:
    """A stand-in for what ``engine.raw_connection()`` returns: a wrapper that
    forwards attributes to the DBAPI connection and records ``close``.
    SQLAlchemy itself is not a kit dependency."""

    def __init__(self, conn: sqlite3.Connection) -> None:
        self._conn = conn
        self.closed = 0

    def cursor(self) -> sqlite3.Cursor:
        return self._conn.cursor()

    def close(self) -> None:
        self.closed += 1
        self._conn.close()

    def __getattr__(self, name: str) -> Any:
        return getattr(self._conn, name)
