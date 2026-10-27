//! Ports of the Go `core/upgrade` migration tests (migrate, migration,
//! backup, e2e and the configfile driver), run against real files.
//! Cross-language vectors live in `upgrade_conformance.rs`.

#![cfg(feature = "upgrade")]

use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::{validate_topic, Context, Event, SharedBus};
use hop_top_kit::upgrade::configfile::{backup_file_name, ConfigFileDriver};
use hop_top_kit::upgrade::{
    default_data_dir, read_version_file_in, BoxError, MigrateError, Migration, MigrationTopics,
    Migrator, SchemaDriver, StepKind,
};

type Log = Arc<Mutex<Vec<String>>>;

fn bg() -> Context {
    Context::background()
}

/// In-memory driver, as Go's `mockDriver`.
#[derive(Default)]
struct Mock {
    name: String,
    version: String,
    log: Log,
}

impl Mock {
    fn new(name: &str, version: &str, log: &Log) -> Self {
        Mock {
            name: name.into(),
            version: version.into(),
            log: log.clone(),
        }
    }
}

impl SchemaDriver for Mock {
    fn name(&self) -> &str {
        &self.name
    }
    fn version(&self) -> Result<String, BoxError> {
        Ok(self.version.clone())
    }
    fn set_version(&mut self, version: &str) -> Result<(), BoxError> {
        self.version = version.into();
        Ok(())
    }
    fn backup(&self, dest: &Path) -> Result<(), BoxError> {
        self.log
            .lock()
            .unwrap()
            .push(format!("backup {}", dest.display()));
        Ok(())
    }
    fn restore(&mut self, _src: &Path) -> Result<(), BoxError> {
        self.log.lock().unwrap().push("restore".into());
        Ok(())
    }
}

fn step(log: &Log, entry: &'static str) -> impl Fn(&Context) -> Result<(), BoxError> + Send + Sync {
    let log = log.clone();
    move |_| {
        log.lock().unwrap().push(entry.into());
        Ok(())
    }
}

fn boom(_: &Context) -> Result<(), BoxError> {
    Err("boom".into())
}

fn entries(log: &Log) -> Vec<String> {
    log.lock().unwrap().clone()
}

#[test]
fn run_applies_pending_in_order_and_backs_up_first() {
    let tmp = tempfile::tempdir().unwrap();
    let log = Log::default();
    let mut m = Migrator::new("testapp", "1.2.0").data_dir(tmp.path());
    m.register(Migration::new("1.2.0", "testdb", step(&log, "up 1.2.0")));
    m.register(Migration::new("1.1.0", "testdb", step(&log, "up 1.1.0")));
    m.add_driver(Mock::new("testdb", "1.0.0", &log));

    m.run(&bg()).unwrap();

    let backup = tmp.path().join("backups/testdb/pre-1.1.0");
    assert_eq!(
        entries(&log),
        [
            format!("backup {}", backup.display()),
            "up 1.1.0".into(),
            "up 1.2.0".into()
        ]
    );
    assert!(backup.is_dir(), "backup dir is created before Backup");
    assert_eq!(read_version_file_in(tmp.path(), "testdb").unwrap(), "1.2.0");
    let h: Vec<_> = m.history().iter().map(|a| a.version.as_str()).collect();
    assert_eq!(h, ["1.1.0", "1.2.0"]);
}

#[test]
fn run_is_idempotent() {
    let tmp = tempfile::tempdir().unwrap();
    let log = Log::default();
    let mut m = Migrator::new("testapp", "1.1.0").data_dir(tmp.path());
    m.register(Migration::new("1.1.0", "testdb", step(&log, "up")));
    m.add_driver(Mock::new("testdb", "1.0.0", &log));

    m.run(&bg()).unwrap();
    m.run(&bg()).unwrap();
    m.run(&bg()).unwrap();

    let ups = entries(&log).iter().filter(|e| *e == "up").count();
    assert_eq!(ups, 1, "a migration runs once");
    assert_eq!(m.history().len(), 1);
}

#[test]
fn run_without_pending_does_not_back_up() {
    let tmp = tempfile::tempdir().unwrap();
    let log = Log::default();
    let mut m = Migrator::new("testapp", "2.0.0").data_dir(tmp.path());
    m.add_driver(Mock::new("testdb", "2.0.0", &log));
    m.run(&bg()).unwrap();
    assert!(entries(&log).is_empty());
    assert!(!tmp.path().join("backups").exists());
}

