//! Shared SQLite connection management with sensible defaults
//! (WAL mode, busy timeout, foreign keys).
//!
//! Low-level primitive underpinning higher-level stores. Mirrors the Go
//! `storage/sqldb` package: [`open`] creates or opens a database with a
//! standard pragma set applied, and [`migrate`] runs numbered migrations
//! idempotently against a caller-named bookkeeping table.
//!
//! # Pragma delivery
//!
//! The Go implementation threads pragmas through the DSN as `_pragma=`
//! query parameters because `database/sql` maintains a *pool*: a pragma
//! applied with a single `Exec` lands on one arbitrary pooled connection,
//! leaving the rest without `busy_timeout`, foreign keys or WAL.
//!
//! [`rusqlite::Connection`] is a single connection, not a pool, so the
//! same guarantee is obtained by executing the pragmas directly after
//! opening. Callers that build their own pool must call [`apply_pragmas`]
//! on every connection they hand out.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::sqldb::{migrate, open, Options};
//! use std::collections::BTreeMap;
//!
//! let db = open(Options::new(":memory:")).unwrap();
//!
//! let mut migrations = BTreeMap::new();
//! migrations.insert(1, "CREATE TABLE items (id TEXT PRIMARY KEY)".to_string());
//! migrate(&db, "schema_versions", &migrations).unwrap();
//! ```

use std::collections::BTreeMap;
use std::fs;
use std::path::Path;

use rusqlite::Connection;
use thiserror::Error;

/// In-memory database path, matching the SQLite sentinel.
pub const MEMORY: &str = ":memory:";

/// Default busy timeout in milliseconds when unset or non-positive.
pub const DEFAULT_BUSY_TIMEOUT_MS: i64 = 5000;

/// Errors returned by [`open`] and [`migrate`].
#[derive(Debug, Error)]
pub enum SqlDbError {
    /// [`Options::path`] was empty.
    #[error("sqldb: path required")]
    PathRequired,

