//! A [`SchemaDriver`] that keeps its version in a PostgreSQL table, over a
//! [`postgres::Client`] the caller opens. Go `upgrade/driver/postgres`.
//!
//! Kit connects to nothing and brings no TLS: the caller hands over the
//! [`SharedClient`] that reads and writes the version, and a connect
//! closure that returns a NEW connection to the same database for the lock
//! (the lock holds that session for the whole run, so it must be a session
//! of its own, not a pooled or shared one, and no transaction-mode pooler
//! may sit in front of the database). Table, SQL and lock key are the Go
//! driver's, so Go, Python, TypeScript, PHP and Rust migrators exclude each
//! other on one database.
//!
//! `backup` and `restore` return [`BackupUnsupported`]: a run goes on
//! without a backup and a rollback that needs a restore ends
//! [`NotRestored`](super::NotRestored).

use std::fmt;
use std::path::Path;
use std::sync::{Arc, Mutex, MutexGuard};
use std::thread;
use std::time::Duration;

use postgres::Client;

use crate::bus::Context;

use super::{BackupUnsupported, BoxError, Locker, SchemaDriver, Unlock};

/// SQL shared verbatim with the Go driver: the lock key text is hashed by
/// the server, so every port derives the same 64-bit key. The wait polls
/// the `try` form: a blocked `pg_advisory_lock` cannot be interrupted
/// without a cancel token, which needs the caller's TLS settings.
const SQL_TRY_LOCK: &str = "SELECT pg_try_advisory_lock(hashtextextended($1, 0))";
const SQL_UNLOCK: &str = "SELECT pg_advisory_unlock(hashtextextended($1, 0))";
const SQL_XACT_LOCK: &str = "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))";
const SQL_BACKEND_PID: &str = "SELECT pg_backend_pid()";

/// How often a blocked wait retries.
const POLL: Duration = Duration::from_millis(50);

/// The client a caller shares with the driver. `postgres::Client` is
/// `Send` but its methods take `&mut self`, so it travels in a mutex.
pub type SharedClient = Arc<Mutex<Client>>;

/// Opens a new connection to the same database, for the lock session.
type ConnectFn = Box<dyn Fn() -> Result<Client, BoxError> + Send + Sync>;

/// Keeps one schema's version in a PostgreSQL table and serializes
/// concurrent migrators with a session advisory lock. Never closes the
/// caller's client.
pub struct PostgresDriver {
    name: String,
    table: String,
    client: SharedClient,
    connect: ConnectFn,
}

impl PostgresDriver {
    /// Validates `table` (`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`, before any
    /// I/O), then creates it on `client` when absent, under an advisory
    /// lock so concurrent drivers do not collide in the catalog. `client`
    /// does the version reads and writes; `connect` returns a new
    /// connection for the lock, called once per run. The schema name
    /// defaults to `postgres`. Go `New`.
    pub fn new<F>(
        client: SharedClient,
        table: impl Into<String>,
        connect: F,
    ) -> Result<PostgresDriver, BoxError>
    where
        F: Fn() -> Result<Client, BoxError> + Send + Sync + 'static,
    {
        let table = table.into();
        validate_table(&table)?;
        let d = PostgresDriver {
            name: "postgres".to_string(),
            table,
            client,
            connect: Box::new(connect),
        };
        d.ensure_table()
            .map_err(|e| format!("postgres upgrade: migrate: {}", pg_err(&e)))?;
        Ok(d)
    }

    /// Overrides the schema name (default `postgres`): the `schema_name`
    /// row key and part of the lock key. Go `WithName`.
    pub fn with_name(mut self, name: impl Into<String>) -> PostgresDriver {
        self.name = name.into();
        self
    }

    /// The text hashed (server side) into a run's advisory lock key.
    pub fn lock_key(table: &str, name: &str) -> String {
        format!("kit-upgrade:{table}:{name}")
    }

