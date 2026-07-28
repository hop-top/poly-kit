//! Minimal key-value storage abstraction with a SQLite backend.
//!
//! Mirrors the Go `storage/kv` package: [`Store`] is the base contract,
//! [`TtlStore`] adds expiry, and [`SqliteStore`] implements both on top of
//! the [`crate::sqldb`] primitive.
//!
//! # Keys are bytes
//!
//! Go models keys as `string`, which is an arbitrary byte sequence — the
//! suite exercises prefixes such as `"data\xff"` that are not valid UTF-8.
//! Rust `String` cannot hold those, so keys are `[u8]` here and stored as
//! `BLOB`. SQLite compares blobs with `memcmp`, which is exactly the
//! ordering Go's string comparison uses, so range scans behave identically.
//! [`SqliteStore::put_str`] and friends are provided for the common
//! UTF-8 case.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::kv::{Config, Store, TtlStore};
//! use std::time::Duration;
//!
//! let dir = tempfile::tempdir().unwrap();
//! let path = dir.path().join("kv.db");
//! let store = Config::sqlite(path.to_str().unwrap()).open().unwrap();
//!
//! store.put(b"app/a", b"1").unwrap();
//! store.put_with_ttl(b"tmp", b"x", Duration::from_secs(60)).unwrap();
//!
//! assert_eq!(store.get(b"app/a").unwrap(), Some(b"1".to_vec()));
//! assert_eq!(store.list(b"app/").unwrap(), vec![b"app/a".to_vec()]);
//! ```

use std::cell::Cell;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use rusqlite::Connection;
use thiserror::Error;

use crate::sqldb::{self, Options, SqlDbError};

/// Sweep expired rows once every this many successful writes.
///
/// Matches the Go backend: amortised cleanup on the write path, so no
/// background thread or timer is needed.
const SWEEP_INTERVAL: i64 = 100;

/// Errors returned by the kv layer.
#[derive(Debug, Error)]
pub enum KvError {
    /// The configured backend name is not recognised.
    #[error("kv: unknown backend {0:?}")]
    UnknownBackend(String),

