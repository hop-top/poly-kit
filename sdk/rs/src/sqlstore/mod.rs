//! Typed key/value store backed by SQLite, built on [`crate::sqldb`].
//!
//! Values are JSON-serialised before storage and deserialised on retrieval,
//! so any [`serde`]-serialisable type works as a value. Keys are plain
//! strings with no namespacing; callers own key uniqueness.
//!
//! Mirrors the Go `storage/sqlstore` package:
//!
//! - [`Store`] — the JSON kv store, with optional TTL expiry.
//! - [`backup`] — file and blob-backed backup/restore helpers.
//! - [`EncryptedStore`] — at-rest encryption wrapper (feature `sqlstore-encrypt`).
//!
//! # Migration strategy
//!
//! The Go implementation runs its own ad hoc `CREATE TABLE IF NOT EXISTS`
//! in `Store.migrate` rather than going through `sqldb.Migrate`, and
//! concatenates `Options.MigrateSQL` onto it. That split exists because the
//! Go migration runner takes an ordered slice keyed by dense version
//! numbers, so a library that wants to own version 1 while letting the
//! caller add tables has no clean way to interleave.
//!
//! This port unifies on [`crate::sqldb::migrate`] instead. The Rust runner
//! takes an ordered, gap-tolerant `BTreeMap<i64, String>`, which removes
//! the obstacle: the store claims version 1 for the `kv` table, and
//! caller-supplied statements land at [`USER_MIGRATION_VERSION`], leaving
//! the whole numeric range in between free for future built-in migrations.
//!
//! Unifying buys three properties the ad hoc path lacks:
//!
//! - Caller SQL runs inside a transaction and rolls back on failure,
//!   instead of leaving a half-applied schema behind.
//! - Caller SQL runs exactly once, recorded in the bookkeeping table,
//!   rather than being re-executed on every [`Store::open`] — so
//!   non-idempotent statements such as seed-data inserts are now safe.
//! - Schema state is introspectable through the same `schema_versions`
//!   table every other `sqldb` consumer uses.
//!
//! The one behavioural difference worth noting: because caller SQL is now
//! versioned, editing [`Options::migrate_sql`] after a database exists will
//! not re-run it. That is the correct semantics for a migration and matches
//! how every other `sqldb` consumer behaves, but it does differ from Go's
//! run-every-open treatment of `MigrateSQL`.

pub mod backup;
#[cfg(feature = "sqlstore-encrypt")]
pub mod crypto;
#[cfg(feature = "sqlstore-encrypt")]
mod encrypted;

#[cfg(feature = "sqlstore-encrypt")]
pub use encrypted::EncryptedStore;

use std::collections::BTreeMap;
use std::time::Duration;

use rusqlite::{Connection, OptionalExtension};
use serde::de::DeserializeOwned;
use serde::Serialize;
use thiserror::Error;

use crate::sqldb::{self, SqlDbError};

/// Name of the bookkeeping table recording applied migrations.
pub const MIGRATIONS_TABLE: &str = "schema_versions";

/// Migration version owned by the store for its own `kv` table.
pub const KV_MIGRATION_VERSION: i64 = 1;

/// Migration version assigned to caller-supplied [`Options::migrate_sql`].
///
/// Deliberately far above [`KV_MIGRATION_VERSION`] so the store can add
/// built-in migrations later without colliding with caller schema.
pub const USER_MIGRATION_VERSION: i64 = 1000;

