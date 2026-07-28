//! Behavioural parity tests for the sqldb primitive, ported from the Go
//! `storage/sqldb` suite.

#![cfg(feature = "sqldb")]

use std::collections::BTreeMap;

use hop_top_kit::sqldb::{migrate, must_open, open, Options, SqlDbError, DEFAULT_BUSY_TIMEOUT_MS};

fn migrations() -> BTreeMap<i64, String> {
    let mut m = BTreeMap::new();
    m.insert(1, "CREATE TABLE items (id TEXT PRIMARY KEY)".to_string());
    m.insert(2, "ALTER TABLE items ADD COLUMN name TEXT".to_string());
    m
}

#[test]
fn open_creates_file() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("sub").join("test.db");

    let db = open(Options::new(path.to_str().unwrap())).unwrap();
    drop(db);

    assert!(path.exists(), "db file not created: {}", path.display());
}

#[test]
fn open_in_memory() {
    let db = open(Options::new(":memory:")).unwrap();
    let result: i64 = db.query_row("SELECT 1", [], |row| row.get(0)).unwrap();
    assert_eq!(result, 1);
}

#[test]
fn wal_mode() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("wal.db");
    let db = open(Options::new(path.to_str().unwrap())).unwrap();

    let mode: String = db
        .query_row("PRAGMA journal_mode", [], |row| row.get(0))
        .unwrap();
    assert_eq!(mode.to_lowercase(), "wal", "got journal_mode={mode}");
}

#[test]
fn wal_can_be_disabled() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("nowal.db");
    let db = open(Options::new(path.to_str().unwrap()).with_wal(false)).unwrap();

    let mode: String = db
        .query_row("PRAGMA journal_mode", [], |row| row.get(0))
        .unwrap();
    assert_ne!(mode.to_lowercase(), "wal", "WAL applied despite opt-out");
}

#[test]
fn busy_timeout() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("bt.db");
    let db = open(Options::new(path.to_str().unwrap()).with_busy_timeout(3000)).unwrap();

    let timeout: i64 = db
        .query_row("PRAGMA busy_timeout", [], |row| row.get(0))
        .unwrap();
    assert_eq!(timeout, 3000);
}

#[test]
fn busy_timeout_defaults() {
    let db = open(Options::new(":memory:")).unwrap();
    let timeout: i64 = db
        .query_row("PRAGMA busy_timeout", [], |row| row.get(0))
        .unwrap();
    assert_eq!(timeout, DEFAULT_BUSY_TIMEOUT_MS);
}

#[test]
fn foreign_keys_on() {
    let db = open(Options::new(":memory:")).unwrap();
    let fk: i64 = db
        .query_row("PRAGMA foreign_keys", [], |row| row.get(0))
        .unwrap();
    assert_eq!(fk, 1, "foreign_keys not enabled");
}

#[test]
fn migrate_idempotent() {
    let db = open(Options::new(":memory:")).unwrap();
    let m = migrations();

    migrate(&db, "schema_versions", &m).unwrap();
    // Run again — must be idempotent.
    migrate(&db, "schema_versions", &m).unwrap();

    db.execute("INSERT INTO items (id, name) VALUES ('a', 'Alice')", [])
        .unwrap();

    let applied: i64 = db
        .query_row("SELECT COUNT(*) FROM schema_versions", [], |row| row.get(0))
        .unwrap();
    assert_eq!(applied, 2, "each version recorded exactly once");
}

#[test]
fn migrate_applies_in_order() {
    let db = open(Options::new(":memory:")).unwrap();
    let mut m = BTreeMap::new();
    // Inserted out of order; ascending application is what makes the
    // ALTER valid.
    m.insert(2, "ALTER TABLE ordered ADD COLUMN name TEXT".to_string());
    m.insert(1, "CREATE TABLE ordered (id TEXT PRIMARY KEY)".to_string());

    migrate(&db, "schema_versions", &m).unwrap();

    db.execute("INSERT INTO ordered (id, name) VALUES ('a', 'Alice')", [])
        .unwrap();
}

#[test]
fn migrate_rejects_bad_table_name() {
    let db = open(Options::new(":memory:")).unwrap();
    let err = migrate(&db, "bad name; DROP TABLE x", &BTreeMap::new()).unwrap_err();
    assert!(matches!(err, SqlDbError::InvalidTable(_)), "got {err:?}");
}

#[test]
fn migrate_rolls_back_failure() {
    let db = open(Options::new(":memory:")).unwrap();
    let mut m = BTreeMap::new();
    m.insert(1, "CREATE TABLE ok (id TEXT PRIMARY KEY)".to_string());
    m.insert(2, "THIS IS NOT SQL".to_string());

    let err = migrate(&db, "schema_versions", &m).unwrap_err();
    assert!(
        matches!(err, SqlDbError::Migrate { version: 2, .. }),
        "got {err:?}"
    );

    // v1 stays applied and recorded; v2 did not land.
    let applied: i64 = db
        .query_row("SELECT COUNT(*) FROM schema_versions", [], |row| row.get(0))
        .unwrap();
    assert_eq!(applied, 1);

    // Connection is usable — the failed transaction was rolled back.
    db.execute("INSERT INTO ok (id) VALUES ('a')", []).unwrap();
}

#[test]
fn migrate_resumes_after_failure() {
    let db = open(Options::new(":memory:")).unwrap();
    let mut broken = BTreeMap::new();
    broken.insert(1, "CREATE TABLE resume (id TEXT PRIMARY KEY)".to_string());
    broken.insert(2, "NOT SQL".to_string());
    migrate(&db, "schema_versions", &broken).unwrap_err();

    let mut fixed = BTreeMap::new();
    fixed.insert(1, "CREATE TABLE resume (id TEXT PRIMARY KEY)".to_string());
    fixed.insert(2, "ALTER TABLE resume ADD COLUMN name TEXT".to_string());
    migrate(&db, "schema_versions", &fixed).unwrap();

    db.execute("INSERT INTO resume (id, name) VALUES ('a', 'Alice')", [])
        .unwrap();
}

#[test]
fn open_requires_path() {
    let err = open(Options::new("")).unwrap_err();
    assert!(matches!(err, SqlDbError::PathRequired), "got {err:?}");
}

#[test]
fn open_preserves_dsn_query() {
    let db = open(Options::new(":memory:?cache=shared")).unwrap();
    let result: i64 = db.query_row("SELECT 1", [], |row| row.get(0)).unwrap();
    assert_eq!(result, 1);

    // A DSN-shaped in-memory path must not leave a literal file behind.
    assert!(
        !std::path::Path::new(":memory:?cache=shared").exists(),
        "in-memory DSN created a file on disk"
    );
}

#[test]
#[should_panic(expected = "path required")]
fn must_open_panics() {
    let _ = must_open(Options::new(""));
}