#[test]
fn failure_runs_down_of_applied_migrations() {
    let tmp = tempfile::tempdir().unwrap();
    let log = Log::default();
    let mut m = Migrator::new("testapp", "1.2.0").data_dir(tmp.path());
    m.register(
        Migration::new("1.1.0", "testdb", step(&log, "up 1.1.0"))
            .with_down(step(&log, "down 1.1.0")),
    );
    m.register(Migration::new("1.2.0", "testdb", boom));
    m.add_driver(Mock::new("testdb", "1.0.0", &log));

    let err = m.run(&bg()).unwrap_err();

    assert!(err.to_string().contains("boom"), "{err}");
    assert!(
        matches!(err, MigrateError::Step { kind: StepKind::Up, ref version, .. } if version == "1.2.0")
    );
    let log = entries(&log);
    assert_eq!(&log[1..], ["up 1.1.0", "down 1.1.0"]);
    assert_eq!(read_version_file_in(tmp.path(), "testdb").unwrap(), "");
}

#[test]
fn failure_without_down_restores_backup() {
    let tmp = tempfile::tempdir().unwrap();
    let log = Log::default();
    let mut m = Migrator::new("testapp", "1.2.0").data_dir(tmp.path());
    m.register(Migration::new("1.1.0", "testdb", |_| Ok(())));
    m.register(Migration::new("1.2.0", "testdb", |_| Err("fail".into())));
    m.add_driver(Mock::new("testdb", "1.0.0", &log));

    assert!(m.run(&bg()).is_err());
    assert!(entries(&log).contains(&"restore".to_string()));
}

#[test]
fn manual_mode_does_not_roll_back() {
    let tmp = tempfile::tempdir().unwrap();
    let log = Log::default();
    let mut m = Migrator::new("testapp", "1.1.0")
        .data_dir(tmp.path())
        .manual_rollback();
    m.register(Migration::new("1.1.0", "testdb", |_| Err("fail".into())));
    m.add_driver(Mock::new("testdb", "1.0.0", &log));

    assert!(m.run(&bg()).is_err());
    assert!(!entries(&log).contains(&"restore".to_string()));
    m.rollback_latest().unwrap();
    assert!(entries(&log).contains(&"restore".to_string()));
}

#[test]
fn rollback_latest_refused_in_auto_mode() {
    let mut m = Migrator::new("testapp", "1.0.0");
    assert!(matches!(
        m.rollback_latest(),
        Err(MigrateError::RollbackUnavailable)
    ));
}

#[test]
fn read_version_file_missing_is_empty_and_trims() {
    let tmp = tempfile::tempdir().unwrap();
    assert_eq!(read_version_file_in(tmp.path(), "testdb").unwrap(), "");
    fs::create_dir_all(tmp.path().join("testdb")).unwrap();
    fs::write(tmp.path().join("testdb/version"), "1.5.0\n").unwrap();
    assert_eq!(read_version_file_in(tmp.path(), "testdb").unwrap(), "1.5.0");
}

#[test]
fn default_data_dir_is_hop_tool_under_data_home() {
    assert!(default_data_dir("mytool").ends_with(Path::new("hop").join("mytool")));
}

#[test]
fn accessors_report_tool_and_trimmed_target() {
    let m = Migrator::new("mytool", "v1.2.0");
    assert_eq!(m.tool(), "mytool");
    assert_eq!(m.version(), "1.2.0");
}

#[test]
fn types_are_send_and_sync() {
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<Migrator>();
    assert_send_sync::<Migration>();
    assert_send_sync::<ConfigFileDriver>();
    assert_send_sync::<MigrateError>();
}

// ---- configfile driver ----------------------------------------------------

#[test]
fn configfile_name_defaults_to_config() {
    assert_eq!(ConfigFileDriver::new(["/tmp/config.yaml"]).name(), "config");
    assert_eq!(
        ConfigFileDriver::new(["/tmp/a"]).with_name("layout").name(),
        "layout"
    );
}

#[test]
fn configfile_backup_and_restore_round_trip() {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = tmp.path().join("config.yaml");
    fs::write(&cfg, "key: value").unwrap();
    let mut d = ConfigFileDriver::new([&cfg]);

    let backup = tmp.path().join("backup");
    d.backup(&backup).unwrap();
    let saved = fs::read_to_string(backup.join(backup_file_name(&cfg))).unwrap();
    assert_eq!(saved, "key: value");

    fs::write(&cfg, "key: changed").unwrap();
    d.restore(&backup).unwrap();
    assert_eq!(fs::read_to_string(&cfg).unwrap(), "key: value");
}

