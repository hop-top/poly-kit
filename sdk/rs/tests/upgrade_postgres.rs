//! The Postgres upgrade driver against a real server. Ports of the Go
//! `upgrade/driver/postgres` integration tests: round trip through the
//! Migrator, the version table, two runners racing one database, raw-key
//! interop with another session, a cancelled wait, a failed `up` (not
//! restored), a failed unlock and the table-name rule.
//!
//! Every test is `#[ignore]`d, so `cargo test --all-features` (`make
//! test-rs`) needs no database. `make test-rs-upgrade-integration` starts a
//! throwaway Postgres 16, exports `KIT_UPGRADE_PG_DSN` (a `postgres://` URL
//! with a superuser) and runs them with `--ignored`; without the variable
//! they fail, they do not skip. Each test works in a database of its own.

#![cfg(feature = "upgrade-postgres")]

use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc;
use std::sync::{Arc, Barrier, Mutex};
use std::thread;
use std::time::{Duration, Instant};

use hop_top_kit::bus::{Context, ContextError, Event};
use hop_top_kit::upgrade::postgres::{PostgresDriver, SharedClient};
use hop_top_kit::upgrade::{
    is_backup_unsupported, BoxError, Locker, MigrateError, Migration, Migrator, SchemaDriver,
};
use postgres::{Client, Config, NoTls};

static NEXT_DB: AtomicUsize = AtomicUsize::new(0);
/// `CREATE DATABASE` copies `template1`, which refuses a second copier.
static CREATE: Mutex<()> = Mutex::new(());

fn admin_config() -> Config {
    std::env::var("KIT_UPGRADE_PG_DSN")
        .expect("KIT_UPGRADE_PG_DSN is not set: run `make test-rs-upgrade-integration`")
        .parse()
        .expect("KIT_UPGRADE_PG_DSN is not a postgres:// URL")
}

/// A database of this test's own, dropped at the end.
struct TestDb {
    name: String,
    cfg: Config,
    roles: Mutex<Vec<String>>,
}

impl TestDb {
    fn new() -> TestDb {
        let admin = admin_config();
        let name = format!(
            "kit_rs_{}_{}",
            std::process::id(),
            NEXT_DB.fetch_add(1, Ordering::SeqCst)
        );
        {
            let _g = CREATE.lock().unwrap_or_else(|p| p.into_inner());
            let mut c = admin.connect(NoTls).expect("connect admin");
            c.batch_execute(&format!("CREATE DATABASE {name}")).unwrap();
        }
        let mut cfg = admin;
        cfg.dbname(&name);
        TestDb {
            name,
            cfg,
            roles: Mutex::new(Vec::new()),
        }
    }

    fn connect(&self) -> Client {
        self.cfg.connect(NoTls).expect("connect")
    }

    fn connector(&self) -> impl Fn() -> Result<Client, BoxError> + Send + Sync + 'static {
        let cfg = self.cfg.clone();
        move || Ok(cfg.connect(NoTls)?)
    }

    fn driver(&self, table: &str) -> PostgresDriver {
        let client: SharedClient = Arc::new(Mutex::new(self.connect()));
        PostgresDriver::new(client, table, self.connector()).expect("new driver")
    }

    fn exec(&self, sql: &str) {
        self.connect().batch_execute(sql).unwrap();
    }

    /// Server sessions on this database.
    fn sessions(&self) -> i64 {
        admin_config()
            .connect(NoTls)
            .unwrap()
            .query_one(
                "SELECT count(*) FROM pg_stat_activity \
                 WHERE datname = $1 AND backend_type = 'client backend'",
                &[&self.name],
            )
            .unwrap()
            .get(0)
    }

    /// Advisory locks granted or waited for on this database.
    fn advisory_locks(&self) -> i64 {
        admin_config()
            .connect(NoTls)
            .unwrap()
            .query_one(
                "SELECT count(*) FROM pg_locks l JOIN pg_database d ON d.oid = l.database \
                 WHERE l.locktype = 'advisory' AND d.datname = $1",
                &[&self.name],
            )
            .unwrap()
            .get(0)
    }
}

impl Drop for TestDb {
    fn drop(&mut self) {
        if let Ok(mut c) = admin_config().connect(NoTls) {
            let _ = c.batch_execute(&format!(
                "DROP DATABASE IF EXISTS {} WITH (FORCE)",
                self.name
            ));
            for r in self.roles.lock().unwrap().iter() {
                let _ = c.batch_execute(&format!("DROP ROLE IF EXISTS {r}"));
            }
        }
    }
}

