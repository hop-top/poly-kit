//! Runs the shared upgrade corpus in `contracts/upgrade-v1`. The vectors
//! are the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.

#![cfg(feature = "upgrade")]

use std::cmp::Ordering;
use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use hop_top_kit::upgrade::{
    compare_versions, pending, read_version_file_in, AppliedMigration, BoxError, Migration,
    Migrator, SchemaDriver,
};
use serde::Deserialize;

const FILES: &[&str] = &["migrator.json", "versions.json"];

fn corpus_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/upgrade-v1")
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CompareCase {
    name: String,
    a: String,
    b: String,
    cmp: i8,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MigrationRef {
    schema: String,
    version: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PendingCase {
    name: String,
    migrations: Vec<MigrationRef>,
    schema: String,
    from: String,
    to: String,
    pending: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Versions {
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    version: String,
    #[allow(dead_code)]
    description: String,
    compare: Vec<CompareCase>,
    pending: Vec<PendingCase>,
}

#[derive(Deserialize, Clone, Default)]
#[serde(deny_unknown_fields)]
struct DriverCase {
    name: String,
    version: String,
    #[serde(default)]
    backup_fail: String,
    #[serde(default)]
    restore_fail: String,
    #[serde(default)]
    version_fail: String,
    #[serde(default)]
    set_version_fail: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MigrationCase {
    schema: String,
    version: String,
    #[serde(default)]
    fail: String,
    #[serde(default)]
    down: bool,
    #[serde(default)]
    down_fail: String,
}

#[derive(Deserialize, Debug, PartialEq)]
#[serde(deny_unknown_fields)]
struct StatusCase {
    schema: String,
    current: String,
    target: String,
    pending: usize,
    error: Option<String>,
}

#[derive(Deserialize, Debug, PartialEq)]
#[serde(deny_unknown_fields)]
struct HistoryCase {
    schema: String,
    version: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Step {
    op: String,
    error: Option<String>,
    calls: Option<Vec<String>>,
    history: Option<Vec<HistoryCase>>,
    #[serde(default)]
    versions: BTreeMap<String, String>,
    #[serde(default)]
    backups: BTreeMap<String, Vec<String>>,
    status: Option<Vec<StatusCase>>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Scenario {
    name: String,
    target: String,
    #[serde(default)]
    manual_rollback: bool,
    #[serde(default)]
    retention: usize,
    drivers: Vec<DriverCase>,
    migrations: Vec<MigrationCase>,
    #[serde(default)]
    backups: BTreeMap<String, Vec<String>>,
    steps: Vec<Step>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MigratorFile {
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    version: String,
    #[allow(dead_code)]
    description: String,
    scenarios: Vec<Scenario>,
}

fn load<T: for<'de> Deserialize<'de>>(name: &str) -> T {
    let dir = corpus_dir();
    let mut found: Vec<String> = fs::read_dir(&dir)
        .unwrap()
        .filter_map(|e| {
            let n = e.unwrap().file_name().into_string().unwrap();
            n.ends_with(".json").then_some(n)
        })
        .collect();
    found.sort();
    assert_eq!(found, FILES, "corpus files without a runner");
    let raw = fs::read_to_string(dir.join(name)).unwrap();
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {name}: {e}"))
}

fn ordering(cmp: i8) -> Ordering {
    cmp.cmp(&0)
}

#[test]
fn conformance_compare() {
    let f: Versions = load("versions.json");
    assert_eq!(f.version, "v1");
    assert!(!f.compare.is_empty(), "compare section is empty");
    for c in &f.compare {
        assert_eq!(
            compare_versions(&c.a, &c.b),
            ordering(c.cmp),
            "{}: compare({:?}, {:?})",
            c.name,
            c.a,
            c.b
        );
        assert_eq!(
            compare_versions(&c.b, &c.a),
            ordering(-c.cmp),
            "{}: compare({:?}, {:?})",
            c.name,
            c.b,
            c.a
        );
    }
}

#[test]
fn conformance_pending() {
    let f: Versions = load("versions.json");
    assert!(!f.pending.is_empty(), "pending section is empty");
    for c in &f.pending {
        let migrations: Vec<Migration> = c
            .migrations
            .iter()
            .map(|m| Migration::new(&m.version, &m.schema, || Ok(())))
            .collect();
        let got: Vec<&str> = pending(&migrations, &c.schema, &c.from, &c.to)
            .into_iter()
            .map(|m| m.version.as_str())
            .collect();
        assert_eq!(got, c.pending, "{}", c.name);
    }
}

type Log = Arc<Mutex<Vec<String>>>;

fn fail_if(msg: &str) -> Result<(), BoxError> {
    if msg.is_empty() {
        Ok(())
    } else {
        Err(msg.into())
    }
}

fn base_name(p: &Path) -> String {
    p.file_name().unwrap().to_string_lossy().into_owned()
}

/// Logs every state-changing call into a log shared with the
/// scenario's migrations.
struct Recording {
    case: DriverCase,
    log: Log,
}

impl Recording {
    fn record(&self, entry: String) {
        self.log.lock().unwrap().push(entry);
    }
}

impl SchemaDriver for Recording {
    fn name(&self) -> &str {
        &self.case.name
    }

    fn version(&self) -> Result<String, BoxError> {
        fail_if(&self.case.version_fail)?;
        Ok(self.case.version.clone())
    }

    fn set_version(&mut self, version: &str) -> Result<(), BoxError> {
        self.record(format!("{} set_version {version}", self.case.name));
        fail_if(&self.case.set_version_fail)?;
        self.case.version = version.to_string();
        Ok(())
    }

    fn backup(&self, dest: &Path) -> Result<(), BoxError> {
        self.record(format!("{} backup {}", self.case.name, base_name(dest)));
        fail_if(&self.case.backup_fail)
    }

    fn restore(&mut self, src: &Path) -> Result<(), BoxError> {
        self.record(format!("{} restore {}", self.case.name, base_name(src)));
        fail_if(&self.case.restore_fail)
    }
}

fn list_backups(base: &Path, schema: &str) -> Vec<String> {
    let Ok(entries) = fs::read_dir(base.join("backups").join(schema)) else {
        return Vec::new();
    };
    let mut names: Vec<String> = entries
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .collect();
    names.sort();
    names
}

fn migration(case: &MigrationCase, log: &Log) -> Migration {
    let (schema, version, fail) = (case.schema.clone(), case.version.clone(), case.fail.clone());
    let up_log = log.clone();
    let mut m = Migration::new(&case.version, &case.schema, move || {
        up_log
            .lock()
            .unwrap()
            .push(format!("{schema} up {version}"));
        fail_if(&fail)
    });
    if case.down || !case.down_fail.is_empty() {
        let (schema, version, fail) = (
            case.schema.clone(),
            case.version.clone(),
            case.down_fail.clone(),
        );
        let down_log = log.clone();
        m = m.with_down(move || {
            down_log
                .lock()
                .unwrap()
                .push(format!("{schema} down {version}"));
            fail_if(&fail)
        });
    }
    m
}

fn run_scenario(sc: &Scenario) {
    let tmp = tempfile::tempdir().unwrap();
    let base = tmp.path().to_path_buf();
    for (schema, dirs) in &sc.backups {
        fs::create_dir_all(base.join("backups").join(schema)).unwrap();
        for d in dirs {
            fs::create_dir_all(base.join("backups").join(schema).join(d)).unwrap();
        }
    }

    let log: Log = Arc::default();
    let mut m = Migrator::new("conftool", &sc.target).data_dir(&base);
    if sc.manual_rollback {
        m = m.manual_rollback();
    }
    if sc.retention != 0 {
        m = m.backup_retention(sc.retention);
    }
    for mc in &sc.migrations {
        m.register(migration(mc, &log));
    }
    for dc in &sc.drivers {
        m.add_driver(Recording {
            case: dc.clone(),
            log: log.clone(),
        });
    }

    for (i, st) in sc.steps.iter().enumerate() {
        let ctx = format!("{}: step {i}", sc.name);
        log.lock().unwrap().clear();
        let err = match st.op.as_str() {
            "run" => m.run().err().map(|e| e.to_string()),
            "rollback_latest" => m.rollback_latest().err().map(|e| e.to_string()),
            "status" => {
                let got: Vec<StatusCase> = m
                    .status()
                    .into_iter()
                    .map(|s| StatusCase {
                        schema: s.schema,
                        current: s.current,
                        target: s.target,
                        pending: s.pending,
                        error: s.error.map(|e| e.to_string()),
                    })
                    .collect();
                let want = st.status.as_ref().expect("status op without status");
                assert_eq!(&got, want, "{ctx}: status");
                None
            }
            op => panic!("{ctx}: unknown op {op:?}"),
        };
        assert_eq!(err, st.error, "{ctx}: error");
        if let Some(want) = &st.calls {
            assert_eq!(&*log.lock().unwrap(), want, "{ctx}: calls");
        }
        if let Some(want) = &st.history {
            let got: Vec<HistoryCase> = m
                .history()
                .iter()
                .map(|AppliedMigration { schema, version }| HistoryCase {
                    schema: schema.clone(),
                    version: version.clone(),
                })
                .collect();
            assert_eq!(&got, want, "{ctx}: history");
        }
        for (schema, want) in &st.versions {
            let got = read_version_file_in(&base, schema).unwrap();
            assert_eq!(&got, want, "{ctx}: {schema} version file");
        }
        for (schema, want) in &st.backups {
            assert_eq!(
                &list_backups(&base, schema),
                want,
                "{ctx}: {schema} backups"
            );
        }
    }
}

#[test]
fn conformance_migrator() {
    let f: MigratorFile = load("migrator.json");
    assert_eq!(f.version, "v1");
    assert!(!f.scenarios.is_empty(), "scenarios section is empty");
    for sc in &f.scenarios {
        run_scenario(sc);
    }
}