    fn client(&self) -> MutexGuard<'_, Client> {
        lock_client(&self.client)
    }

    /// Creates the table under a transaction-scoped advisory lock:
    /// concurrent `CREATE TABLE IF NOT EXISTS` can still fail with a
    /// catalog unique violation when two sessions create one table at once.
    fn ensure_table(&self) -> Result<(), postgres::Error> {
        let ddl_key = format!("kit-upgrade:{}:ddl", self.table);
        let ddl = format!(
            "CREATE TABLE IF NOT EXISTS {} (schema_name TEXT PRIMARY KEY, version TEXT NOT NULL DEFAULT '')",
            self.table
        );
        let mut client = self.client();
        let mut tx = client.transaction()?;
        tx.query(SQL_XACT_LOCK, &[&ddl_key])?;
        tx.batch_execute(&ddl)?;
        tx.commit()
    }
}

impl fmt::Debug for PostgresDriver {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("PostgresDriver")
            .field("name", &self.name)
            .field("table", &self.table)
            .finish_non_exhaustive()
    }
}

/// The server's own message for a failed statement: `postgres::Error`
/// displays as just "db error" and keeps the text in its source.
fn pg_err(e: &postgres::Error) -> String {
    if let Some(db) = e.as_db_error() {
        return db.to_string();
    }
    let mut text = e.to_string();
    let mut src = std::error::Error::source(e);
    while let Some(cause) = src {
        text.push_str(": ");
        text.push_str(&cause.to_string());
        src = cause.source();
    }
    text
}

fn lock_client(shared: &SharedClient) -> MutexGuard<'_, Client> {
    shared
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

fn validate_table(name: &str) -> Result<(), BoxError> {
    let b = name.as_bytes();
    let ok = (1..=64).contains(&b.len())
        && (b[0].is_ascii_alphabetic() || b[0] == b'_')
        && b.iter().all(|c| c.is_ascii_alphanumeric() || *c == b'_');
    if ok {
        Ok(())
    } else {
        Err(format!("invalid table name: {name:?}").into())
    }
}

impl SchemaDriver for PostgresDriver {
    fn name(&self) -> &str {
        &self.name
    }

    /// The stored version; no row means `""`.
    fn version(&self) -> Result<String, BoxError> {
        let q = format!("SELECT version FROM {} WHERE schema_name = $1", self.table);
        let row = self
            .client()
            .query_opt(&q, &[&self.name])
            .map_err(|e| format!("postgres upgrade: version: {}", pg_err(&e)))?;
        Ok(row.map(|r| r.get::<_, String>(0)).unwrap_or_default())
    }

    /// Upserts the row for this schema.
    fn set_version(&mut self, version: &str) -> Result<(), BoxError> {
        let q = format!(
            "INSERT INTO {} (schema_name, version) VALUES ($1, $2) \
             ON CONFLICT (schema_name) DO UPDATE SET version = EXCLUDED.version",
            self.table
        );
        self.client()
            .execute(&q, &[&self.name, &version])
            .map_err(|e| format!("postgres upgrade: set version: {}", pg_err(&e)))?;
        Ok(())
    }

    /// The row is the only version: the Migrator writes no version file.
    fn stores_version(&self) -> bool {
        true
    }

    fn as_locker(&self) -> Option<&dyn Locker> {
        Some(self)
    }

    /// [`BackupUnsupported`]: kit does not back up a server database, so a
    /// run goes on without one. Take one outside kit when a failed
    /// migration must be recoverable.
    fn backup(&self, _dest: &Path) -> Result<(), BoxError> {
        Err(Box::new(BackupUnsupported))
    }

    /// [`BackupUnsupported`]: nothing is restored, so a rollback that needs
    /// it ends as [`NotRestored`](super::NotRestored).
    fn restore(&mut self, _src: &Path) -> Result<(), BoxError> {
        Err(Box::new(BackupUnsupported))
    }
}