fn eventually(what: &str, f: impl Fn() -> bool) {
    let end = Instant::now() + Duration::from_secs(10);
    while !f() {
        assert!(Instant::now() < end, "timed out waiting for {what}");
        thread::sleep(Duration::from_millis(50));
    }
}

/// `SELECT` the raw lock statement the way another tool would spell it,
/// the key text written out.
fn raw_key(table: &str, schema: &str) -> String {
    format!("hashtextextended('kit-upgrade:{table}:{schema}', 0)")
}

fn raw_lock(c: &mut Client, table: &str, schema: &str) {
    c.batch_execute(&format!(
        "SELECT pg_advisory_lock({})",
        raw_key(table, schema)
    ))
    .unwrap();
}

fn raw_unlock(c: &mut Client, table: &str, schema: &str) {
    c.batch_execute(&format!(
        "SELECT pg_advisory_unlock({})",
        raw_key(table, schema)
    ))
    .unwrap();
}

fn raw_try(c: &mut Client, table: &str, schema: &str) -> bool {
    c.query_one(
        &format!("SELECT pg_try_advisory_lock({})", raw_key(table, schema)),
        &[],
    )
    .unwrap()
    .get(0)
}

fn stored(db: &TestDb, table: &str, schema: &str) -> Option<String> {
    db.connect()
        .query_opt(
            &format!("SELECT version FROM {table} WHERE schema_name = $1"),
            &[&schema],
        )
        .unwrap()
        .map(|r| r.get(0))
}

/// A migration whose `up` records its version in `applied(v)` through a
/// connection of its own.
fn recording(db: &TestDb, version: &str, schema: &str, pause: Duration) -> Migration {
    let cfg = db.cfg.clone();
    let v = version.to_string();
    Migration::new(version, schema, move |_| {
        thread::sleep(pause);
        let mut c = cfg.connect(NoTls)?;
        c.execute("INSERT INTO applied (v) VALUES ($1)", &[&v])?;
        Ok(())
    })
}

/// Every file under `dir`.
fn walk(dir: &Path, out: &mut Vec<std::path::PathBuf>) {
    for e in std::fs::read_dir(dir).unwrap() {
        let p = e.unwrap().path();
        if p.is_dir() {
            walk(&p, out);
        } else {
            out.push(p);
        }
    }
}

fn applied(db: &TestDb) -> Vec<(String, i64)> {
    db.connect()
        .query("SELECT v, count(*) FROM applied GROUP BY v ORDER BY v", &[])
        .unwrap()
        .iter()
        .map(|r| (r.get(0), r.get(1)))
        .collect()
}

#[test]
#[ignore]
fn round_trip_through_migrator() {
    let db = TestDb::new();
    db.exec("CREATE TABLE applied (v TEXT)");
    let tmp = tempfile::tempdir().unwrap();
    let mut m = Migrator::new("pgtool", "0.2.0").data_dir(tmp.path());
    m.register(recording(&db, "0.1.0", "app", Duration::ZERO));
    m.register(recording(&db, "0.2.0", "app", Duration::ZERO));
    m.add_driver(db.driver("schema_versions").with_name("app"));

    m.run(&Context::background()).unwrap();
    assert_eq!(
        stored(&db, "schema_versions", "app").as_deref(),
        Some("0.2.0")
    );
    assert_eq!(
        db.driver("schema_versions")
            .with_name("app")
            .version()
            .unwrap(),
        "0.2.0"
    );
    let mut files = Vec::new();
    walk(tmp.path(), &mut files);
    assert!(files.is_empty(), "no version file, no backup: {files:?}");
    assert_eq!(db.advisory_locks(), 0, "the run unlocked");

    m.run(&Context::background()).unwrap(); // nothing pending
    assert_eq!(
        applied(&db),
        vec![("0.1.0".to_string(), 1), ("0.2.0".to_string(), 1)]
    );
}