    /// A backend that requires a filesystem path was configured without one.
    #[error("kv: {0} backend requires path")]
    PathRequired(&'static str),

    /// The backend is known but not compiled into this build.
    #[error("kv: {0} backend not available")]
    BackendUnavailable(&'static str),

    /// Opening the underlying database failed.
    #[error("sqlite kv: open: {0}")]
    Open(#[source] SqlDbError),

    /// Creating the kv table or its index failed.
    #[error("sqlite kv: migrate: {0}")]
    Migrate(#[source] SqlDbError),

    /// A query or statement failed.
    #[error("sqlite kv: {0}")]
    Query(#[source] rusqlite::Error),
}

/// Minimal key-value storage contract.
pub trait Store {
    /// Stores `value` under `key`, replacing any existing entry and
    /// clearing any expiry previously set on it.
    ///
    /// # Errors
    ///
    /// Returns [`KvError`] when the write fails.
    fn put(&self, key: &[u8], value: &[u8]) -> Result<(), KvError>;

    /// Returns the value for `key`, or `None` when absent or expired.
    ///
    /// # Errors
    ///
    /// Returns [`KvError`] when the read fails.
    fn get(&self, key: &[u8]) -> Result<Option<Vec<u8>>, KvError>;

    /// Removes `key`. Deleting a missing key is not an error.
    ///
    /// # Errors
    ///
    /// Returns [`KvError`] when the delete fails.
    fn delete(&self, key: &[u8]) -> Result<(), KvError>;

    /// Returns every live key starting with `prefix`. An empty prefix
    /// lists all keys. Expired keys are excluded.
    ///
    /// # Errors
    ///
    /// Returns [`KvError`] when the scan fails.
    fn list(&self, prefix: &[u8]) -> Result<Vec<Vec<u8>>, KvError>;
}

/// Extends [`Store`] with time-to-live support.
pub trait TtlStore: Store {
    /// Stores `value` under `key`, expiring it after `ttl`.
    ///
    /// # Errors
    ///
    /// Returns [`KvError`] when the write fails.
    fn put_with_ttl(&self, key: &[u8], value: &[u8], ttl: Duration) -> Result<(), KvError>;
}

/// Backend selection for [`Config`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Backend {
    /// SQLite file (or `:memory:`) backend.
    Sqlite,
    /// Recognised by the Go implementation, not compiled here.
    Badger,
    /// Recognised by the Go implementation, not compiled here.
    Etcd,
    /// Recognised by the Go implementation, not compiled here.
    Tidb,
}

impl Backend {
    /// Parses a backend name as the Go `Config.Backend` field spells it.
    ///
    /// # Errors
    ///
    /// Returns [`KvError::UnknownBackend`] for unrecognised names.
    pub fn parse(name: &str) -> Result<Self, KvError> {
        match name {
            "sqlite" => Ok(Self::Sqlite),
            "badger" => Ok(Self::Badger),
            "etcd" => Ok(Self::Etcd),
            "tidb" => Ok(Self::Tidb),
            other => Err(KvError::UnknownBackend(other.to_string())),
        }
    }
}

/// Describes which backend to open and how.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Config {
    /// Which backend to open.
    pub backend: Backend,
    /// Filesystem path, for path-backed backends.
    pub path: String,
}

impl Config {
    /// Builds a SQLite config for `path`.
    pub fn sqlite(path: impl Into<String>) -> Self {
        Self {
            backend: Backend::Sqlite,
            path: path.into(),
        }
    }

    /// Opens the configured backend.
    ///
    /// Only [`Backend::Sqlite`] is compiled in; the remaining variants are
    /// recognised so misconfiguration reports "not available" rather than
    /// "unknown backend", matching the Go behaviour for backends gated
    /// behind build tags.
    ///
    /// # Errors
    ///
    /// Returns [`KvError::PathRequired`] when a path-backed backend has an
    /// empty path, [`KvError::BackendUnavailable`] for backends not
    /// compiled in, or an open error from the backend itself.
    pub fn open(&self) -> Result<SqliteStore, KvError> {
        match self.backend {
            Backend::Sqlite => {
                if self.path.is_empty() {
                    return Err(KvError::PathRequired("sqlite"));
                }
                SqliteStore::new(&self.path)
            }
            Backend::Badger => {
                if self.path.is_empty() {
                    return Err(KvError::PathRequired("badger"));
                }
                Err(KvError::BackendUnavailable("badger"))
            }
            Backend::Etcd => Err(KvError::BackendUnavailable("etcd")),
            Backend::Tidb => Err(KvError::BackendUnavailable("tidb")),
        }
    }
}

/// SQLite-backed [`Store`] and [`TtlStore`].
///
/// Wraps a single [`rusqlite::Connection`] opened through
/// [`crate::sqldb::open`], so it inherits WAL mode, the busy timeout and
/// foreign-key enforcement.
#[derive(Debug)]
pub struct SqliteStore {
    conn: Connection,
    writes: Cell<i64>,
}

impl SqliteStore {
    /// Opens (or creates) a database at `path` and ensures the kv schema.
    ///
    /// # Errors
    ///
    /// Returns [`KvError::Open`] when the database cannot be opened, or
    /// [`KvError::Migrate`] when the schema cannot be created.
    pub fn new(path: &str) -> Result<Self, KvError> {
        let conn = sqldb::open(Options::new(path)).map_err(KvError::Open)?;

        // BLOB key column: Go keys are arbitrary bytes, and SQLite orders
        // blobs by memcmp — the same ordering the Go range scan assumes.
        conn.execute_batch(
            "CREATE TABLE IF NOT EXISTS kv (
                key        BLOB PRIMARY KEY,
                value      BLOB NOT NULL,
                expires_at INTEGER
            );
            CREATE INDEX IF NOT EXISTS idx_kv_expires
                ON kv(expires_at) WHERE expires_at IS NOT NULL;",
        )
        .map_err(|source| KvError::Migrate(SqlDbError::CreateMigrationsTable(source)))?;

        Ok(Self {
            conn,
            writes: Cell::new(0),
        })
    }

    /// [`Store::put`] for UTF-8 keys and values.
    ///
    /// # Errors
    ///
    /// Returns [`KvError`] when the write fails.
    pub fn put_str(&self, key: &str, value: &str) -> Result<(), KvError> {
        self.put(key.as_bytes(), value.as_bytes())
    }

    /// [`Store::get`] for a UTF-8 key, returning the raw value bytes.
    ///
    /// # Errors
    ///
    /// Returns [`KvError`] when the read fails.
    pub fn get_str(&self, key: &str) -> Result<Option<Vec<u8>>, KvError> {
        self.get(key.as_bytes())
    }

    /// Closes the connection, surfacing any close error.
    ///
    /// Dropping the store closes it too; this exists for callers that need
    /// to observe failures, mirroring Go's `Close() error`.
    ///
    /// # Errors
    ///
    /// Returns [`KvError::Query`] when the connection cannot be closed.
    pub fn close(self) -> Result<(), KvError> {
        self.conn.close().map_err(|(_, err)| KvError::Query(err))
    }

    /// Sweeps expired rows once every [`SWEEP_INTERVAL`] writes.
    ///
    /// Sweep failures are deliberately ignored: expiry is already enforced
    /// on the read path, so a failed sweep costs space, never correctness.
    fn maybe_sweep(&self) {
        let n = self.writes.get() + 1;
        self.writes.set(n);
        if n % SWEEP_INTERVAL != 0 {
            return;
        }
        let _ = self.conn.execute(
            "DELETE FROM kv WHERE expires_at IS NOT NULL AND expires_at <= ?1",
            [now_millis()],
        );
    }
}

impl Store for SqliteStore {
    fn put(&self, key: &[u8], value: &[u8]) -> Result<(), KvError> {
        self.conn
            .execute(
                "INSERT OR REPLACE INTO kv (key, value, expires_at) VALUES (?1, ?2, NULL)",
                (key, value),
            )
            .map_err(KvError::Query)?;
        self.maybe_sweep();
        Ok(())
    }

    fn get(&self, key: &[u8]) -> Result<Option<Vec<u8>>, KvError> {
        let found = self.conn.query_row(
            "SELECT value FROM kv
             WHERE key = ?1 AND (expires_at IS NULL OR expires_at > ?2)",
            (key, now_millis()),
            |row| row.get::<_, Vec<u8>>(0),
        );
        match found {
            Ok(value) => Ok(Some(value)),
            Err(rusqlite::Error::QueryReturnedNoRows) => Ok(None),
            Err(err) => Err(KvError::Query(err)),
        }
    }

    fn delete(&self, key: &[u8]) -> Result<(), KvError> {
        self.conn
            .execute("DELETE FROM kv WHERE key = ?1", [key])
            .map_err(KvError::Query)?;
        Ok(())
    }

    fn list(&self, prefix: &[u8]) -> Result<Vec<Vec<u8>>, KvError> {
        let now = now_millis();
        let live = "(expires_at IS NULL OR expires_at > ?)";

        // Three shapes, matching Go: unbounded (empty prefix), lower-bound
        // only (all-0xff prefix, which has no successor), and half-open
        // range for everything else.
        let (sql, params): (String, Vec<rusqlite::types::Value>) = if prefix.is_empty() {
            (format!("SELECT key FROM kv WHERE {live}"), vec![now.into()])
        } else if let Some(end) = prefix_end(prefix) {
            (
                format!("SELECT key FROM kv WHERE key >= ? AND key < ? AND {live}"),
                vec![prefix.to_vec().into(), end.into(), now.into()],
            )
        } else {
            (
                format!("SELECT key FROM kv WHERE key >= ? AND {live}"),
                vec![prefix.to_vec().into(), now.into()],
            )
        };

        let mut stmt = self.conn.prepare(&sql).map_err(KvError::Query)?;
        let rows = stmt
            .query_map(rusqlite::params_from_iter(params), |row| {
                row.get::<_, Vec<u8>>(0)
            })
            .map_err(KvError::Query)?;

        rows.collect::<Result<Vec<_>, _>>().map_err(KvError::Query)
    }
}

impl TtlStore for SqliteStore {
    fn put_with_ttl(&self, key: &[u8], value: &[u8], ttl: Duration) -> Result<(), KvError> {
        let expires = now_millis().saturating_add(millis_i64(ttl));
        self.conn
            .execute(
                "INSERT OR REPLACE INTO kv (key, value, expires_at) VALUES (?1, ?2, ?3)",
                (key, value, expires),
            )
            .map_err(KvError::Query)?;
        self.maybe_sweep();
        Ok(())
    }
}

/// Milliseconds since the Unix epoch.
fn now_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(millis_i64)
        .unwrap_or(0)
}

/// Saturating `Duration` → milliseconds, so an absurd TTL clamps rather
/// than wrapping into the past.
fn millis_i64(d: Duration) -> i64 {
    i64::try_from(d.as_millis()).unwrap_or(i64::MAX)
}

/// Returns the exclusive upper bound of the range covered by `prefix`.
///
/// Increments the last byte below `0xff`, dropping every trailing `0xff`.
/// Returns `None` when the prefix is empty or entirely `0xff`, meaning
/// there is no successor and the scan must be lower-bounded only —
/// returning a bound there would silently drop matching keys.
fn prefix_end(prefix: &[u8]) -> Option<Vec<u8>> {
    let mut end = prefix.to_vec();
    while let Some(last) = end.last_mut() {
        if *last < 0xff {
            *last += 1;
            return Some(end);
        }
        end.pop();
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn prefix_end_cases() {
        // Mirrors the Go table test; `None` stands in for Go's "" sentinel.
        assert_eq!(prefix_end(b""), None);
        assert_eq!(prefix_end(b"abc"), Some(b"abd".to_vec()));
        assert_eq!(prefix_end(b"ab\xff"), Some(b"ac".to_vec()));
        assert_eq!(prefix_end(b"a\xff\xff"), Some(b"b".to_vec()));
        assert_eq!(prefix_end(b"\xff"), None);
        assert_eq!(prefix_end(b"\xff\xff\xff"), None);
        assert_eq!(prefix_end(b"a"), Some(b"b".to_vec()));
        assert_eq!(prefix_end(b"\x00"), Some(b"\x01".to_vec()));
    }

    #[test]
    fn backend_parse() {
        assert_eq!(Backend::parse("sqlite").unwrap(), Backend::Sqlite);
        assert_eq!(Backend::parse("badger").unwrap(), Backend::Badger);
        assert_eq!(Backend::parse("etcd").unwrap(), Backend::Etcd);
        assert_eq!(Backend::parse("tidb").unwrap(), Backend::Tidb);
        assert!(matches!(
            Backend::parse("redis"),
            Err(KvError::UnknownBackend(_))
        ));
    }

    #[test]
    fn millis_saturates() {
        assert_eq!(millis_i64(Duration::from_millis(50)), 50);
        assert_eq!(millis_i64(Duration::MAX), i64::MAX);
    }
}
