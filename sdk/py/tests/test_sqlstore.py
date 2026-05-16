"""
Tests for hop_top_kit.sqlstore — SQLite KV store with TTL.

All tests use ":memory:" to avoid disk I/O.
"""

import sqlite3
from datetime import UTC, datetime

import pytest

from hop_top_kit import Store as Store_init
from hop_top_kit import StoreOptions
from hop_top_kit import open_store as open_store_init
from hop_top_kit.sqlstore import Options, Store
from hop_top_kit.sqlstore import open as open_store

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def make_store(**kw) -> Store:
    opts = Options(**kw) if kw else None
    return open_store(":memory:", opts)


# ---------------------------------------------------------------------------
# Basic put / get roundtrip
# ---------------------------------------------------------------------------


def test_put_get_roundtrip():
    s = make_store()
    s.put("key1", {"hello": "world"})
    found, val = s.get("key1", dict)
    assert found is True
    assert val == {"hello": "world"}
    s.close()


def test_put_get_string():
    s = make_store()
    s.put("greeting", "hello")
    found, val = s.get("greeting", str)
    assert found is True
    assert val == "hello"
    s.close()


def test_put_get_int():
    s = make_store()
    s.put("count", 42)
    found, val = s.get("count", int)
    assert found is True
    assert val == 42
    s.close()


def test_put_get_list():
    s = make_store()
    s.put("items", [1, 2, 3])
    found, val = s.get("items", list)
    assert found is True
    assert val == [1, 2, 3]
    s.close()


# ---------------------------------------------------------------------------
# Overwrite
# ---------------------------------------------------------------------------


def test_overwrite():
    s = make_store()
    s.put("k", "first")
    s.put("k", "second")
    found, val = s.get("k", str)
    assert found is True
    assert val == "second"
    s.close()


# ---------------------------------------------------------------------------
# Missing key
# ---------------------------------------------------------------------------


def test_missing_key_returns_false_none():
    s = make_store()
    found, val = s.get("nonexistent", str)
    assert found is False
    assert val is None
    s.close()


# ---------------------------------------------------------------------------
# TTL — not yet expired
# ---------------------------------------------------------------------------


def test_ttl_not_yet_expired(monkeypatch):
    """Value stored 1 s ago with 60 s TTL → still valid."""
    from datetime import timedelta

    import hop_top_kit.sqlstore as sqlstore_mod

    s = open_store(":memory:", Options(ttl=timedelta(seconds=60)))
    s.put("ttl-key", "alive")

    # Advance "now" by only 1 second — entry should still be valid
    _real_now = datetime.now(tz=UTC)
    fake_now = _real_now.replace(second=(_real_now.second + 1) % 60)

    original_now = sqlstore_mod._now
    monkeypatch.setattr(sqlstore_mod, "_now", lambda: fake_now)

    found, val = s.get("ttl-key", str)
    assert found is True
    assert val == "alive"

    monkeypatch.setattr(sqlstore_mod, "_now", original_now)
    s.close()


# ---------------------------------------------------------------------------
# TTL — expired
# ---------------------------------------------------------------------------


def test_ttl_expired(monkeypatch):
    """Value stored now with 10 s TTL → expired when clock is +20 s."""
    from datetime import timedelta

    import hop_top_kit.sqlstore as sqlstore_mod

    s = open_store(":memory:", Options(ttl=timedelta(seconds=10)))
    s.put("ttl-key", "gone")

    # Advance fake clock 20 seconds into the future
    _real_now = datetime.now(tz=UTC)
    fake_future = _real_now.replace(
        second=(_real_now.second + 20) % 60,
        minute=_real_now.minute + (_real_now.second + 20) // 60,
    )

    monkeypatch.setattr(sqlstore_mod, "_now", lambda: fake_future)

    found, val = s.get("ttl-key", str)
    assert found is False
    assert val is None

    s.close()


def test_ttl_expired_simple(monkeypatch):
    """Simpler TTL expiry: mock _now to fixed future timestamp."""
    from datetime import timedelta

    import hop_top_kit.sqlstore as sqlstore_mod

    s = open_store(":memory:", Options(ttl=timedelta(milliseconds=100)))
    s.put("soon-gone", "value")

    # Mock _now to return a time 1 hour in the future
    future = datetime(2099, 1, 1, tzinfo=UTC)
    monkeypatch.setattr(sqlstore_mod, "_now", lambda: future)

    found, val = s.get("soon-gone", str)
    assert found is False
    assert val is None
    s.close()


# ---------------------------------------------------------------------------
# migrate_sql
# ---------------------------------------------------------------------------


def test_migrate_sql_creates_table():
    """migrate_sql runs after kv table creation."""
    extra_ddl = "CREATE TABLE IF NOT EXISTS extra (id INTEGER PRIMARY KEY);"
    s = open_store(":memory:", Options(migrate_sql=extra_ddl))

    conn = s.db()
    cur = conn.execute("SELECT name FROM sqlite_master WHERE type='table' AND name='extra'")
    row = cur.fetchone()
    assert row is not None, "extra table should exist after migrate_sql"
    s.close()


def test_migrate_sql_empty_is_fine():
    s = open_store(":memory:", Options(migrate_sql=""))
    conn = s.db()
    cur = conn.execute("SELECT name FROM sqlite_master WHERE type='table' AND name='kv'")
    assert cur.fetchone() is not None
    s.close()


# ---------------------------------------------------------------------------
# close makes ops raise
# ---------------------------------------------------------------------------


def test_close_then_put_raises():
    s = make_store()
    s.close()
    with pytest.raises(sqlite3.ProgrammingError):
        s.put("k", "v")


def test_close_then_get_raises():
    s = make_store()
    s.close()
    with pytest.raises(sqlite3.ProgrammingError):
        s.get("k", str)


# ---------------------------------------------------------------------------
# db() returns sqlite3.Connection
# ---------------------------------------------------------------------------


def test_db_returns_connection():
    s = make_store()
    conn = s.db()
    assert isinstance(conn, sqlite3.Connection)
    s.close()


# ---------------------------------------------------------------------------
# __init__ re-exports
# ---------------------------------------------------------------------------


def test_init_exports():
    """open_store, Store, StoreOptions are re-exported from hop_top_kit."""
    s = open_store_init(":memory:")
    assert isinstance(s, Store_init)
    assert isinstance(Options(), StoreOptions)
    s.close()


# ---------------------------------------------------------------------------
# No TTL → never expires
# ---------------------------------------------------------------------------


def test_no_ttl_never_expires(monkeypatch):
    """With no TTL, values survive even with a far-future clock."""
    import hop_top_kit.sqlstore as sqlstore_mod

    s = make_store()
    s.put("forever", "value")

    future = datetime(2099, 1, 1, tzinfo=UTC)
    monkeypatch.setattr(sqlstore_mod, "_now", lambda: future)

    found, val = s.get("forever", str)
    assert found is True
    assert val == "value"
    s.close()
