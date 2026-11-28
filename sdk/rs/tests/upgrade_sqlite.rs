//! Ports of the Go `upgrade/driver/sqlite` tests: WAL-safe backup, both
//! modes, sidecar-cleaning restore, the file lock (including two real
//! processes racing one file) and end to end through the Migrator.

#![cfg(all(feature = "upgrade-sqlite", unix))]

use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use hop_top_kit::bus::{Context, ContextError, Event};
use hop_top_kit::sqldb::{self, Options};
use hop_top_kit::upgrade::sqlite::{SharedConnection, SqliteDriver};
use hop_top_kit::upgrade::{
    is_backup_unsupported, read_version_file_in, BoxError, Migration, Migrator, SchemaDriver,
};
use rusqlite::Connection;

fn bg() -> Context {
    Context::background()
}

/// A WAL-mode database with table `t(x)`; the connection stays open, so
/// committed rows stay in the `-wal` file until a checkpoint.
fn open_wal(path: &Path) -> Connection {
    let c = sqldb::open(Options::new(path.to_str().unwrap())).unwrap();
    c.execute("CREATE TABLE IF NOT EXISTS t (x INTEGER)", [])
        .unwrap();
    c
}

fn insert(c: &Connection, xs: &[i64]) {
    for x in xs {
        c.execute("INSERT INTO t (x) VALUES (?1)", [x]).unwrap();
    }
}

/// Reads `t(x)` through a connection of its own.
fn rows(path: &Path) -> Vec<i64> {
    let c = Connection::open(path).unwrap();
    let mut s = c.prepare("SELECT x FROM t ORDER BY x").unwrap();
    let r = s.query_map([], |r| r.get(0)).unwrap();
    r.map(Result::unwrap).collect()
}

fn integrity_ok(path: &Path) {
    let c = Connection::open(path).unwrap();
    let res: String = c
        .query_row("PRAGMA integrity_check", [], |r| r.get(0))
        .unwrap();
    assert_eq!(res, "ok");
}

fn sidecars(db: &Path) -> Vec<PathBuf> {
    ["-wal", "-shm", "-journal"]
        .iter()
        .map(|s| PathBuf::from(format!("{}{s}", db.display())))
        .collect()
}

fn shared(c: Connection) -> SharedConnection {
    Arc::new(Mutex::new(c))
}

#[test]
fn backup_contains_rows_still_in_the_wal() {
    for with_conn in [false, true] {
        let dir = tempfile::tempdir().unwrap();
        let db_path = dir.path().join("app.db");
        let c = open_wal(&db_path);
        insert(&c, &[1, 2, 3]);
        let wal =
            fs::metadata(format!("{}-wal", db_path.display())).expect("rows must sit in the WAL");
        assert!(wal.len() > 0);

        let mut d = SqliteDriver::new(&db_path);
        let keep = shared(c);
        if with_conn {
            d = d.with_connection(keep.clone());
        }
        let bk = dir.path().join("backup");
        d.backup(&bk).unwrap();

        let copy = bk.join("app.db");
        assert_eq!(rows(&copy), vec![1, 2, 3], "with_conn={with_conn}");
        integrity_ok(&copy);
        assert!(!bk.join("app.db-wal").exists(), "backup is one file");
        assert!(!bk.join("app.db.tmp").exists(), "no temp file left");
    }
}

#[test]
fn backup_odd_paths_stay_data() {
    for odd in [
        "with space",
        "o'brien",
        "q\"uote",
        "a?b#c%d",
        "x;DROP TABLE t;--",
    ] {
        let tmp = tempfile::tempdir().unwrap();
        let dir = tmp.path().join(odd);
        fs::create_dir_all(&dir).unwrap();
        let db_path = dir.join("my db'.sqlite");
        let c = open_wal(&db_path);
        insert(&c, &[7]);

        let bk = dir.join("back up").join(odd);
        SqliteDriver::new(&db_path).backup(&bk).unwrap();
        assert_eq!(rows(&bk.join("my db'.sqlite")), vec![7], "{odd}");

        let d = SqliteDriver::new(&db_path).with_connection(shared(c));
        d.backup(&bk).expect("second backup replaces the first");
        assert_eq!(rows(&bk.join("my db'.sqlite")), vec![7]);
        assert_eq!(rows(&db_path), vec![7], "source untouched");
    }
}

