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
use std::thread;
use std::time::Duration;

use rusqlite::Connection;
use thiserror::Error;

/// In-memory database path, matching the SQLite sentinel.
pub const MEMORY: &str = ":memory:";

/// Default busy timeout in milliseconds when unset or non-positive.
pub const DEFAULT_BUSY_TIMEOUT_MS: i64 = 5000;

/// Default number of `journal_mode=WAL` conversion attempts when unset
/// or non-positive. See [`Options::wal_retry_attempts`].
pub const DEFAULT_WAL_RETRY_ATTEMPTS: u32 = 10;

/// Upper bound on [`Options::wal_retry_attempts`]. Larger requests are
/// clamped.
pub const MAX_WAL_RETRY_ATTEMPTS: u32 = 32;

/// Default base delay in milliseconds between `journal_mode=WAL`
/// conversion attempts when unset or non-positive. Doubles per attempt.
pub const DEFAULT_WAL_RETRY_BASE_MS: i64 = 2;

/// Upper bound on [`Options::wal_retry_base_ms`]. Larger requests are
/// clamped. One minute per attempt is already far past any plausible
/// first-open contention window.
pub const MAX_WAL_RETRY_BASE_MS: i64 = 60_000;

/// Ceiling on a single backoff delay, applied *after* doubling.
///
/// Bounding the attempt count and the base delay independently is not
/// enough: the delay doubles, so the two bounds multiply into a
/// worst-case wait of astronomical length — finite, and free of
/// [`Duration`] overflow, but indistinguishable from a hang. Plateauing
/// each delay here turns the tail of the schedule linear, so the total
/// wait is bounded by `MAX_WAL_RETRY_ATTEMPTS * MAX_WAL_RETRY_DELAY_MS`
/// (about two minutes) no matter how the knobs are set.
pub const MAX_WAL_RETRY_DELAY_MS: i64 = 4000;

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

    /// Attempts at converting the journal to WAL on a concurrent first
    /// open. `0` falls back to [`DEFAULT_WAL_RETRY_ATTEMPTS`]; values
    /// above [`MAX_WAL_RETRY_ATTEMPTS`] are clamped down to it.
    ///
    /// Only consulted when [`Options::wal_enabled`] is `true`.
    pub wal_retry_attempts: u32,

    /// Base delay in milliseconds between WAL conversion attempts,
    /// doubling per attempt. Values `<= 0` fall back to
    /// [`DEFAULT_WAL_RETRY_BASE_MS`]; values above
    /// [`MAX_WAL_RETRY_BASE_MS`] are clamped down to it.
    ///
    /// Only consulted when [`Options::wal_enabled`] is `true`.
    pub wal_retry_base_ms: i64,
}

