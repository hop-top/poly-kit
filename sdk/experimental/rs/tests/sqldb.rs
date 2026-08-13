//! Behavioural parity tests for the sqldb primitive, ported from the Go
//! `storage/sqldb` suite.

#![cfg(feature = "sqldb")]

use std::collections::BTreeMap;

use hop_top_kit::sqldb::{
    migrate, must_open, open, Options, SqlDbError, DEFAULT_BUSY_TIMEOUT_MS, MAX_WAL_RETRY_BASE_MS,
};

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

/// Racing opens of one brand-new file must all succeed on WAL.
///
/// Converting a fresh database from rollback journal to WAL takes an
/// exclusive lock that SQLite acquires without consulting the busy
/// handler, so `busy_timeout` does not cover it. Every racer must still
/// end up with a usable WAL connection.
///
/// Threads stand in for the processes this guards against: the retry is
/// backoff on the losing connection, not in-process coordination, so the
/// same path runs whether the racers share an address space or not.
#[test]
fn concurrent_first_open_converges_on_wal() {
    let dir = tempfile::tempdir().unwrap();

    // Several fresh files: the race only exists on the very first open
    // of a given path, so one file gives one chance to hit it.
    for round in 0..16 {
        let path = dir.path().join(format!("race{round}.db"));
        let path = path.to_str().unwrap().to_string();
        let threads = 8;
        let barrier = std::sync::Arc::new(std::sync::Barrier::new(threads));

        let handles: Vec<_> = (0..threads)
            .map(|_| {
                let path = path.clone();
                let barrier = std::sync::Arc::clone(&barrier);
                std::thread::spawn(move || {
                    barrier.wait();
                    let db = open(Options::new(&path)).map_err(|e| e.to_string())?;
                    db.query_row("PRAGMA journal_mode", [], |row| row.get::<_, String>(0))
                        .map_err(|e| e.to_string())
                })
            })
            .collect();

        for handle in handles {
            let mode = handle.join().expect("thread panicked");
            assert_eq!(
                mode.as_deref().map(str::to_lowercase),
                Ok("wal".to_string()),
                "round {round}: got {mode:?}"
            );
        }
    }
}

/// Holds a shared read lock on a fresh non-WAL database.
///
/// A read lock is what makes this harness usable: it blocks the exclusive
/// lock the WAL conversion needs, while still permitting the `PRAGMA
/// journal_mode` reads the retry loop performs each round. An EXCLUSIVE
/// lock would instead fail those reads outright, short-circuiting the
/// loop before any retry budget is consumed.
fn blocked_wal_db(dir: &std::path::Path, name: &str) -> (rusqlite::Connection, String) {
    let path = dir.join(name);
    let path = path.to_str().unwrap().to_string();

    let holder = open(Options::new(&path).with_wal(false)).unwrap();
    holder.execute_batch("CREATE TABLE t (x)").unwrap();
    holder.execute_batch("BEGIN").unwrap();
    let _: i64 = holder
        .query_row("SELECT count(*) FROM t", [], |row| row.get(0))
        .unwrap();

    (holder, path)
}

/// A deliberately tiny budget must give up fast, and give up *loudly*.
///
/// The contract being pinned: exhaustion is an error mentioning the WAL
/// pragma, never a silent downgrade to a rollback-journal connection.
#[test]
fn wal_retry_tiny_budget_fails_loudly() {
    let dir = tempfile::tempdir().unwrap();
    let (_holder, path) = blocked_wal_db(dir.path(), "tiny.db");

    let start = std::time::Instant::now();
    let err = open(
        Options::new(&path)
            .with_busy_timeout(1)
            .with_wal_retry(2, 1),
    )
    .expect_err("WAL conversion should not succeed against a held read lock");

    let msg = err.to_string();
    assert!(
        matches!(err, SqlDbError::Pragma { .. }),
        "expected a pragma error, got {err:?}"
    );
    assert!(
        msg.contains("journal_mode=WAL"),
        "error must name the failing pragma: {msg}"
    );

    // 2 attempts at a 1ms base is a handful of milliseconds; a generous
    // bound still proves the budget, not busy_timeout, ended it.
    assert!(
        start.elapsed() < std::time::Duration::from_secs(2),
        "tiny budget took {:?} — not fast-failing",
        start.elapsed()
    );
}