#[test]
fn backup_replaces_a_stale_backup() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    let c = open_wal(&db_path);
    insert(&c, &[1]);
    let d = SqliteDriver::new(&db_path);
    let bk = dir.path().join("bk");
    d.backup(&bk).unwrap();
    insert(&c, &[2]);
    d.backup(&bk).unwrap();
    assert_eq!(rows(&bk.join("app.db")), vec![1, 2]);
}

#[test]
fn backup_missing_file_is_nothing_to_back_up() {
    let dir = tempfile::tempdir().unwrap();
    let bk = dir.path().join("bk");
    SqliteDriver::new(dir.path().join("none.db"))
        .backup(&bk)
        .unwrap();
    assert!(!bk.join("none.db").exists());
}

#[test]
fn backup_not_a_database_fails_without_a_partial_file() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    fs::write(&db_path, "not sqlite, just bytes").unwrap();
    let err = SqliteDriver::new(&db_path)
        .backup(&dir.path().join("bk"))
        .unwrap_err();
    assert!(!is_backup_unsupported(&*err), "{err}");
    assert!(!dir.path().join("bk/app.db").exists(), "no partial backup");
}

/// The caller's connection is borrowed: usable after backup, after a failed
/// backup and after an unsupported restore.
#[test]
fn backup_with_connection_does_not_close_it() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    let c = shared(open_wal(&db_path));
    insert(&c.lock().unwrap(), &[1]);
    let mut d = SqliteDriver::new(&db_path).with_connection(c.clone());
    d.backup(&dir.path().join("bk")).unwrap();
    insert(&c.lock().unwrap(), &[2]);
    assert_eq!(rows(&db_path), vec![1, 2]);

    // A backup directory that cannot be created fails; the connection lives.
    assert!(d.backup(&db_path.join("nope")).is_err());
    let err = d.restore(&dir.path().join("bk")).unwrap_err();
    assert!(is_backup_unsupported(&*err), "{err}");
    insert(&c.lock().unwrap(), &[3]);
}

/// With a connection and no path the backup is named after the schema.
#[test]
fn backup_with_connection_and_no_path_is_named_for_the_schema() {
    let dir = tempfile::tempdir().unwrap();
    let c = open_wal(&dir.path().join("app.db"));
    insert(&c, &[5]);
    let bk = dir.path().join("bk");
    SqliteDriver::new("")
        .with_name("appdb")
        .with_connection(shared(c))
        .backup(&bk)
        .unwrap();
    assert_eq!(rows(&bk.join("appdb.db")), vec![5]);
}

/// A crashed writer's `-wal`, `-shm` and `-journal` sit beside the file:
/// after a restore none is left, so SQLite replays nothing onto it.
#[test]
fn restore_path_only_returns_earlier_state_without_sidecars() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    let c = open_wal(&db_path);
    insert(&c, &[1, 2]);
    let mut d = SqliteDriver::new(&db_path);
    let bk = dir.path().join("bk");
    d.backup(&bk).unwrap();

    insert(&c, &[3, 4]); // later write, still only in the WAL
    let crash = dir.path().join("crash");
    fs::create_dir_all(&crash).unwrap();
    fs::copy(&db_path, crash.join("app.db")).unwrap();
    fs::copy(
        format!("{}-wal", db_path.display()),
        crash.join("app.db-wal"),
    )
    .unwrap();
    drop(c); // precondition: no open connection on the file
    fs::remove_file(&db_path).unwrap();
    fs::copy(crash.join("app.db"), &db_path).unwrap();
    fs::copy(
        crash.join("app.db-wal"),
        format!("{}-wal", db_path.display()),
    )
    .unwrap();
    fs::write(format!("{}-shm", db_path.display()), "stale").unwrap();
    fs::write(format!("{}-journal", db_path.display()), "stale").unwrap();

    d.restore(&bk).unwrap();

    for p in sidecars(&db_path) {
        assert!(!p.exists(), "stale sidecar {}", p.display());
    }
    assert_eq!(rows(&db_path), vec![1, 2]);
    integrity_ok(&db_path);
    assert_eq!(rows(&bk.join("app.db")), vec![1, 2], "backup kept");
    for e in fs::read_dir(dir.path()).unwrap() {
        let n = e.unwrap().file_name().to_string_lossy().into_owned();
        assert!(!n.contains(".restore-"), "temp file left: {n}");
    }
}