#[test]
#[ignore]
fn version_table_and_upsert() {
    let db = TestDb::new();
    let mut a = db.driver("versions").with_name("a");
    let mut b = db.driver("versions").with_name("b");
    assert!(a.stores_version());
    assert_eq!(a.name(), "a");
    assert_eq!(a.version().unwrap(), "", "no row is the empty version");

    a.set_version("1.0.0").unwrap();
    a.set_version("1.1.0").unwrap();
    assert_eq!(a.version().unwrap(), "1.1.0");
    assert_eq!(b.version().unwrap(), "", "another name has its own row");
    b.set_version("2.0.0").unwrap();
    assert_eq!(a.version().unwrap(), "1.1.0");
    assert_eq!(b.version().unwrap(), "2.0.0");

    let n: i64 = db
        .connect()
        .query_one("SELECT count(*) FROM versions", &[])
        .unwrap()
        .get(0);
    assert_eq!(n, 2, "an upsert, not an insert per call");
    // The default schema name.
    assert_eq!(db.driver("versions").name(), "postgres");
    // A second driver over the same table keeps the rows.
    assert_eq!(
        db.driver("versions").with_name("a").version().unwrap(),
        "1.1.0"
    );
}

#[test]
#[ignore]
fn concurrent_table_creation() {
    let db = TestDb::new();
    let start = Barrier::new(8);
    thread::scope(|s| {
        for _ in 0..8 {
            s.spawn(|| {
                let c: SharedClient = Arc::new(Mutex::new(db.connect()));
                start.wait();
                PostgresDriver::new(c, "racy_versions", db.connector()).expect("new");
            });
        }
    });
}

#[test]
#[ignore]
fn racing_runs_apply_each_migration_once() {
    let db = TestDb::new();
    db.exec("CREATE TABLE applied (v TEXT)");
    let tmp = tempfile::tempdir().unwrap();
    let start = Barrier::new(2);
    thread::scope(|s| {
        let runs: Vec<_> = (0..2)
            .map(|_| {
                s.spawn(|| {
                    let mut m = Migrator::new("racetool", "0.3.0").data_dir(tmp.path());
                    for v in ["0.1.0", "0.2.0", "0.3.0"] {
                        m.register(recording(&db, v, "race", Duration::from_millis(150)));
                    }
                    m.add_driver(db.driver("schema_versions").with_name("race"));
                    start.wait();
                    m.run(&Context::background())
                })
            })
            .collect();
        for r in runs {
            r.join().unwrap().unwrap();
        }
    });
    assert_eq!(
        applied(&db),
        vec![
            ("0.1.0".to_string(), 1),
            ("0.2.0".to_string(), 1),
            ("0.3.0".to_string(), 1)
        ],
        "each migration exactly once across both runners"
    );
    assert_eq!(
        stored(&db, "schema_versions", "race").as_deref(),
        Some("0.3.0")
    );
    assert_eq!(db.advisory_locks(), 0);
}

#[test]
#[ignore]
fn raw_advisory_key_interop() {
    let db = TestDb::new();
    let drv = db.driver("schema_versions").with_name("app");
    let mut raw = db.connect();

    // Another session on the key text written out blocks the driver.
    raw_lock(&mut raw, "schema_versions", "app");
    let (tx, rx) = mpsc::channel();
    thread::scope(|s| {
        s.spawn(|| {
            let unlock = drv.lock(&Context::background()).unwrap();
            tx.send(()).unwrap();
            unlock().unwrap();
        });
        assert!(
            rx.recv_timeout(Duration::from_millis(500)).is_err(),
            "the driver took a key another session holds"
        );
        raw_unlock(&mut raw, "schema_versions", "app");
        rx.recv_timeout(Duration::from_secs(5))
            .expect("the driver acquires after the release");
    });

    // A different schema name or table is a different key.
    raw_lock(&mut raw, "schema_versions", "other");
    raw_lock(&mut raw, "other_table", "app");
    let unlock = drv.lock(&Context::background()).unwrap();
    // While the driver holds the key a raw try fails; afterwards it works.
    assert!(!raw_try(&mut raw, "schema_versions", "app"));
    unlock().unwrap();
    assert!(raw_try(&mut raw, "schema_versions", "app"));
}