/// A wider budget must actually spend more time before giving up.
///
/// Same permanently-blocked database, so both budgets are exhausted; the
/// only variable is how long each is willing to wait. That the wider one
/// waits measurably longer is what proves the knob reaches the retry
/// loop rather than being decorative.
#[test]
fn wal_retry_wider_budget_waits_longer() {
    let dir = tempfile::tempdir().unwrap();

    let (_h1, tiny_path) = blocked_wal_db(dir.path(), "narrow.db");
    let start = std::time::Instant::now();
    open(
        Options::new(&tiny_path)
            .with_busy_timeout(1)
            .with_wal_retry(1, 1),
    )
    .expect_err("held read lock must block conversion");
    let tiny = start.elapsed();

    let (_h2, wide_path) = blocked_wal_db(dir.path(), "wide.db");
    let start = std::time::Instant::now();
    open(
        Options::new(&wide_path)
            .with_busy_timeout(1)
            .with_wal_retry(6, 10),
    )
    .expect_err("held read lock must block conversion");
    let wide = start.elapsed();

    // Wide schedule sleeps 10+20+40+80+160 = 310ms across its 6 attempts;
    // the tiny one sleeps not at all. Assert well under that to stay
    // robust on a loaded runner, while still separating the two.
    assert!(
        wide > tiny,
        "wider budget did not wait longer: wide={wide:?} tiny={tiny:?}"
    );
    assert!(
        wide >= std::time::Duration::from_millis(150),
        "wider budget barely waited: {wide:?}"
    );
}

/// Releasing the lock mid-retry must let a wide budget converge on WAL.
///
/// The tiny budget expires before the lock lifts and fails; the wide one
/// outlasts it and succeeds. This is the retry loop's whole purpose —
/// that a racer finishing its work turns a failure into a success.
#[test]
fn wal_retry_wide_budget_survives_transient_lock() {
    let dir = tempfile::tempdir().unwrap();
    let (holder, path) = blocked_wal_db(dir.path(), "transient.db");

    // Tiny budget, lock still held: must fail.
    open(
        Options::new(&path)
            .with_busy_timeout(1)
            .with_wal_retry(1, 1),
    )
    .expect_err("tiny budget must not outlast a held lock");

    // Release the lock shortly after the wide open starts retrying.
    let handle = std::thread::spawn(move || {
        std::thread::sleep(std::time::Duration::from_millis(150));
        holder.execute_batch("ROLLBACK").unwrap();
        drop(holder);
    });

    let db = open(
        Options::new(&path)
            .with_busy_timeout(1)
            .with_wal_retry(20, 5),
    )
    .expect("wide budget should outlast a transient lock");

    handle.join().unwrap();

    let mode: String = db
        .query_row("PRAGMA journal_mode", [], |row| row.get(0))
        .unwrap();
    assert_eq!(mode.to_lowercase(), "wal", "got journal_mode={mode}");
}

/// Out-of-range knobs are clamped into the working range, not rejected.
///
/// Absurd input must still produce a usable connection — the documented
/// clamp-don't-error contract, matching `busy_timeout`.
#[test]
fn wal_retry_out_of_range_still_opens() {
    let dir = tempfile::tempdir().unwrap();

    for (i, (attempts, base)) in [(0u32, 0i64), (0, -1), (u32::MAX, i64::MAX), (1, i64::MIN)]
        .into_iter()
        .enumerate()
    {
        let path = dir.path().join(format!("clamp{i}.db"));
        let db = open(Options::new(path.to_str().unwrap()).with_wal_retry(attempts, base))
            .unwrap_or_else(|e| panic!("attempts={attempts} base={base} failed: {e}"));

        let mode: String = db
            .query_row("PRAGMA journal_mode", [], |row| row.get(0))
            .unwrap();
        assert_eq!(
            mode.to_lowercase(),
            "wal",
            "attempts={attempts} base={base}"
        );
    }
}

/// An uncontended open must not sleep, whatever the configured budget.
///
/// Guards against a regression where the loop sleeps before its first
/// attempt: with a 60s base that would be immediately obvious here.
#[test]
fn wal_retry_does_not_sleep_when_uncontended() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("fast.db");

    let start = std::time::Instant::now();
    let db = open(Options::new(path.to_str().unwrap()).with_wal_retry(32, MAX_WAL_RETRY_BASE_MS))
        .unwrap();
    let elapsed = start.elapsed();

    let mode: String = db
        .query_row("PRAGMA journal_mode", [], |row| row.get(0))
        .unwrap();
    assert_eq!(mode.to_lowercase(), "wal");
    assert!(
        elapsed < std::time::Duration::from_secs(1),
        "uncontended open slept: {elapsed:?}"
    );
}