#[test]
fn restore_keeps_permissions() {
    use std::os::unix::fs::PermissionsExt;
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    let c = open_wal(&db_path);
    insert(&c, &[1]);
    let bk = dir.path().join("bk");
    SqliteDriver::new(&db_path).backup(&bk).unwrap();
    drop(c);
    fs::set_permissions(&db_path, fs::Permissions::from_mode(0o640)).unwrap();
    SqliteDriver::new(&db_path).restore(&bk).unwrap();
    let mode = fs::metadata(&db_path).unwrap().permissions().mode() & 0o777;
    assert_eq!(mode, 0o640);
}

#[test]
fn restore_missing_backup_fails_and_leaves_the_database() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    let c = open_wal(&db_path);
    insert(&c, &[1]);
    drop(c);
    assert!(SqliteDriver::new(&db_path)
        .restore(&dir.path().join("none"))
        .is_err());
    assert_eq!(rows(&db_path), vec![1], "database untouched");
}

/// With a connection restore does nothing and says so; file and backup
/// stay as they were.
#[test]
fn restore_with_connection_is_unsupported_and_touches_nothing() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    let c = open_wal(&db_path);
    insert(&c, &[1]);
    let mut d = SqliteDriver::new(&db_path).with_connection(shared(c));
    let bk = dir.path().join("bk");
    d.backup(&bk).unwrap();
    let again = open_wal(&db_path);
    insert(&again, &[2]);

    let err = d.restore(&bk).unwrap_err();
    assert!(is_backup_unsupported(&*err), "{err}");
    assert_eq!(rows(&db_path), vec![1, 2], "database untouched");
    assert_eq!(rows(&bk.join("app.db")), vec![1], "backup kept");
}

// ---- lock ----

#[test]
fn lock_file_sits_beside_the_version_file() {
    let dir = tempfile::tempdir().unwrap();
    let d = SqliteDriver::new("/x/app.db")
        .with_name("appdb")
        .with_data_dir(dir.path());
    let unlock = d.as_locker().unwrap().lock(&bg()).unwrap();
    assert!(
        dir.path().join("appdb/.lock").exists(),
        "<data dir>/<name>/.lock"
    );
    unlock().unwrap();
}

/// Same rule in both modes; two drivers (as two processes would) on one
/// file exclude each other. The wait is context-bound and the lock is
/// reusable after unlock.
#[test]
fn lock_excludes_and_honours_the_context() {
    for with_conn in [false, true] {
        let dir = tempfile::tempdir().unwrap();
        let db_path = dir.path().join("app.db");
        let c = shared(open_wal(&db_path));
        let mk = || {
            let d = SqliteDriver::new(&db_path)
                .with_name("appdb")
                .with_data_dir(dir.path());
            if with_conn {
                d.with_connection(c.clone())
            } else {
                d
            }
        };
        let (a, b) = (mk(), mk());
        let unlock_a = a.as_locker().unwrap().lock(&bg()).unwrap();

        // Deadline while waiting: the context's error, nothing held.
        let (ctx, _h) = bg().with_timeout(Duration::from_millis(150));
        let start = Instant::now();
        let err = b.as_locker().unwrap().lock(&ctx).err().expect("held");
        assert_eq!(
            err.downcast_ref::<ContextError>(),
            Some(&ContextError::DeadlineExceeded)
        );
        assert!(start.elapsed() >= Duration::from_millis(100), "it did wait");
        assert!(start.elapsed() < Duration::from_secs(2));

        // Canceled by the caller mid-wait.
        let (ctx, cancel) = bg().with_cancel();
        let t = thread::spawn(move || {
            thread::sleep(Duration::from_millis(80));
            cancel.cancel();
        });
        let err = b.as_locker().unwrap().lock(&ctx).err().expect("held");
        assert_eq!(
            err.downcast_ref::<ContextError>(),
            Some(&ContextError::Canceled)
        );
        t.join().unwrap();

        // Blocks until a releases, then acquires.
        let (tx, rx) = std::sync::mpsc::channel();
        let waiter = {
            let b = b.clone();
            thread::spawn(move || {
                let r = b
                    .as_locker()
                    .unwrap()
                    .lock(&Context::background())
                    .and_then(|u| u());
                tx.send(r.map_err(|e| e.to_string())).unwrap();
            })
        };
        assert!(
            rx.recv_timeout(Duration::from_millis(150)).is_err(),
            "returned while held"
        );
        unlock_a().unwrap();
        rx.recv_timeout(Duration::from_secs(3))
            .expect("acquire after unlock")
            .unwrap();
        waiter.join().unwrap();

        // The canceled waits left nothing held.
        require_lock_free(&a);
    }
}