#[test]
#[ignore]
fn cancelled_wait_holds_nothing() {
    let db = TestDb::new();
    let drv = db.driver("schema_versions").with_name("app");
    let mut raw = db.connect();
    raw_lock(&mut raw, "schema_versions", "app");
    let baseline = db.sessions(); // the driver's client and the raw session
    assert_eq!(db.advisory_locks(), 1);

    // A deadline.
    let (ctx, _keep) = Context::background().with_timeout(Duration::from_millis(300));
    let started = Instant::now();
    let err = drv.lock(&ctx).err().expect("a held key must time out");
    assert_eq!(
        err.downcast_ref::<ContextError>(),
        Some(&ContextError::DeadlineExceeded)
    );
    assert!(started.elapsed() < Duration::from_secs(5));

    // A cancel from another thread, through the Migrator.
    let (ctx, cancel) = Context::background().with_cancel();
    let mut m = Migrator::new("canceltool", "0.1.0");
    m.register(Migration::new("0.1.0", "app", |_| Ok(())));
    m.add_driver(db.driver("schema_versions").with_name("app"));
    let baseline = baseline + 1; // the Migrator's driver client stays open
    let err = thread::scope(|s| {
        s.spawn(|| {
            thread::sleep(Duration::from_millis(200));
            cancel.cancel();
        });
        m.run(&ctx).unwrap_err()
    });
    assert!(matches!(err, MigrateError::Lock { .. }), "{err}");
    assert!(err.to_string().contains("context canceled"), "{err}");

    // Nothing is left behind: no lock session, no granted lock but the
    // raw session's own; the key is free once that lets go.
    eventually("lock sessions to end", || db.sessions() == baseline);
    assert_eq!(db.advisory_locks(), 1, "only the raw session holds a lock");
    raw_unlock(&mut raw, "schema_versions", "app");
    assert_eq!(db.advisory_locks(), 0);
    assert!(raw_try(&mut db.connect(), "schema_versions", "app"));
}

#[test]
#[ignore]
fn connect_failure_fails_the_schema() {
    let db = TestDb::new();
    let client: SharedClient = Arc::new(Mutex::new(db.connect()));
    let drv = PostgresDriver::new(client, "schema_versions", || {
        Err::<Client, BoxError>("no route to host".into())
    })
    .unwrap()
    .with_name("app");
    let err = drv.lock(&Context::background()).err().unwrap();
    assert_eq!(err.to_string(), "postgres upgrade: lock: no route to host");
}

/// No backup, so a failed `up` ends not restored whether or not its
/// migrations have a `down`: none runs, the version stays at the last
/// step still applied and no `reverted` event is published.
#[test]
#[ignore]
fn failed_up_is_not_restored() {
    let db = TestDb::new();
    db.exec("CREATE TABLE applied (v TEXT)");
    for with_down in [false, true] {
        let tmp = tempfile::tempdir().unwrap();
        let downs = Arc::new(Mutex::new(0));
        let reverted = Arc::new(Mutex::new(0));
        let r = reverted.clone();
        let mut m = Migrator::new("failtool", "0.2.0")
            .data_dir(tmp.path())
            .publisher(move |_: &Context, e: &Event| {
                if e.topic.as_str().ends_with(".reverted") {
                    *r.lock().unwrap() += 1;
                }
            });
        let mut first = recording(&db, "0.1.0", "app", Duration::ZERO);
        let mut second = Migration::new("0.2.0", "app", |_| Err("boom".into()));
        if with_down {
            let (d1, d2) = (downs.clone(), downs.clone());
            first = first.with_down(move |_| {
                *d1.lock().unwrap() += 1;
                Ok(())
            });
            second = second.with_down(move |_| {
                *d2.lock().unwrap() += 1;
                Ok(())
            });
        }
        m.register(first);
        m.register(second);
        m.add_driver(db.driver("schema_versions").with_name("app"));

        let err = m.run(&Context::background()).unwrap_err();
        assert!(err.is_not_restored(), "{err}");
        assert!(err.is_backup_unsupported(), "{err}");
        assert!(err.to_string().contains("boom"), "{err}");
        assert_eq!(*downs.lock().unwrap(), 0, "no down runs");
        assert_eq!(*reverted.lock().unwrap(), 0, "nothing was reverted");
        assert_eq!(
            stored(&db, "schema_versions", "app").as_deref(),
            Some("0.1.0"),
            "the version stays at the last step still applied"
        );
        assert_eq!(db.advisory_locks(), 0);
        // Reset for the second pass: the schema is back at nothing.
        db.exec("DELETE FROM schema_versions");
    }
}

/// A `down` still rolls a run back on Postgres when the run fails after
/// its `up`s completed: here a failed final verify.
#[test]
#[ignore]
fn down_rolls_back_a_failed_final_verify_without_a_backup() {
    let db = TestDb::new();
    let tmp = tempfile::tempdir().unwrap();
    let mut m = Migrator::new("downtool", "0.2.0")
        .data_dir(tmp.path())
        .verify(|_, _, _| Err("boom".into()));
    m.register(Migration::new("0.1.0", "app", |_| Ok(())).with_down(|_| Ok(())));
    m.register(Migration::new("0.2.0", "app", |_| Ok(())).with_down(|_| Ok(())));
    m.add_driver(db.driver("schema_versions").with_name("app"));

    let err = m.run(&Context::background()).unwrap_err();
    assert!(!err.is_not_restored(), "{err}");
    assert!(err.to_string().contains("boom"), "{err}");
    assert_eq!(stored(&db, "schema_versions", "app").as_deref(), Some(""));
}