/// Errors returned by [`Store`] and [`EncryptedStore`] operations.
#[derive(Debug, Error)]
pub enum StoreError {
    /// Opening or migrating the underlying database failed.
    #[error("sqlstore: {0}")]
    Db(#[from] SqlDbError),

    /// A query against the `kv` table failed.
    #[error("sqlstore: query: {0}")]
    Query(#[source] rusqlite::Error),

    /// A value could not be serialised to, or deserialised from, JSON.
    #[error("sqlstore: json: {0}")]
    Json(#[from] serde_json::Error),

    /// The AEAD refused to seal a value.
    #[cfg(feature = "sqlstore-encrypt")]
    #[error("sqlstore: encrypt failed")]
    Encrypt,

    /// The stored ciphertext is too short to contain a nonce and tag.
    #[cfg(feature = "sqlstore-encrypt")]
    #[error("sqlstore: ciphertext too short")]
    CiphertextTooShort,

    /// Authentication failed — corrupt ciphertext, or the wrong key.
    #[cfg(feature = "sqlstore-encrypt")]
    #[error("sqlstore: decryption failed")]
    Decrypt,
}

/// Controls [`Store`] behaviour.
#[derive(Debug, Clone, Default)]
pub struct Options {
    /// Maximum age of a stored value. [`Store::get`] returns `None` for any
    /// entry older than this. `None` disables expiry.
    ///
    /// Expired entries are not deleted by [`Store::get`]. Callers worried
    /// about storage growth should periodically sweep the `kv` table.
    pub ttl: Option<Duration>,

    /// Extra SQL applied once, after the `kv` table is created.
    ///
    /// Use it to add application-specific tables, indexes, or seed data.
    /// Multiple statements may be separated by semicolons. Applied at
    /// [`USER_MIGRATION_VERSION`] and therefore run exactly once per
    /// database, inside a transaction.
    pub migrate_sql: Option<String>,
}

impl Options {
    /// Options with no TTL and no extra migration SQL.
    pub fn new() -> Self {
        Self::default()
    }

    /// Sets the value expiry window.
    #[must_use]
    pub fn with_ttl(mut self, ttl: Duration) -> Self {
        self.ttl = Some(ttl);
        self
    }

    /// Sets caller-supplied migration SQL.
    #[must_use]
    pub fn with_migrate_sql(mut self, sql: impl Into<String>) -> Self {
        self.migrate_sql = Some(sql.into());
        self
    }
}

/// A JSON key/value store backed by a SQLite database.
///
/// Build one with [`Store::open`]; drop it to close the connection.
#[derive(Debug)]
pub struct Store {
    conn: Connection,
    opts: Options,
}

impl Store {
    /// Opens (or creates) a SQLite database at `path` and applies the
    /// store's migrations.
    ///
    /// Missing parent directories are created. The connection comes from
    /// [`crate::sqldb::open`], so it carries the standard pragma set (WAL,
    /// busy timeout, foreign keys) and handles DSN-shaped in-memory paths
    /// correctly.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Db`] when the database cannot be opened or a
    /// migration fails.
    pub fn open(path: impl Into<String>, opts: Options) -> Result<Self, StoreError> {
        Self::open_with(sqldb::Options::new(path), opts)
    }

    /// Like [`Store::open`] but with full control over the connection
    /// options (WAL, busy timeout).
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Db`] when the database cannot be opened or a
    /// migration fails.
    pub fn open_with(db_opts: sqldb::Options, opts: Options) -> Result<Self, StoreError> {
        let conn = sqldb::open(db_opts)?;
        let store = Store { conn, opts };
        store.migrate()?;
        Ok(store)
    }

    /// Wraps an already-open connection, applying the store's migrations.
    ///
    /// The caller is responsible for having applied
    /// [`crate::sqldb::apply_pragmas`].
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Db`] when a migration fails.
    pub fn from_connection(conn: Connection, opts: Options) -> Result<Self, StoreError> {
        let store = Store { conn, opts };
        store.migrate()?;
        Ok(store)
    }

    fn migrate(&self) -> Result<(), StoreError> {
        let mut migrations = BTreeMap::new();
        migrations.insert(
            KV_MIGRATION_VERSION,
            "CREATE TABLE IF NOT EXISTS kv (
               key       TEXT PRIMARY KEY,
               stored_at TEXT NOT NULL,
               payload   TEXT NOT NULL
             )"
            .to_string(),
        );
        if let Some(sql) = &self.opts.migrate_sql {
            if !sql.is_empty() {
                migrations.insert(USER_MIGRATION_VERSION, sql.clone());
            }
        }
        sqldb::migrate(&self.conn, MIGRATIONS_TABLE, &migrations)?;
        Ok(())
    }

    /// Serialises `value` as JSON and upserts it under `key`.
    ///
    /// An existing entry has both its payload and its `stored_at` stamp
    /// overwritten, so a re-put refreshes the TTL window.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Json`] if `value` cannot be serialised and
    /// [`StoreError::Query`] if the write fails.
    pub fn put<T: Serialize + ?Sized>(&self, key: &str, value: &T) -> Result<(), StoreError> {
        let payload = serde_json::to_string(value)?;
        self.put_raw(key, &payload)
    }

    /// Upserts a pre-serialised JSON `payload` under `key`.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Query`] if the write fails.
    pub fn put_raw(&self, key: &str, payload: &str) -> Result<(), StoreError> {
        self.conn
            .execute(
                "INSERT INTO kv (key, stored_at, payload) VALUES (?1, ?2, ?3)
                 ON CONFLICT(key) DO UPDATE SET
                   stored_at = excluded.stored_at,
                   payload   = excluded.payload",
                rusqlite::params![key, now_rfc3339(), payload],
            )
            .map_err(StoreError::Query)?;
        Ok(())
    }

    /// Reads the value stored under `key` and deserialises it.
    ///
    /// Returns `Ok(None)` when the key is absent or the entry has aged past
    /// [`Options::ttl`] — an expired entry is indistinguishable from a
    /// missing one, matching Go.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Query`] on a read failure and
    /// [`StoreError::Json`] when the stored payload does not match `T`.
    pub fn get<T: DeserializeOwned>(&self, key: &str) -> Result<Option<T>, StoreError> {
        match self.get_raw(key)? {
            Some(payload) => Ok(Some(serde_json::from_str(&payload)?)),
            None => Ok(None),
        }
    }

    /// Reads the raw JSON payload stored under `key`, applying TTL expiry.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Query`] on a read failure.
    pub fn get_raw(&self, key: &str) -> Result<Option<String>, StoreError> {
        let row: Option<(String, String)> = self
            .conn
            .query_row(
                "SELECT stored_at, payload FROM kv WHERE key = ?1",
                [key],
                |row| Ok((row.get(0)?, row.get(1)?)),
            )
            .optional()
            .map_err(StoreError::Query)?;

        let Some((stored_at, payload)) = row else {
            return Ok(None);
        };

        if let Some(ttl) = self.opts.ttl {
            // An unparseable stamp is treated as expired, as in Go: a row we
            // cannot date is a row we cannot vouch for.
            if !within_ttl(&stored_at, ttl) {
                return Ok(None);
            }
        }
        Ok(Some(payload))
    }

    /// Removes the entry stored under `key`.
    ///
    /// Returns `true` when a row was removed.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Query`] if the delete fails.
    pub fn delete(&self, key: &str) -> Result<bool, StoreError> {
        let n = self
            .conn
            .execute("DELETE FROM kv WHERE key = ?1", [key])
            .map_err(StoreError::Query)?;
        Ok(n > 0)
    }

    /// Borrows the underlying connection for custom queries against the
    /// same database.
    pub fn conn(&self) -> &Connection {
        &self.conn
    }

    /// Consumes the store, returning the underlying connection.
    pub fn into_connection(self) -> Connection {
        self.conn
    }
}

/// Current UTC time formatted as Go's `time.RFC3339` renders it.
fn now_rfc3339() -> String {
    format_rfc3339(unix_seconds_now())
}

/// Seconds since the Unix epoch, saturating at 0 for pre-epoch clocks.
fn unix_seconds_now() -> i64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// Formats Unix `secs` as `YYYY-MM-DDTHH:MM:SSZ`.
///
/// Hand-rolled rather than pulling in a date-time crate: the store needs
/// exactly one format, in UTC, at second precision, and civil-date
/// conversion is a dozen lines of arithmetic. Uses the standard
/// days-from-civil algorithm run in reverse.
fn format_rfc3339(secs: i64) -> String {
    let days = secs.div_euclid(86_400);
    let rem = secs.rem_euclid(86_400);
    let (h, mi, s) = (rem / 3600, (rem % 3600) / 60, rem % 60);
    let (y, mo, d) = civil_from_days(days);
    format!("{y:04}-{mo:02}-{d:02}T{h:02}:{mi:02}:{s:02}Z")
}

/// Parses a `YYYY-MM-DDTHH:MM:SSZ` stamp back to Unix seconds.
///
/// Accepts a fractional-seconds suffix and a numeric offset so stamps
/// written by other RFC 3339 producers still parse. Returns `None` for
/// anything it cannot read.
fn parse_rfc3339(s: &str) -> Option<i64> {
    let b = s.as_bytes();
    if b.len() < 19 || b[4] != b'-' || b[7] != b'-' || (b[10] != b'T' && b[10] != b't') {
        return None;
    }
    let num = |r: std::ops::Range<usize>| s.get(r)?.parse::<i64>().ok();
    let (y, mo, d) = (num(0..4)?, num(5..7)?, num(8..10)?);
    let (h, mi, sec) = (num(11..13)?, num(14..16)?, num(17..19)?);
    if !(1..=12).contains(&mo) || !(1..=31).contains(&d) || h > 23 || mi > 59 || sec > 60 {
        return None;
    }

    let mut total = days_from_civil(y, mo as u32, d as u32) * 86_400 + h * 3600 + mi * 60 + sec;

    // Trailing zone: 'Z' or ±HH:MM, optionally after a fractional part.
    let mut rest = &s[19..];
    if let Some(stripped) = rest.strip_prefix('.') {
        let digits = stripped.len()
            - stripped
                .trim_start_matches(|c: char| c.is_ascii_digit())
                .len();
        rest = &stripped[digits..];
    }
    match rest.as_bytes().first() {
        None | Some(b'Z') | Some(b'z') => {}
        Some(sign @ (b'+' | b'-')) => {
            if rest.len() < 6 {
                return None;
            }
            let oh: i64 = rest.get(1..3)?.parse().ok()?;
            let om: i64 = rest.get(4..6)?.parse().ok()?;
            let offset = oh * 3600 + om * 60;
            total += if *sign == b'+' { -offset } else { offset };
        }
        _ => return None,
    }
    Some(total)
}

/// Reports whether the entry stamped `stored_at` is still inside `ttl`.
///
/// An unparseable stamp reports `false` (expired).
fn within_ttl(stored_at: &str, ttl: Duration) -> bool {
    let Some(ts) = parse_rfc3339(stored_at) else {
        return false;
    };
    let age = unix_seconds_now().saturating_sub(ts);
    // The stamp has second precision while the TTL may be sub-second, so a
    // value written and read inside the same wall-clock second reports an
    // age of 0. Comparing against a ceiling'd TTL keeps sub-second windows
    // from expiring a row the moment it is written.
    let ttl_secs = ttl.as_secs() as i64 + i64::from(ttl.subsec_nanos() > 0);
    age <= ttl_secs
}

/// Days since 1970-01-01 for a proleptic-Gregorian civil date.
fn days_from_civil(y: i64, m: u32, d: u32) -> i64 {
    let y = if m <= 2 { y - 1 } else { y };
    let era = if y >= 0 { y } else { y - 399 } / 400;
    let yoe = y - era * 400;
    let mp = i64::from((m + 9) % 12);
    let doy = (153 * mp + 2) / 5 + i64::from(d) - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    era * 146_097 + doe - 719_468
}

/// Inverse of [`days_from_civil`].
fn civil_from_days(z: i64) -> (i64, u32, u32) {
    let z = z + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = z - era * 146_097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (if m <= 2 { y + 1 } else { y }, m, d)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rfc3339_roundtrips() {
        for secs in [0i64, 1, 951_782_400, 1_700_000_000, 4_102_444_800] {
            let s = format_rfc3339(secs);
            assert_eq!(parse_rfc3339(&s), Some(secs), "roundtrip {s}");
        }
    }

    #[test]
    fn rfc3339_matches_known_stamps() {
        assert_eq!(format_rfc3339(0), "1970-01-01T00:00:00Z");
        assert_eq!(format_rfc3339(1_700_000_000), "2023-11-14T22:13:20Z");
        // 2000 is a leap year (divisible by 400); 1900 was not.
        assert_eq!(format_rfc3339(951_868_800), "2000-03-01T00:00:00Z");
    }

    #[test]
    fn parse_rfc3339_accepts_offsets_and_fractions() {
        let base = parse_rfc3339("2023-11-14T22:13:20Z").unwrap();
        assert_eq!(parse_rfc3339("2023-11-14T22:13:20.500Z"), Some(base));
        assert_eq!(parse_rfc3339("2023-11-14T23:13:20+01:00"), Some(base));
        assert_eq!(parse_rfc3339("2023-11-14T21:13:20-01:00"), Some(base));
    }

    #[test]
    fn parse_rfc3339_rejects_garbage() {
        assert_eq!(parse_rfc3339(""), None);
        assert_eq!(parse_rfc3339("not-a-timestamp"), None);
        assert_eq!(parse_rfc3339("2023-13-14T22:13:20Z"), None);
        assert_eq!(parse_rfc3339("2023-11-14 22:13:20"), None);
    }

    #[test]
    fn within_ttl_treats_unparseable_as_expired() {
        assert!(!within_ttl("garbage", Duration::from_secs(3600)));
    }
}