fn require_lock_free(d: &SqliteDriver) {
    let (ctx, _h) = bg().with_timeout(Duration::from_secs(2));
    let unlock = d.as_locker().unwrap().lock(&ctx).expect("lock still held");
    unlock().unwrap();
}

#[test]
fn lock_with_a_canceled_context_holds_nothing() {
    let dir = tempfile::tempdir().unwrap();
    let d = SqliteDriver::new("/x/app.db")
        .with_name("appdb")
        .with_data_dir(dir.path());
    let (ctx, cancel) = bg().with_cancel();
    cancel.cancel();
    let err = d.as_locker().unwrap().lock(&ctx).err().expect("canceled");
    assert_eq!(
        err.downcast_ref::<ContextError>(),
        Some(&ContextError::Canceled)
    );
    require_lock_free(&d);
}

// ---- two real processes ----

const HELPER_ENV: &str = "SQLITE_DRIVER_RACE_DIR";

/// The second process of `two_processes_apply_each_migration_once`: this
/// test binary re-executed with the env var set. A no-op in a normal run.
#[test]
fn race_helper_migrate() {
    let Ok(dir) = std::env::var(HELPER_ENV) else {
        return;
    };
    let dir = PathBuf::from(dir);
    let db_path = dir.join("app.db");
    let data = dir.join("data");
    let mut m = Migrator::new("tool", "1.2.0").data_dir(&data);
    for v in ["1.1.0", "1.2.0"] {
        let db = db_path.clone();
        m.register(Migration::new(v, "appdb", move |_| {
            thread::sleep(Duration::from_millis(200)); // a window for the other process
            let c = Connection::open(&db)?;
            c.busy_timeout(Duration::from_secs(5))?;
            c.execute(
                "INSERT INTO applied (version, pid) VALUES (?1, ?2)",
                (v, std::process::id()),
            )?;
            Ok(())
        }));
    }
    m.add_driver(
        SqliteDriver::new(&db_path)
            .with_name("appdb")
            .with_data_dir(&data),
    );
    m.run(&Context::background()).unwrap();
}

/// Two processes start the same migrations on one file at once: each
/// migration is applied once, by whichever process got the lock.
#[test]
fn two_processes_apply_each_migration_once() {
    if std::env::var(HELPER_ENV).is_ok() {
        return; // the helper process runs `race_helper_migrate` only
    }
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("app.db");
    let c = open_wal(&db_path);
    c.execute("CREATE TABLE applied (version TEXT, pid INTEGER)", [])
        .unwrap();
    drop(c);

    let exe = std::env::current_exe().unwrap();
    let children: Vec<_> = (0..2)
        .map(|_| {
            Command::new(&exe)
                .args([
                    "--exact",
                    "race_helper_migrate",
                    "--test-threads=1",
                    "--nocapture",
                ])
                .env(HELPER_ENV, dir.path())
                .output_child()
        })
        .collect();
    for (i, child) in children.into_iter().enumerate() {
        let out = child.wait_with_output().unwrap();
        assert!(
            out.status.success(),
            "helper {i}:\n{}{}",
            String::from_utf8_lossy(&out.stdout),
            String::from_utf8_lossy(&out.stderr)
        );
        assert!(
            String::from_utf8_lossy(&out.stdout).contains("1 passed"),
            "helper {i} did not run"
        );
    }

    let c = Connection::open(&db_path).unwrap();
    let mut s = c
        .prepare("SELECT version FROM applied ORDER BY version")
        .unwrap();
    let got: Vec<String> = s
        .query_map([], |r| r.get(0))
        .unwrap()
        .map(Result::unwrap)
        .collect();
    assert_eq!(got, vec!["1.1.0", "1.2.0"], "each migration once");
    assert_eq!(
        read_version_file_in(&dir.path().join("data"), "appdb").unwrap(),
        "1.2.0"
    );
}

trait SpawnPiped {
    fn output_child(&mut self) -> std::process::Child;
}