#[test]
fn configfile_backs_up_several_files_and_skips_missing() {
    let tmp = tempfile::tempdir().unwrap();
    let a = tmp.path().join("a.yaml");
    let b = tmp.path().join("b.json");
    let missing = tmp.path().join("missing.yaml");
    fs::write(&a, "a").unwrap();
    fs::write(&b, "b").unwrap();
    let d = ConfigFileDriver::new([&a, &b, &missing]);
    let backup = tmp.path().join("backup");
    d.backup(&backup).unwrap();
    assert_eq!(
        fs::read_to_string(backup.join(backup_file_name(&a))).unwrap(),
        "a"
    );
    assert_eq!(
        fs::read_to_string(backup.join(backup_file_name(&b))).unwrap(),
        "b"
    );
    assert!(!backup.join(backup_file_name(&missing)).exists());
}

#[test]
fn configfile_backup_name_is_collision_safe() {
    let a = backup_file_name(Path::new("/etc/foo/config.yaml"));
    let b = backup_file_name(Path::new("/etc/bar/config.yaml"));
    assert_ne!(a, b);
    assert!(!a.contains(std::path::MAIN_SEPARATOR));
    if cfg!(unix) {
        assert_eq!(a, "_etc_foo_config.yaml");
    }
}

#[test]
fn configfile_version_reads_the_version_file() {
    let tmp = tempfile::tempdir().unwrap();
    let d = ConfigFileDriver::new(["/nonexistent"]).with_data_dir(tmp.path());
    assert_eq!(d.version().unwrap(), "");
    fs::create_dir_all(tmp.path().join("config")).unwrap();
    fs::write(tmp.path().join("config/version"), "2.0.0").unwrap();
    assert_eq!(d.version().unwrap(), "2.0.0");
}

// ---- end to end on real files --------------------------------------------

fn write_doc(path: &PathBuf, body: &str) {
    fs::write(path, body).unwrap();
}

fn rewrite(
    path: &Path,
    from: &'static str,
    to: &'static str,
) -> impl Fn(&Context) -> Result<(), BoxError> + Send + Sync {
    let path = path.to_path_buf();
    move |_| {
        let s = fs::read_to_string(&path)?;
        fs::write(&path, s.replace(from, to))?;
        Ok(())
    }
}

fn file_migrator(data: &Path, doc: &Path, target: &str) -> Migrator {
    let mut m = Migrator::new("e2etool", target).data_dir(data);
    m.register(Migration::new(
        "1.1.0",
        "layout",
        rewrite(doc, "\"v\":1", "\"v\":2"),
    ));
    m.register(Migration::new(
        "1.2.0",
        "layout",
        rewrite(doc, "\"v\":2", "\"v\":3"),
    ));
    m.add_driver(
        ConfigFileDriver::new([doc])
            .with_name("layout")
            .with_data_dir(data),
    );
    m
}

#[test]
fn e2e_lifecycle_upgrades_once_then_extends() {
    let tmp = tempfile::tempdir().unwrap();
    let data = tmp.path().join("data");
    let doc = tmp.path().join("layout.json");
    write_doc(&doc, "{\"v\":1}");

    let mut m = file_migrator(&data, &doc, "1.2.0");
    m.run(&bg()).unwrap();
    assert_eq!(fs::read_to_string(&doc).unwrap(), "{\"v\":3}");
    assert_eq!(read_version_file_in(&data, "layout").unwrap(), "1.2.0");

    // A fresh process at the same version applies nothing.
    let mut again = file_migrator(&data, &doc, "1.2.0");
    again.run(&bg()).unwrap();
    assert!(again.history().is_empty());
    assert_eq!(fs::read_to_string(&doc).unwrap(), "{\"v\":3}");

    // A newer build adds one migration and applies only that.
    let mut next = file_migrator(&data, &doc, "1.3.0");
    next.register(Migration::new(
        "1.3.0",
        "layout",
        rewrite(&doc, "\"v\":3", "\"v\":4"),
    ));
    next.run(&bg()).unwrap();
    let h: Vec<_> = next.history().iter().map(|a| a.version.as_str()).collect();
    assert_eq!(h, ["1.3.0"]);
    assert_eq!(fs::read_to_string(&doc).unwrap(), "{\"v\":4}");
}