impl Locker for PostgresDriver {
    /// Takes a session advisory lock on a connection from the connect
    /// closure and holds that session until the returned unlock runs. The
    /// wait polls `pg_try_advisory_lock` every 50ms and checks `ctx`
    /// between tries, so a cancel or deadline ends it with the context's
    /// error and holds nothing (the connection is dropped, ending the
    /// session). The unlock runs `pg_advisory_unlock` on that same
    /// connection; when it errors or answers false, the connection is
    /// closed, which ends the session and with it the lock.
    ///
    /// Refused when the closure returns the caller's own session (same
    /// `pg_backend_pid()`).
    fn lock(&self, ctx: &Context) -> Result<Unlock, BoxError> {
        let mut conn = (self.connect)().map_err(|e| format!("postgres upgrade: lock: {e}"))?;
        let mine = backend_pid(&mut self.client())?;
        let theirs = backend_pid(&mut conn)?;
        distinct_sessions(mine, theirs)?;
        let key = Self::lock_key(&self.table, &self.name);
        loop {
            if let Some(e) = ctx.err() {
                return Err(Box::new(e)); // `conn` drops: nothing held
            }
            match try_lock(&mut conn, &key) {
                Ok(true) => break,
                Ok(false) => thread::sleep(POLL),
                Err(e) => return Err(format!("postgres upgrade: lock: {}", pg_err(&e)).into()),
            }
        }
        Ok(Box::new(move || unlock(conn, &key)))
    }
}

fn backend_pid(c: &mut Client) -> Result<i32, BoxError> {
    c.query_one(SQL_BACKEND_PID, &[])
        .map(|r| r.get(0))
        .map_err(|e| format!("postgres upgrade: lock: {}", pg_err(&e)).into())
}

/// The lock session must not be the caller's: a lock held by the session
/// that also does the work would never exclude another run of it.
fn distinct_sessions(mine: i32, theirs: i32) -> Result<(), BoxError> {
    if mine == theirs {
        return Err(format!(
            "postgres upgrade: lock needs a connection of its own: connect returned the \
             caller's session (backend pid {mine}); the lock holds one session for the whole \
             run, so connect must open a new connection each time"
        )
        .into());
    }
    Ok(())
}

fn try_lock(conn: &mut Client, key: &str) -> Result<bool, postgres::Error> {
    conn.query_one(SQL_TRY_LOCK, &[&key]).map(|r| r.get(0))
}

/// Releases the lock on its own session, then closes that session. A
/// failed unlock closes it anyway: the server drops the session and every
/// advisory lock it holds.
fn unlock(mut conn: Client, key: &str) -> Result<(), BoxError> {
    let held: Result<bool, postgres::Error> = conn.query_one(SQL_UNLOCK, &[&key]).map(|r| r.get(0));
    let err = match held {
        Ok(true) => None,
        Ok(false) => Some("lock was not held by this session".to_string()),
        Err(e) => Some(pg_err(&e)),
    };
    let _ = conn.close();
    match err {
        None => Ok(()),
        Some(e) => Err(format!("postgres upgrade: unlock: {e}").into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn table_names() {
        let ok = ["t", "_t", "schema_versions", "T9", &"a".repeat(64)];
        for n in ok {
            assert!(validate_table(n).is_ok(), "{n:?}");
        }
        let long = "a".repeat(65);
        let bad = [
            "", "1t", "a-b", "a b", "a;b", "t\n", "a.b", "é", "a\"b", &long,
        ];
        for n in bad {
            let e = validate_table(n).unwrap_err().to_string();
            assert_eq!(e, format!("invalid table name: {n:?}"));
        }
    }

    #[test]
    fn lock_key_text() {
        assert_eq!(
            PostgresDriver::lock_key("schema_versions", "app"),
            "kit-upgrade:schema_versions:app"
        );
    }

    #[test]
    fn same_session_is_refused() {
        assert!(distinct_sessions(7, 8).is_ok());
        let e = distinct_sessions(7, 7).unwrap_err().to_string();
        assert!(e.contains("connection of its own"), "{e}");
        assert!(e.contains("backend pid 7"), "{e}");
    }
}