impl Options {
    /// Builds options for `path` with default WAL and busy timeout.
    pub fn new(path: impl Into<String>) -> Self {
        Self {
            path: path.into(),
            wal: None,
            busy_timeout: 0,
            wal_retry_attempts: 0,
            wal_retry_base_ms: 0,
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

    /// Sets the WAL conversion retry budget: `attempts` tries with
    /// `base_ms` between the first two, doubling thereafter up to
    /// [`MAX_WAL_RETRY_DELAY_MS`] per attempt.
    ///
    /// Out-of-range input is clamped, never rejected, matching how
    /// [`Options::with_busy_timeout`] treats its own: these are tuning
    /// hints on a best-effort backoff, not correctness-bearing input, and
    /// an [`open`] that fails because a caller asked for a hundred
    /// retries would trade a recoverable wait for a hard error. The
    /// bounds exist to stop an unbounded spin and to keep the doubling
    /// schedule clear of [`Duration`] overflow, not to police callers.
    #[must_use]
    pub fn with_wal_retry(mut self, attempts: u32, base_ms: i64) -> Self {
        self.wal_retry_attempts = attempts;
        self.wal_retry_base_ms = base_ms;
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

    /// Resolved WAL retry attempt count, applying the default for `0` and
    /// clamping to [`MAX_WAL_RETRY_ATTEMPTS`]. Always `>= 1`.
    pub fn wal_retry_attempts_resolved(&self) -> u32 {
        if self.wal_retry_attempts == 0 {
            DEFAULT_WAL_RETRY_ATTEMPTS
        } else {
            self.wal_retry_attempts.min(MAX_WAL_RETRY_ATTEMPTS)
        }
    }

    /// Resolved WAL retry base delay, applying the default for
    /// non-positive values and clamping to [`MAX_WAL_RETRY_BASE_MS`].
    pub fn wal_retry_base(&self) -> Duration {
        let ms = if self.wal_retry_base_ms <= 0 {
            DEFAULT_WAL_RETRY_BASE_MS
        } else {
            self.wal_retry_base_ms.min(MAX_WAL_RETRY_BASE_MS)
        };
        Duration::from_millis(ms as u64)
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
        set_wal(conn, opts)?;
    }

    Ok(())
}

/// Switches the journal to WAL, tolerating a concurrent first-open race.
///
/// Converting a database from rollback journal to WAL takes an exclusive
/// lock. SQLite acquires that lock *without* consulting the busy handler,
/// so the connection's `busy_timeout` cannot absorb the contention: when
/// several processes open one brand-new file at the same moment, the
/// losers get `SQLITE_BUSY` back in microseconds rather than waiting.
///
/// The contention is transient — it lasts only as long as one racer's
/// conversion — and another racer finishing the job is a success, not a
/// failure. So retry with bounded exponential backoff, re-reading the
/// mode each round: whoever wins, everyone ends up on WAL.
///
/// Retry is chosen over a process-global lock deliberately. A mutex would
/// only order threads inside one process, and cross-process access to a
/// single database file is a hard requirement here; backoff is the only
/// option that holds when the racers are separate processes.
///
/// The budget — attempt count and base delay — comes from
/// [`Options::wal_retry_attempts`] and [`Options::wal_retry_base_ms`],
/// both resolved and clamped, so it is always finite and at least one
/// attempt wide. Exhausting it is a loud `SQLITE_BUSY` error, never a
/// silent fallback to a non-WAL connection.
///
/// Only `SQLITE_BUSY` is retried. Every other error, and a mode that
/// settles on something other than WAL, propagates to the caller.
fn set_wal(conn: &Connection, opts: &Options) -> Result<(), SqlDbError> {
    let pragma = || SqlDbError::Pragma {
        pragma: "journal_mode=WAL".to_string(),
        source: rusqlite::Error::SqliteFailure(
            rusqlite::ffi::Error::new(rusqlite::ffi::SQLITE_BUSY),
            Some("journal_mode=WAL still busy after retries".to_string()),
        ),
    };

    let attempts = opts.wal_retry_attempts_resolved();
    let max_delay = Duration::from_millis(MAX_WAL_RETRY_DELAY_MS as u64);
    let mut delay = opts.wal_retry_base().min(max_delay);

    for attempt in 0..attempts {
        // A racer may already have converted the file; that is a success.
        if journal_mode(conn)?.eq_ignore_ascii_case("wal") {
            return Ok(());
        }

        // journal_mode returns the resulting mode as a row, so it must be
        // queried rather than issued as a plain update.
        match conn.pragma_update_and_check(None, "journal_mode", "WAL", |_| Ok(())) {
            Ok(()) => return Ok(()),
            Err(source) if is_busy(&source) => {
                if attempt + 1 == attempts {
                    break;
                }
                thread::sleep(delay);
                // Saturating double, then plateau: keeps the tail of the
                // schedule linear so the total wait stays bounded.
                delay = delay.saturating_mul(2).min(max_delay);
            }
            Err(source) => {
                return Err(SqlDbError::Pragma {
                    pragma: "journal_mode=WAL".to_string(),
                    source,
                })
            }
        }
    }

    // Retries exhausted. One last check: a racer may have landed WAL
    // between the final attempt and now.
    if journal_mode(conn)?.eq_ignore_ascii_case("wal") {
        return Ok(());
    }

    Err(pragma())
}

/// Reads the current journal mode.
fn journal_mode(conn: &Connection) -> Result<String, SqlDbError> {
    conn.query_row("PRAGMA journal_mode", [], |row| row.get::<_, String>(0))
        .map_err(|source| SqlDbError::Pragma {
            pragma: "journal_mode".to_string(),
            source,
        })
}

/// Reports whether `err` is a `SQLITE_BUSY` failure.
fn is_busy(err: &rusqlite::Error) -> bool {
    matches!(
        err.sqlite_error().map(|e| e.code),
        Some(rusqlite::ErrorCode::DatabaseBusy)
    )
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

    /// The pre-configurable budget was 10 attempts at a 2ms doubling base.
    #[test]
    fn wal_retry_defaults_match_historical_constants() {
        let opts = Options::new("x.db");
        assert_eq!(opts.wal_retry_attempts_resolved(), 10);
        assert_eq!(opts.wal_retry_base(), Duration::from_millis(2));
    }

    #[test]
    fn wal_retry_honours_explicit_budget() {
        let opts = Options::new("x.db").with_wal_retry(3, 25);
        assert_eq!(opts.wal_retry_attempts_resolved(), 3);
        assert_eq!(opts.wal_retry_base(), Duration::from_millis(25));
    }

    #[test]
    fn wal_retry_clamps_out_of_range() {
        // Zero attempts would be an unbounded-failure knob; sentinel.
        let zero = Options::new("x.db").with_wal_retry(0, 0);
        assert_eq!(
            zero.wal_retry_attempts_resolved(),
            DEFAULT_WAL_RETRY_ATTEMPTS
        );
        assert_eq!(
            zero.wal_retry_base(),
            Duration::from_millis(DEFAULT_WAL_RETRY_BASE_MS as u64)
        );

        // Negative base is out of range, same sentinel treatment as
        // `busy_timeout`.
        let negative = Options::new("x.db").with_wal_retry(1, -5);
        assert_eq!(negative.wal_retry_attempts_resolved(), 1);
        assert_eq!(
            negative.wal_retry_base(),
            Duration::from_millis(DEFAULT_WAL_RETRY_BASE_MS as u64)
        );

        // Absurd values clamp rather than erroring or overflowing.
        let huge = Options::new("x.db").with_wal_retry(u32::MAX, i64::MAX);
        assert_eq!(huge.wal_retry_attempts_resolved(), MAX_WAL_RETRY_ATTEMPTS);
        assert_eq!(
            huge.wal_retry_base(),
            Duration::from_millis(MAX_WAL_RETRY_BASE_MS as u64)
        );
    }

    /// The worst case a caller can request must be bounded in wall-clock
    /// terms, not merely finite. Bounding attempts and base delay
    /// independently is insufficient — the delay doubles, so the two
    /// bounds would multiply into an effective hang. The per-attempt
    /// ceiling is what keeps the total tractable.
    #[test]
    fn wal_retry_max_budget_is_bounded() {
        let opts = Options::new("x.db").with_wal_retry(u32::MAX, i64::MAX);
        let max_delay = Duration::from_millis(MAX_WAL_RETRY_DELAY_MS as u64);

        // Mirrors the schedule in `set_wal`.
        let mut delay = opts.wal_retry_base().min(max_delay);
        let mut total = Duration::ZERO;
        for _ in 0..opts.wal_retry_attempts_resolved() {
            total = total.saturating_add(delay);
            delay = delay.saturating_mul(2).min(max_delay);
        }

        let ceiling = max_delay * MAX_WAL_RETRY_ATTEMPTS;
        assert!(total <= ceiling, "total {total:?} exceeds {ceiling:?}");
        // Two minutes-ish, not geological time.
        assert!(total <= Duration::from_secs(180), "total {total:?}");
    }

    /// The default schedule must sum to roughly the historical ~1s, and
    /// must not be perturbed by the per-attempt ceiling.
    #[test]
    fn wal_retry_default_schedule_unchanged() {
        let opts = Options::new("x.db");
        let max_delay = Duration::from_millis(MAX_WAL_RETRY_DELAY_MS as u64);

        let mut delay = opts.wal_retry_base().min(max_delay);
        let mut schedule = Vec::new();
        for _ in 0..opts.wal_retry_attempts_resolved() {
            schedule.push(delay);
            delay = delay.saturating_mul(2).min(max_delay);
        }

        // 2, 4, 8, ... 1024 ms — pure doubling, ceiling never engages.
        let expected: Vec<Duration> = (0..10).map(|i| Duration::from_millis(2 << i)).collect();
        assert_eq!(schedule, expected);
        assert_eq!(
            schedule.iter().sum::<Duration>(),
            Duration::from_millis(2046)
        );
    }
}