#[test]
fn e2e_first_time_user_runs_everything() {
    let tmp = tempfile::tempdir().unwrap();
    let data = tmp.path().join("data");
    let doc = tmp.path().join("layout.json");
    write_doc(&doc, "{\"v\":1}");
    let mut m = file_migrator(&data, &doc, "1.2.0");
    assert_eq!(m.status()[0].pending, 2);
    m.run(&bg()).unwrap();
    assert_eq!(m.history().len(), 2);
}

#[test]
fn e2e_multi_step_failure_restores_the_file() {
    let tmp = tempfile::tempdir().unwrap();
    let data = tmp.path().join("data");
    let doc = tmp.path().join("layout.json");
    write_doc(&doc, "{\"v\":1}");
    let mut m = file_migrator(&data, &doc, "1.3.0");
    m.register(Migration::new("1.3.0", "layout", |_| {
        Err("boom at 1.3.0".into())
    }));

    let err = m.run(&bg()).unwrap_err();

    assert_eq!(
        err.to_string(),
        "migrate layout: up 1.3.0 failed: boom at 1.3.0"
    );
    assert_eq!(
        fs::read_to_string(&doc).unwrap(),
        "{\"v\":1}",
        "old data kept"
    );
    assert_eq!(read_version_file_in(&data, "layout").unwrap(), "");
}

#[test]
fn e2e_manual_rollback_restores_the_file() {
    let tmp = tempfile::tempdir().unwrap();
    let data = tmp.path().join("data");
    let doc = tmp.path().join("layout.json");
    write_doc(&doc, "{\"v\":1}");
    let mut m = Migrator::new("manualtool", "1.1.0")
        .data_dir(&data)
        .manual_rollback();
    let half = rewrite(&doc, "\"v\":1", "\"v\":2");
    m.register(Migration::new("1.1.0", "layout", move |ctx: &Context| {
        half(ctx)?;
        Err("simulated failure after change".into())
    }));
    m.add_driver(
        ConfigFileDriver::new([&doc])
            .with_name("layout")
            .with_data_dir(&data),
    );

    assert!(m.run(&bg()).is_err());
    assert_eq!(
        fs::read_to_string(&doc).unwrap(),
        "{\"v\":2}",
        "manual mode leaves it"
    );
    m.rollback_latest().unwrap();
    assert_eq!(fs::read_to_string(&doc).unwrap(), "{\"v\":1}");
    assert_eq!(read_version_file_in(&data, "layout").unwrap(), "");
}

#[test]
fn e2e_prune_keeps_retention_backups() {
    let tmp = tempfile::tempdir().unwrap();
    let data = tmp.path().join("data");
    for v in [
        "pre-1.0.0",
        "pre-1.1.0",
        "pre-1.2.0",
        "pre-1.3.0",
        "pre-2.0.0",
    ] {
        fs::create_dir_all(data.join("backups/db").join(v)).unwrap();
    }
    let log = Log::default();
    let mut m = Migrator::new("myapp", "3.0.0")
        .data_dir(&data)
        .backup_retention(3);
    m.register(Migration::new("3.0.0", "db", |_| Ok(())));
    m.add_driver(Mock::new("db", "2.0.0", &log));
    m.run(&bg()).unwrap();
    let mut names: Vec<_> = fs::read_dir(data.join("backups/db"))
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .collect();
    names.sort();
    assert_eq!(names, ["pre-1.3.0", "pre-2.0.0", "pre-3.0.0"]);
}

// ---- verify, cancellation, events, warnings -------------------------------

#[test]
fn verify_and_cancel_have_their_own_kinds() {
    let tmp = tempfile::tempdir().unwrap();
    let log = Log::default();
    let mut m = Migrator::new("k", "1.1.0").data_dir(tmp.path());
    m.register(Migration::new("1.1.0", "db", |_| Ok(())).with_verify(|_| Err("bad".into())));
    m.add_driver(Mock::new("db", "1.0.0", &log));
    let err = m.run(&bg()).unwrap_err();
    assert!(err.is_verify() && !err.is_canceled(), "{err}");

    let (ctx, cancel) = bg().with_cancel();
    cancel.cancel();
    let mut m = Migrator::new("k", "1.1.0").data_dir(tmp.path().join("2"));
    m.register(Migration::new("1.1.0", "db", |_| Ok(())));
    m.add_driver(Mock::new("db", "1.0.0", &log));
    let err = m.run(&ctx).unwrap_err();
    assert!(err.is_canceled() && !err.is_verify(), "{err}");
    assert_eq!(
        err.to_string(),
        "migrate db: up 1.1.0 canceled: context canceled"
    );

    let mut m = Migrator::new("k", "1.1.0")
        .data_dir(tmp.path().join("3"))
        .verify(|_, _, _| Err("x".into()));
    m.register(Migration::new("1.1.0", "db", |_| Ok(())));
    m.add_driver(Mock::new("db", "1.0.0", &log));
    let err = m.run(&bg()).unwrap_err();
    assert_eq!(err.step_kind(), Some(StepKind::FinalVerify));
}