impl SpawnPiped for Command {
    fn output_child(&mut self) -> std::process::Child {
        self.stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .spawn()
            .unwrap()
    }
}

// ---- end to end through the Migrator ----

/// A database at 1.0.0 (row 1, in the WAL) with an isolated data dir.
struct RunEnv {
    _tmp: tempfile::TempDir,
    db_path: PathBuf,
    data_dir: PathBuf,
    conn: SharedConnection,
}

impl RunEnv {
    fn new() -> RunEnv {
        let tmp = tempfile::tempdir().unwrap();
        let db_path = tmp.path().join("app.db");
        let data_dir = tmp.path().join("data");
        let conn = open_wal(&db_path);
        insert(&conn, &[1]);
        fs::create_dir_all(data_dir.join("appdb")).unwrap();
        fs::write(data_dir.join("appdb/version"), "1.0.0").unwrap();
        RunEnv {
            _tmp: tmp,
            db_path,
            data_dir,
            conn: shared(conn),
        }
    }

    fn version(&self) -> String {
        read_version_file_in(&self.data_dir, "appdb").unwrap()
    }

    fn driver(&self, with_conn: bool) -> SqliteDriver {
        let d = SqliteDriver::new(&self.db_path)
            .with_name("appdb")
            .with_data_dir(&self.data_dir);
        if with_conn {
            d.with_connection(self.conn.clone())
        } else {
            d
        }
    }

    /// 1.1.0 inserts row 2; 1.2.0 runs `before_fail` and fails.
    fn migrator(
        &self,
        with_conn: bool,
        with_down: bool,
        manual: bool,
        before_fail: impl Fn() + Send + Sync + 'static,
    ) -> Migrator {
        let mut m = Migrator::new("tool", "1.2.0").data_dir(&self.data_dir);
        if manual {
            m = m.manual_rollback();
        }
        let (db, conn) = (self.db_path.clone(), self.conn.clone());
        let up_conn = conn.clone();
        let mut up11 = Migration::new("1.1.0", "appdb", move |_| {
            if with_conn {
                insert(&up_conn.lock().unwrap(), &[2]);
            } else {
                insert(&Connection::open(&db)?, &[2]);
            }
            Ok(())
        });
        if with_down {
            up11 = up11.with_down(move |_| {
                conn.lock()
                    .unwrap()
                    .execute("DELETE FROM t WHERE x = 2", [])?;
                Ok(())
            });
        }
        m.register(up11);
        m.register(Migration::new("1.2.0", "appdb", move |_| {
            before_fail();
            Err::<(), BoxError>("simulated failure".into())
        }));
        m.add_driver(self.driver(with_conn));
        m
    }
}

/// Path only: a failed run restores the file; the lock sits beside the
/// version file and is free afterwards.
#[test]
fn run_path_only_failure_restores_the_file() {
    let e = RunEnv::new();
    // The caller's own connection goes away before the restore (the
    // precondition: no open handle on the file).
    let held = e.conn.clone();
    let mut m = e.migrator(false, false, false, move || close_caller(&held));
    let err = m.run(&bg()).unwrap_err();
    assert!(err.to_string().contains("simulated failure"), "{err}");
    assert!(!err.is_not_restored(), "{err}");

    assert_eq!(rows(&e.db_path), vec![1], "restored to the pre-run state");
    assert_eq!(e.version(), "1.0.0");
    assert!(e.data_dir.join("appdb/.lock").exists());
    require_lock_free(&e.driver(false));
}

/// Closes the caller's connection on the file (the shared slot keeps an
/// in-memory one), as a path-only restore requires.
fn close_caller(c: &SharedConnection) {
    *c.lock().unwrap() = Connection::open_in_memory().unwrap();
}

/// With a connection there is no restore: a failed `up` ends the run not
/// restored (a `down` does not matter: a failed `up` is never downed) and
/// the backup stays for manual recovery.
#[test]
fn run_with_connection_failed_up_is_not_restored() {
    for with_down in [false, true] {
        let e = RunEnv::new();
        let err = e
            .migrator(true, with_down, false, || {})
            .run(&bg())
            .unwrap_err();
        assert!(err.is_not_restored(), "{err}");
        assert!(err.is_backup_unsupported(), "{err}");
        assert!(err.to_string().contains("simulated failure"), "{err}");

        assert_eq!(e.version(), "1.1.0", "stays at the last step applied");
        assert_eq!(
            rows(&e.db_path),
            vec![1, 2],
            "the earlier step is not downed"
        );
        assert_eq!(
            rows(&e.data_dir.join("backups/appdb/pre-1.1.0/app.db")),
            vec![1]
        );
        insert(&e.conn.lock().unwrap(), &[9]); // the caller's connection is open
        require_lock_free(&e.driver(true));
    }
}