    /// The parent directory of the database file could not be created.
    #[error("sqldb: mkdir: {0}")]
    Mkdir(#[source] std::io::Error),

    /// The connection could not be opened.
    #[error("sqldb: open: {0}")]
    Open(#[source] rusqlite::Error),

    /// A pragma failed to apply, or the post-open smoke check failed.
    #[error("sqldb: pragma {pragma}: {source}")]
    Pragma {
        /// The pragma statement that failed.
        pragma: String,
        /// Underlying driver error.
        #[source]
        source: rusqlite::Error,
    },

    /// The migration bookkeeping table name is not a safe SQL identifier.
    #[error("sqldb: invalid table name: {0:?}")]
    InvalidTable(String),

    /// The migration bookkeeping table could not be created.
    #[error("sqldb: create migrations table: {0}")]
    CreateMigrationsTable(#[source] rusqlite::Error),

    /// Reading the applied-version bookkeeping row failed.
    #[error("sqldb: check version {version}: {source}")]
    CheckVersion {
        /// Migration version being checked.
        version: i64,
        /// Underlying driver error.
        #[source]
        source: rusqlite::Error,
    },

    /// A migration statement failed. The transaction was rolled back.
    #[error("sqldb: migrate v{version}: {source}")]
    Migrate {
        /// Migration version that failed.
        version: i64,
        /// Underlying driver error.
        #[source]
        source: rusqlite::Error,
    },

    /// Recording an applied version failed. The transaction was rolled back.
    #[error("sqldb: record v{version}: {source}")]
    Record {
        /// Migration version that failed to record.
        version: i64,
        /// Underlying driver error.
        #[source]
        source: rusqlite::Error,
    },

    /// Committing a migration transaction failed.
    #[error("sqldb: commit v{version}: {source}")]
    Commit {
        /// Migration version that failed to commit.
        version: i64,
        /// Underlying driver error.
        #[source]
        source: rusqlite::Error,
    },
}

/// Configures the SQLite connection.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Options {
    /// Path to the database file. Use [`MEMORY`] for in-memory.
    pub path: String,

    /// Enables WAL journal mode. Defaults to `true` when `None`.
    pub wal: Option<bool>,

    /// Busy timeout in milliseconds. Values `<= 0` fall back to
    /// [`DEFAULT_BUSY_TIMEOUT_MS`].
    pub busy_timeout: i64,
}

impl Options {
    /// Builds options for `path` with default WAL and busy timeout.
    pub fn new(path: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            wal: None,
            busy_timeout: 0,
        }
    }

    /// Sets WAL journal mode explicitly.
    #[must_use]
    pub fn with_wal(mut self, wal: bool) -> Self {
        self.wal = Some(wal);
        self
    }

    /// Sets the busy timeout in milliseconds.
    #[must_use]
    pub fn with_busy_timeout(mut self, ms: i64) -> Self {
        self.busy_timeout = ms;
        self
    }

    /// Resolved WAL setting — `true` unless explicitly disabled.
    pub fn wal_enabled(&self) -> bool {
        self.wal.unwrap_or(true)
    }

    /// Resolved busy timeout, applying the default for non-positive values.
    pub fn busy_timeout_ms(&self) -> i64 {
        if self.busy_timeout <= 0 {
            DEFAULT_BUSY_TIMEOUT_MS
        } else {
            self.busy_timeout
        }
    }
}

impl Default for Options {
    fn default() -> Self {
        Self::new(String::new())
    }
}

/// Returns `true` when `name` is a safe unquoted SQL identifier:
/// leading letter or underscore, then up to 63 further word characters.
fn valid_table(name: &str) -> bool {
    let mut chars = name.chars();
    let Some(first) = chars.next() else {
        return false;
    };
    if !(first.is_ascii_alphabetic() || first == '_') {
        return false;
    }
    if name.len() > 64 {
        return false;
    }
    chars.all(|c| c.is_ascii_alphanumeric() || c == '_')
}

/// Splits any `?query` suffix off a database path.
///
/// Callers occasionally pass DSN-shaped paths (`":memory:?cache=shared"`,
/// `"file:foo?mode=ro"`). The base is what the filesystem sees; the query
/// is preserved and handed to SQLite's URI parser.
fn split_path(path: &str) -> (&str, Option<&str>) {
    match path.find('?') {
        Some(i) => (&path[..i], Some(&path[i + 1..])),
        None => (path, None),
    }
}

/// Open flags for DSN-shaped paths carrying a `?query` suffix.
fn uri_flags() -> rusqlite::OpenFlags {
    rusqlite::OpenFlags::SQLITE_OPEN_READ_WRITE
        | rusqlite::OpenFlags::SQLITE_OPEN_CREATE
        | rusqlite::OpenFlags::SQLITE_OPEN_URI
        | rusqlite::OpenFlags::SQLITE_OPEN_NO_MUTEX
}

/// Applies the standard pragma set to an already-open connection.
///
/// Exposed so callers running their own connection pool can guarantee
/// identical pragma state on every connection they hand out — the
/// property the Go implementation obtains via DSN `_pragma=` parameters.
pub fn apply_pragmas(conn: &Connection, opts: &Options) -> Result<(), SqlDbError> {
    let busy = format!("busy_timeout={}", opts.busy_timeout_ms());
    // busy_timeout first: later pragmas may contend for the write lock.
    conn.pragma_update(None, "busy_timeout", opts.busy_timeout_ms())
        .map_err(|source| SqlDbError::Pragma {
            pragma: busy,
            source,
        })?;

    conn.pragma_update(None, "foreign_keys", "on")
        .map_err(|source| SqlDbError::Pragma {
            pragma: "foreign_keys=on".to_string(),
            source,
        })?;

    if opts.wal_enabled() {
        // journal_mode returns the resulting mode as a row, so it must be
        // queried rather than issued as a plain update.
        conn.pragma_update_and_check(None, "journal_mode", "WAL", |_| Ok(()))
            .map_err(|source| SqlDbError::Pragma {
                pragma: "journal_mode=WAL".to_string(),
                source,
            })?;
    }

    Ok(())
}

/// Creates or opens a SQLite database with standard pragmas applied.
///
/// Missing parent directories of a file-backed database are created.
/// The connection is smoke-checked before returning so a misconfigured
/// path surfaces here, not at first query.
///
/// # Errors
///
/// Returns [`SqlDbError`] when the path is empty, the parent directory
/// cannot be created, the connection cannot be opened, or a pragma fails.
pub fn open(opts: Options) -> Result<Connection, SqlDbError> {
    if opts.path.is_empty() {
        return Err(SqlDbError::PathRequired);
    }

    let (base, query) = split_path(&opts.path);

    if base != MEMORY {
        if let Some(parent) = Path::new(base).parent() {
            if !parent.as_os_str().is_empty() {
                fs::create_dir_all(parent).map_err(SqlDbError::Mkdir)?;
            }
        }
    }

    // `:memory:` is a bare sentinel, not a URI — SQLite only honours a
    // `?query` suffix when the path is a `file:` URI and the URI flag is
    // set. Handing `":memory:?cache=shared"` to the plain opener would
    // create a literal file of that name, so rewrite it to the URI form.
    let conn = if base == MEMORY {
        match query {
            Some(q) => Connection::open_with_flags(format!("file::memory:?{q}"), uri_flags()),
            None => Connection::open_in_memory(),
        }
    } else if query.is_some() || base.starts_with("file:") {
        Connection::open_with_flags(&opts.path, uri_flags())
    } else {
        Connection::open(base)
    }
    .map_err(SqlDbError::Open)?;

    apply_pragmas(&conn, &opts)?;

    // Smoke-check the connection so a misconfigured path surfaces here.
    conn.query_row("SELECT 1", [], |_| Ok(()))
        .map_err(|source| SqlDbError::Pragma {
            pragma: "SELECT 1".to_string(),
            source,
        })?;

    Ok(conn)
}

/// Like [`open`] but panics on error.
///
/// # Panics
///
/// Panics when [`open`] returns an error.
pub fn must_open(opts: Options) -> Connection {
    match open(opts) {
        Ok(conn) => conn,
        Err(err) => panic!("{err}"),
    }
}

/// Applies numbered migrations to the database.
///
/// Applied versions are tracked in a table named by `table`. Migrations
/// run in ascending version order; already-applied versions are skipped,
/// making repeat calls idempotent. Each migration and its bookkeeping row
/// commit in a single transaction.
///
/// # Errors
///
/// Returns [`SqlDbError`] when `table` is not a safe identifier, the
/// bookkeeping table cannot be created, or any migration fails. A failed
/// migration is rolled back, leaving prior versions applied.
pub fn migrate(
    conn: &Connection,
    table: &str,
    migrations: &BTreeMap<i64, String>,
) -> Result<(), SqlDbError> {
    if !valid_table(table) {
        return Err(SqlDbError::InvalidTable(table.to_string()));
    }

    conn.execute_batch(&format!(
        "CREATE TABLE IF NOT EXISTS {table} (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"
    ))
    .map_err(SqlDbError::CreateMigrationsTable)?;

    for (&version, statement) in migrations {
        let exists: i64 = conn
            .query_row(
                &format!("SELECT COUNT(*) FROM {table} WHERE version = ?1"),
                [version],
                |row| row.get(0),
            )
            .map_err(|source| SqlDbError::CheckVersion { version, source })?;
        if exists > 0 {
            continue;
        }

        conn.execute_batch("BEGIN")
            .map_err(|source| SqlDbError::Migrate { version, source })?;

        if let Err(source) = conn.execute_batch(statement) {
            let _ = conn.execute_batch("ROLLBACK");
            return Err(SqlDbError::Migrate { version, source });
        }

        if let Err(source) = conn.execute(
            &format!("INSERT INTO {table} (version, applied_at) VALUES (?1, datetime('now'))"),
            [version],
        ) {
            let _ = conn.execute_batch("ROLLBACK");
            return Err(SqlDbError::Record { version, source });
        }

        conn.execute_batch("COMMIT")
            .map_err(|source| SqlDbError::Commit { version, source })?;
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn valid_table_accepts_identifiers() {
        assert!(valid_table("schema_versions"));
        assert!(valid_table("_private"));
        assert!(valid_table("T9"));
    }

    #[test]
    fn valid_table_rejects_injection() {
        assert!(!valid_table(""));
        assert!(!valid_table("9start"));
        assert!(!valid_table("bad name"));
        assert!(!valid_table("drop; --"));
        assert!(!valid_table(&"a".repeat(65)));
    }

    #[test]
    fn split_path_separates_query() {
        assert_eq!(split_path(":memory:"), (":memory:", None));
        assert_eq!(
            split_path(":memory:?cache=shared"),
            (":memory:", Some("cache=shared"))
        );
    }

    #[test]
    fn options_defaults() {
        let opts = Options::new("x.db");
        assert!(opts.wal_enabled());
        assert_eq!(opts.busy_timeout_ms(), DEFAULT_BUSY_TIMEOUT_MS);

        let opts = Options::new("x.db").with_wal(false).with_busy_timeout(3000);
        assert!(!opts.wal_enabled());
        assert_eq!(opts.busy_timeout_ms(), 3000);
    }
}