#[test]
fn steps_see_the_run_context() {
    let tmp = tempfile::tempdir().unwrap();
    #[derive(Clone)]
    struct Marker(&'static str);
    let seen = Arc::new(Mutex::new(None));
    let s = seen.clone();
    let mut m = Migrator::new("k", "1.1.0").data_dir(tmp.path());
    m.register(Migration::new("1.1.0", "db", move |ctx| {
        *s.lock().unwrap() = ctx.value::<Marker>().map(|m| m.0);
        Ok(())
    }));
    m.add_driver(Mock::new("db", "1.0.0", &Log::default()));
    m.run(&bg().with_value(Marker("mine"))).unwrap();
    assert_eq!(*seen.lock().unwrap(), Some("mine"));
}

#[test]
fn default_topics_validate_and_prefix_rebrands() {
    let d = MigrationTopics::default();
    for t in [
        &d.run_started,
        &d.run_completed,
        &d.run_failed,
        &d.started,
        &d.applied,
        &d.verified,
        &d.failed,
        &d.reverted,
    ] {
        validate_topic(t).unwrap();
    }
    assert_eq!(d.applied.as_str(), "kit.core.migration.applied");
    let p = MigrationTopics::from_prefix("myapp.core.migration");
    assert_eq!(p.run_started.as_str(), "myapp.core.migration.run_started");
    assert!(std::panic::catch_unwind(|| MigrationTopics::from_prefix("too.short")).is_err());
}

#[test]
fn events_reach_a_kit_bus_in_order() {
    let tmp = tempfile::tempdir().unwrap();
    let bus = SharedBus::new(hop_top_kit::bus::Bus::new());
    let got = Arc::new(Mutex::new(Vec::new()));
    let g = got.clone();
    bus.subscribe("kit.core.migration.*", move |e: &Event| {
        g.lock()
            .unwrap()
            .push((e.topic.as_str().to_string(), e.source.clone()));
        Ok(())
    });
    let mut m = Migrator::new("k", "1.1.0")
        .data_dir(tmp.path())
        .publisher(bus.clone());
    m.register(Migration::new("1.1.0", "db", |_| Ok(())));
    m.add_driver(Mock::new("db", "1.0.0", &Log::default()));
    m.run(&bg()).unwrap();
    let topics: Vec<String> = got.lock().unwrap().iter().map(|(t, _)| t.clone()).collect();
    assert_eq!(
        topics,
        [
            "kit.core.migration.run_started",
            "kit.core.migration.started",
            "kit.core.migration.applied",
            "kit.core.migration.run_completed"
        ]
    );
    assert!(got.lock().unwrap().iter().all(|(_, s)| s == "core.upgrade"));
}

#[cfg(unix)]
#[test]
fn prune_failure_goes_to_the_warning_hook() {
    use std::os::unix::fs::PermissionsExt;
    let tmp = tempfile::tempdir().unwrap();
    let locked = tmp.path().join("backups/db/pre-0.1.0/locked");
    fs::create_dir_all(&locked).unwrap();
    fs::write(locked.join("f"), "").unwrap();
    fs::set_permissions(&locked, fs::Permissions::from_mode(0o500)).unwrap();
    let warnings = Arc::new(Mutex::new(Vec::new()));
    let w = warnings.clone();
    let mut m = Migrator::new("k", "1.1.0")
        .data_dir(tmp.path())
        .backup_retention(1)
        .on_warning(move |s| w.lock().unwrap().push(s.to_string()));
    m.register(Migration::new("1.1.0", "db", |_| Ok(())));
    m.add_driver(Mock::new("db", "1.0.0", &Log::default()));
    let result = m.run(&bg());
    fs::set_permissions(&locked, fs::Permissions::from_mode(0o750)).unwrap();
    result.unwrap();
    let w = warnings.lock().unwrap();
    assert_eq!(w.len(), 1);
    assert!(
        w[0].starts_with("warning: prune old backups for db: "),
        "{}",
        w[0]
    );
}