/// With a connection a `down` still rolls a run back: here a failed final
/// verify, after both `up`s completed.
#[test]
fn run_with_connection_down_rolls_back_a_failed_final_verify() {
    let e = RunEnv::new();
    let mut m = Migrator::new("tool", "1.2.0")
        .data_dir(&e.data_dir)
        .verify(|_, _, _| Err("simulated failure".into()));
    for (v, x) in [("1.1.0", 2), ("1.2.0", 3)] {
        let (up, down) = (e.conn.clone(), e.conn.clone());
        m.register(
            Migration::new(v, "appdb", move |_| {
                insert(&up.lock().unwrap(), &[x]);
                Ok(())
            })
            .with_down(move |_| {
                down.lock()
                    .unwrap()
                    .execute("DELETE FROM t WHERE x = ?1", [x])?;
                Ok(())
            }),
        );
    }
    m.add_driver(e.driver(true));
    let err = m.run(&bg()).unwrap_err();
    assert!(err.to_string().contains("simulated failure"), "{err}");
    assert!(!err.is_not_restored(), "{err}");
    assert_eq!(rows(&e.db_path), vec![1]);
    assert_eq!(e.version(), "1.0.0");
    insert(&e.conn.lock().unwrap(), &[9]);
}

fn columns(path: &Path) -> Vec<String> {
    let c = Connection::open(path).unwrap();
    let mut s = c
        .prepare("SELECT name FROM pragma_table_info('t') ORDER BY cid")
        .unwrap();
    let r = s.query_map([], |r| r.get(0)).unwrap();
    r.map(Result::unwrap).collect()
}

/// An `up` that adds a column on a connection of its own, then fails: a
/// partial change.
fn add_column_then_fail(
    db: PathBuf,
    col: &'static str,
) -> impl Fn(&Context) -> Result<(), BoxError> {
    move |_| {
        let c = Connection::open(&db)?;
        c.execute(&format!("ALTER TABLE t ADD COLUMN {col} TEXT"), [])?;
        Err("boom".into())
    }
}

/// The `reverted` methods a run published.
type Seen = Arc<Mutex<Vec<String>>>;

/// A publisher recording the `method` of each `reverted` event.
fn reverted_methods() -> (Seen, impl Fn(&Context, &Event) + Send + Sync) {
    let seen = Seen::default();
    let s = seen.clone();
    (seen, move |_: &Context, e: &Event| {
        if e.topic.as_str().ends_with(".reverted") {
            let m = e.payload["method"].as_str().unwrap_or("").to_string();
            s.lock().unwrap().push(m);
        }
    })
}

/// The backup directories of `appdb`, directories only (the version
/// sidecar `pre-<v>.version` is a file beside them).
fn backup_dirs(e: &RunEnv) -> Vec<String> {
    let mut v: Vec<String> = fs::read_dir(e.data_dir.join("backups/appdb"))
        .unwrap()
        .map(Result::unwrap)
        .filter(|d| d.path().is_dir())
        .map(|d| d.file_name().to_string_lossy().into_owned())
        .collect();
    v.sort();
    v
}

/// Path only, the first `up` adds a column and fails: the run restores the
/// backup it took, so the column is gone and the version stays the
/// pre-run one; the backup directory stays.
#[test]
fn failed_first_up_restores_the_file() {
    let e = RunEnv::new();
    close_caller(&e.conn); // precondition: no open handle on the file
    let (seen, publisher) = reverted_methods();
    let mut m = Migrator::new("tool", "1.1.0")
        .data_dir(&e.data_dir)
        .publisher(publisher);
    m.register(Migration::new(
        "1.1.0",
        "appdb",
        add_column_then_fail(e.db_path.clone(), "note"),
    ));
    m.add_driver(e.driver(false));

    let err = m.run(&bg()).unwrap_err();
    assert!(err.to_string().contains("up 1.1.0 failed: boom"), "{err}");
    assert!(!err.is_not_restored(), "{err}");

    assert!(!columns(&e.db_path).contains(&"note".to_string()));
    assert_eq!(rows(&e.db_path), vec![1]);
    assert_eq!(e.version(), "1.0.0");
    assert_eq!(backup_dirs(&e), ["pre-1.1.0"], "the backup stays");
    assert_eq!(*seen.lock().unwrap(), ["restore"]);
    require_lock_free(&e.driver(false));
}