#[test]
#[ignore]
fn failed_unlock_ends_the_session() {
    let db = TestDb::new();
    let role = format!("kit_rs_lim_{}", db.name);
    db.roles.lock().unwrap().push(role.clone());
    // The lock session may take the key but not release it: the unlock
    // statement is refused while the session stays alive.
    db.exec(&format!(
        "CREATE ROLE {role} LOGIN PASSWORD 'lock-only'; \
         REVOKE EXECUTE ON FUNCTION pg_advisory_unlock(bigint) FROM PUBLIC"
    ));
    let mut lim = db.cfg.clone();
    lim.user(&role).password("lock-only");

    let client: SharedClient = Arc::new(Mutex::new(db.connect()));
    let drv = PostgresDriver::new(client, "schema_versions", move || Ok(lim.connect(NoTls)?))
        .unwrap()
        .with_name("app");
    let baseline = db.sessions();

    let unlock = drv.lock(&Context::background()).unwrap();
    assert_eq!(db.advisory_locks(), 1);
    assert_eq!(db.sessions(), baseline + 1);
    let err = unlock().unwrap_err().to_string();
    assert!(err.starts_with("postgres upgrade: unlock: "), "{err}");
    assert!(err.contains("permission denied"), "{err}");

    // The refused unlock must not leave the session holding the key.
    eventually("the lock session to end", || db.sessions() == baseline);
    assert_eq!(db.advisory_locks(), 0);
    assert!(raw_try(&mut db.connect(), "schema_versions", "app"));

    // Through the Migrator it is a warning, not a failure.
    let warnings = Arc::new(Mutex::new(Vec::<String>::new()));
    let w = warnings.clone();
    let tmp = tempfile::tempdir().unwrap();
    let mut m = Migrator::new("unlocktool", "0.1.0")
        .data_dir(tmp.path())
        .on_warning(move |s| w.lock().unwrap().push(s.to_string()));
    m.register(Migration::new("0.1.0", "app", |_| Ok(())));
    let mut lim = db.cfg.clone();
    lim.user(&role).password("lock-only");
    let client: SharedClient = Arc::new(Mutex::new(db.connect()));
    m.add_driver(
        PostgresDriver::new(client, "schema_versions", move || Ok(lim.connect(NoTls)?))
            .unwrap()
            .with_name("app"),
    );
    m.run(&Context::background()).unwrap();
    let got = warnings.lock().unwrap().clone();
    assert_eq!(got.len(), 1, "{got:?}");
    assert!(
        got[0].starts_with("warning: unlock app: postgres upgrade: unlock: "),
        "{got:?}"
    );
    assert_eq!(db.advisory_locks(), 0);
}

#[test]
#[ignore]
fn invalid_table_names_touch_nothing() {
    let db = TestDb::new();
    let long = "a".repeat(65);
    for name in ["", "1abc", "a-b", "a b", "t\n", "a;drop table x", &long] {
        let client: SharedClient = Arc::new(Mutex::new(db.connect()));
        let err = PostgresDriver::new(client, name, db.connector()).unwrap_err();
        assert_eq!(err.to_string(), format!("invalid table name: {name:?}"));
    }
    let tables: i64 = db
        .connect()
        .query_one(
            "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'",
            &[],
        )
        .unwrap()
        .get(0);
    assert_eq!(tables, 0, "validation runs before any I/O");

    // 64 characters is the limit and is fine.
    let max = format!("t{}", "x".repeat(63));
    db.driver(&max);
    db.driver("_underscore_9");
}

#[test]
#[ignore]
fn backup_and_restore_are_unsupported() {
    let db = TestDb::new();
    let mut d = db.driver("schema_versions");
    let e = d.backup(Path::new("/nonexistent")).unwrap_err();
    assert!(is_backup_unsupported(&*e));
    let e = d.restore(Path::new("/nonexistent")).unwrap_err();
    assert!(is_backup_unsupported(&*e));
    assert!(d.as_locker().is_some());
    assert!(d.stores_version());
}