/// Path only, a later `up` adds a column and fails: no `down` runs, the
/// backup is restored and the earlier step is undone with the rest.
#[test]
fn failed_later_up_restores_the_file_without_down() {
    let e = RunEnv::new();
    close_caller(&e.conn);
    let downs = Arc::new(Mutex::new(0));
    let (d, db) = (downs.clone(), e.db_path.clone());
    let (seen, publisher) = reverted_methods();
    let mut m = Migrator::new("tool", "1.2.0")
        .data_dir(&e.data_dir)
        .publisher(publisher);
    m.register(
        Migration::new("1.1.0", "appdb", move |_| {
            insert(&Connection::open(&db)?, &[2]);
            Ok(())
        })
        .with_down(move |_| {
            *d.lock().unwrap() += 1;
            Ok(())
        }),
    );
    m.register(Migration::new(
        "1.2.0",
        "appdb",
        add_column_then_fail(e.db_path.clone(), "note"),
    ));
    m.add_driver(e.driver(false));

    let err = m.run(&bg()).unwrap_err();
    assert!(err.to_string().contains("up 1.2.0 failed: boom"), "{err}");
    assert!(!err.is_not_restored(), "{err}");

    assert_eq!(*downs.lock().unwrap(), 0, "no down runs");
    assert!(!columns(&e.db_path).contains(&"note".to_string()));
    assert_eq!(rows(&e.db_path), vec![1]);
    assert_eq!(e.version(), "1.0.0");
    assert_eq!(*seen.lock().unwrap(), ["restore"]);
}

/// Manual rollback, path only: `rollback_latest` restores the backup and
/// records the version the run started at.
#[test]
fn rollback_latest_path_only() {
    let e = RunEnv::new();
    let mut m = e.migrator(false, false, true, || {});
    assert!(m
        .run(&bg())
        .unwrap_err()
        .to_string()
        .contains("simulated failure"));
    assert_eq!(e.version(), "1.1.0");
    assert_eq!(rows(&e.db_path), vec![1, 2]);

    close_caller(&e.conn); // precondition: no connection on the file
    m.rollback_latest().unwrap();
    assert_eq!(rows(&e.db_path), vec![1]);
    assert_eq!(e.version(), "1.0.0");
    for p in sidecars(&e.db_path) {
        assert!(!p.exists(), "{}", p.display());
    }
    require_lock_free(&e.driver(false));
}

/// Manual rollback with a connection: nothing to restore with, reported
/// as not restored; the version stays and the connection stays open.
#[test]
fn rollback_latest_with_connection() {
    let e = RunEnv::new();
    let mut m = e.migrator(true, false, true, || {});
    assert!(m.run(&bg()).is_err());
    let err = m.rollback_latest().unwrap_err();
    assert!(
        err.is_not_restored() && err.is_backup_unsupported(),
        "{err}"
    );
    assert_eq!(e.version(), "1.1.0");
    assert_eq!(rows(&e.db_path), vec![1, 2]);
    insert(&e.conn.lock().unwrap(), &[9]);
    require_lock_free(&e.driver(true));
}

/// A successful run in connection mode leaves a backup with the rows that
/// were still in the WAL.
#[test]
fn run_with_connection_backup_has_wal_rows() {
    let e = RunEnv::new();
    let conn = e.conn.clone();
    let mut m = Migrator::new("tool", "1.1.0").data_dir(&e.data_dir);
    m.register(Migration::new("1.1.0", "appdb", move |_| {
        insert(&conn.lock().unwrap(), &[2]);
        Ok(())
    }));
    m.add_driver(e.driver(true));
    m.run(&bg()).unwrap();
    assert_eq!(
        rows(&e.data_dir.join("backups/appdb/pre-1.1.0/app.db")),
        vec![1]
    );
    assert_eq!(rows(&e.db_path), vec![1, 2]);
    assert_eq!(e.version(), "1.1.0");
}
