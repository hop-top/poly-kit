//! Runs the shared upgrade corpus in `contracts/upgrade-v1`. The vectors
//! are the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.

#![cfg(all(feature = "upgrade", unix))]

use std::cmp::Ordering;
use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::{Context, Event};
use hop_top_kit::upgrade::{
    compare_versions, migrate_document, migrate_value, pending, read_version_file_in,
    AppliedMigration, BoxError, DocApplied, DocErrorKind, DocOptions, DocReport, DocStep,
    Migration, Migrator, SchemaDriver,
};
use serde::Deserialize;
use serde_json::Value;

const FILES: &[&str] = &["document.json", "migrator.json", "versions.json"];

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
    #[serde(default)]
    verify: bool,
    #[serde(default)]
    verify_fail: String,
    #[serde(default)]
    cancel: bool,
}

#[derive(Deserialize, Debug, PartialEq)]
#[serde(deny_unknown_fields)]
struct EventCase {
    topic: String,
    payload: Value,
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
    #[serde(default)]
    canceled: bool,
    events: Option<Vec<EventCase>>,
    warnings: Option<Vec<String>>,
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
    #[serde(default)]
    locked_backups: BTreeMap<String, Vec<String>>,
    #[serde(default)]
    final_verify: bool,
    #[serde(default)]
    final_verify_fail: String,
    #[serde(default)]
    data_dir: bool,
    /// Rust has no default registry: `own` is what every Rust migrator
    /// is, and `default_migrations` are registered nowhere it can see.
    #[serde(default)]
    #[allow(dead_code)]
    registry: String,
    #[serde(default)]
    #[allow(dead_code)]
    default_migrations: Vec<MigrationCase>,
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
            .map(|m| Migration::new(&m.version, &m.schema, |_| Ok(())))
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

fn logged(log: &Log, entry: String, fail: String) -> impl Fn(&Context) -> Result<(), BoxError> {
    let log = log.clone();
    move |_| {
        log.lock().unwrap().push(entry.clone());
        fail_if(&fail)
    }
}

fn migration(
    case: &MigrationCase,
    log: &Log,
    cancel: &hop_top_kit::bus::CancelHandle,
) -> Migration {
    let (schema, version) = (&case.schema, &case.version);
    let up = logged(log, format!("{schema} up {version}"), case.fail.clone());
    let cancel_after = case.cancel.then(|| cancel.clone());
    let mut m = Migration::new(version, schema, move |ctx| {
        let r = up(ctx);
        if let Some(c) = &cancel_after {
            c.cancel();
        }
        r
    });
    if case.down || !case.down_fail.is_empty() {
        m = m.with_down(logged(
            log,
            format!("{schema} down {version}"),
            case.down_fail.clone(),
        ));
    }
    if case.verify || !case.verify_fail.is_empty() {
        m = m.with_verify(logged(
            log,
            format!("{schema} verify {version}"),
            case.verify_fail.clone(),
        ));
    }
    m
}

fn lock_backups(base: &Path, locked: &BTreeMap<String, Vec<String>>) -> Vec<PathBuf> {
    use std::os::unix::fs::PermissionsExt;
    let mut out = Vec::new();
    for (schema, dirs) in locked {
        for d in dirs {
            let inner = base.join("backups").join(schema).join(d).join("locked");
            fs::create_dir_all(&inner).unwrap();
            fs::write(inner.join("f"), "").unwrap();
            fs::set_permissions(&inner, fs::Permissions::from_mode(0o500)).unwrap();
            out.push(inner);
        }
    }
    out
}

fn unlock(dirs: &[PathBuf]) {
    use std::os::unix::fs::PermissionsExt;
    for d in dirs {
        let _ = fs::set_permissions(d, fs::Permissions::from_mode(0o750));
    }
}

/// The event as the corpus states it: `duration_ms` checked and dropped.
fn event_case(e: &Event) -> EventCase {
    assert_eq!(e.source, "core.upgrade");
    let mut payload = e.payload.clone();
    if let Some(obj) = payload.as_object_mut() {
        if let Some(d) = obj.remove("duration_ms") {
            assert!(
                d.as_u64().is_some(),
                "{}: duration_ms = {d}",
                e.topic.as_str()
            );
        }
    }
    EventCase {
        topic: e.topic.as_str().to_string(),
        payload,
    }
}

fn run_scenario(sc: &Scenario) {
    let tmp = tempfile::tempdir().unwrap();
    // Stands in for the XDG data dir, or is the explicit one.
    let base = tmp.path().join(if sc.data_dir { "custom" } else { "data" });
    for (schema, dirs) in &sc.backups {
        fs::create_dir_all(base.join("backups").join(schema)).unwrap();
        for d in dirs {
            fs::create_dir_all(base.join("backups").join(schema).join(d)).unwrap();
        }
    }
    let locked = lock_backups(&base, &sc.locked_backups);

    let log: Log = Arc::default();
    let events: Arc<Mutex<Vec<EventCase>>> = Arc::default();
    let warnings: Arc<Mutex<Vec<String>>> = Arc::default();
    let (ctx, cancel) = Context::background().with_cancel();

    let ev = events.clone();
    let wn = warnings.clone();
    let mut m = Migrator::new("conftool", &sc.target)
        .data_dir(&base)
        .publisher(move |_: &Context, e: &Event| ev.lock().unwrap().push(event_case(e)))
        .on_warning(move |w| wn.lock().unwrap().push(w.to_string()));
    if sc.manual_rollback {
        m = m.manual_rollback();
    }
    if sc.retention != 0 {
        m = m.backup_retention(sc.retention);
    }
    if sc.final_verify || !sc.final_verify_fail.is_empty() {
        let (log, fail) = (log.clone(), sc.final_verify_fail.clone());
        m = m.verify(move |_, schema, version| {
            log.lock()
                .unwrap()
                .push(format!("{schema} final_verify {version}"));
            fail_if(&fail)
        });
    }
    for mc in &sc.migrations {
        m.register(migration(mc, &log, &cancel));
    }
    for dc in &sc.drivers {
        m.add_driver(Recording {
            case: dc.clone(),
            log: log.clone(),
        });
    }

    for (i, st) in sc.steps.iter().enumerate() {
        let ctx_name = format!("{}: step {i}", sc.name);
        log.lock().unwrap().clear();
        events.lock().unwrap().clear();
        warnings.lock().unwrap().clear();
        let run_ctx = if st.canceled {
            let (c, h) = ctx.with_cancel();
            h.cancel();
            c
        } else {
            ctx.clone()
        };
        let err = match st.op.as_str() {
            "run" => m.run(&run_ctx).err().map(|e| e.to_string()),
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
                assert_eq!(&got, want, "{ctx_name}: status");
                None
            }
            op => panic!("{ctx_name}: unknown op {op:?}"),
        };
        assert_eq!(err, st.error, "{ctx_name}: error");
        if let Some(want) = &st.calls {
            assert_eq!(&*log.lock().unwrap(), want, "{ctx_name}: calls");
        }
        if let Some(want) = &st.events {
            assert_eq!(&*events.lock().unwrap(), want, "{ctx_name}: events");
        }
        if let Some(want) = &st.warnings {
            let got = warnings.lock().unwrap();
            assert_eq!(got.len(), want.len(), "{ctx_name}: warnings {got:?}");
            for (g, w) in got.iter().zip(want) {
                assert!(
                    g.starts_with(w),
                    "{ctx_name}: warning {g:?}, want prefix {w:?}"
                );
            }
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
            assert_eq!(&got, want, "{ctx_name}: history");
        }
        for (schema, want) in &st.versions {
            let got = read_version_file_in(&base, schema).unwrap();
            assert_eq!(&got, want, "{ctx_name}: {schema} version file");
        }
        for (schema, want) in &st.backups {
            assert_eq!(
                &list_backups(&base, schema),
                want,
                "{ctx_name}: {schema} backups"
            );
        }
    }
    unlock(&locked);
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

// ---- document.json -------------------------------------------------------

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct DocSet {
    path: String,
    value: Value,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct DocRename {
    from: String,
    to: String,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct DocOp {
    set: Option<DocSet>,
    rename: Option<DocRename>,
    fail: Option<String>,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct DocVerifyCase {
    require: String,
}

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct DocStepCase {
    from: i64,
    to: i64,
    note: String,
    ops: Vec<DocOp>,
    verify: Option<DocVerifyCase>,
}

#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields)]
struct DocOptionsCase {
    #[serde(default)]
    version_field: String,
    #[serde(default)]
    latest: i64,
    #[serde(default)]
    missing_is_zero: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DocAppliedCase {
    from: i64,
    to: i64,
    note: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DocReportCase {
    from: i64,
    to: i64,
    applied: Vec<DocAppliedCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DocExpect {
    doc: Option<Value>,
    text: Option<String>,
    report: Option<DocReportCase>,
    error: Option<String>,
    kind: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DocScenario {
    name: String,
    doc: Option<Value>,
    text: Option<String>,
    #[serde(default)]
    options: DocOptionsCase,
    steps: Vec<DocStepCase>,
    expect: DocExpect,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DocumentFile {
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    version: String,
    #[allow(dead_code)]
    description: String,
    scenarios: Vec<DocScenario>,
}

fn lookup<'v>(doc: &'v Value, path: &str) -> Option<&'v Value> {
    path.split('.')
        .try_fold(doc, |cur, seg| cur.as_object()?.get(seg))
}

fn set_path(doc: &mut Value, path: &str, value: Value) {
    let segs: Vec<&str> = path.split('.').collect();
    let mut cur = doc;
    for seg in &segs[..segs.len() - 1] {
        let obj = cur.as_object_mut().unwrap();
        cur = obj
            .entry(seg.to_string())
            .or_insert_with(|| Value::Object(Default::default()));
        if !cur.is_object() {
            *cur = Value::Object(Default::default());
        }
    }
    cur.as_object_mut()
        .unwrap()
        .insert(segs[segs.len() - 1].to_string(), value);
}

fn doc_steps(cases: &[DocStepCase]) -> Vec<DocStep> {
    cases
        .iter()
        .map(|c| {
            let ops = c.ops.clone();
            // In place, on purpose: the migrator must hand steps a copy.
            let mut s = DocStep::new(c.from, c.to, move |mut doc: Value| {
                for op in &ops {
                    if let Some(set) = &op.set {
                        set_path(&mut doc, &set.path, set.value.clone());
                    }
                    if let Some(r) = &op.rename {
                        let obj = doc.as_object_mut().unwrap();
                        if let Some(v) = obj.remove(&r.from) {
                            obj.insert(r.to.clone(), v);
                        }
                    }
                    if let Some(msg) = &op.fail {
                        return Err(msg.clone().into());
                    }
                }
                Ok(doc)
            })
            .with_note(c.note.clone());
            if let Some(v) = &c.verify {
                let path = v.require.clone();
                s = s.with_verify(move |doc| match lookup(doc, &path) {
                    Some(_) => Ok(()),
                    None => Err(format!("missing {path}").into()),
                });
            }
            s
        })
        .collect()
}

fn kind_name(k: DocErrorKind) -> &'static str {
    match k {
        DocErrorKind::Newer => "newer",
        DocErrorKind::Version => "version",
        DocErrorKind::Gap => "gap",
        DocErrorKind::Step => "step",
        DocErrorKind::Verify => "verify",
        DocErrorKind::Invalid => "invalid",
    }
}

fn want_report(r: &DocReportCase) -> DocReport {
    DocReport {
        from: r.from,
        to: r.to,
        applied: r
            .applied
            .iter()
            .map(|a| DocApplied {
                from: a.from,
                to: a.to,
                note: a.note.clone(),
            })
            .collect(),
    }
}

#[test]
fn conformance_document() {
    let f: DocumentFile = load("document.json");
    assert_eq!(f.version, "v1");
    assert!(!f.scenarios.is_empty(), "scenarios section is empty");
    for sc in &f.scenarios {
        let name = &sc.name;
        let opts = DocOptions {
            version_field: sc.options.version_field.clone(),
            latest: sc.options.latest,
            missing_is_zero: sc.options.missing_is_zero,
        };
        let steps = doc_steps(&sc.steps);
        let input = match (&sc.text, &sc.doc) {
            (Some(t), _) => t.clone(),
            (None, Some(d)) => d.to_string(),
            _ => panic!("{name}: neither doc nor text"),
        };
        let check =
            |via: &str, got: Result<(Value, DocReport), hop_top_kit::upgrade::DocError>| match (
                &sc.expect.error,
                got,
            ) {
                (Some(want), Err(e)) => {
                    assert_eq!(&e.to_string(), want, "{name} ({via}): error");
                    assert_eq!(
                        Some(kind_name(e.kind())),
                        sc.expect.kind.as_deref(),
                        "{name} ({via}): kind"
                    );
                }
                (Some(want), Ok(_)) => panic!("{name} ({via}): no error, want {want:?}"),
                (None, Err(e)) => panic!("{name} ({via}): unexpected error {e}"),
                (None, Ok((doc, report))) => {
                    if let Some(want) = &sc.expect.doc {
                        assert_eq!(&doc, want, "{name} ({via}): doc");
                    }
                    if let Some(want) = &sc.expect.report {
                        assert_eq!(report, want_report(want), "{name} ({via}): report");
                    }
                }
            };

        let out = migrate_document(&input, &steps, &opts);
        if let (Some(want), Ok((text, _))) = (&sc.expect.text, &out) {
            assert_eq!(text, want, "{name}: byte-identical text");
        }
        check(
            "text",
            out.map(|(t, r)| (serde_json::from_str(&t).unwrap(), r)),
        );

        // In place: the same result, and the caller's value untouched on error.
        if sc.text.is_none() {
            let mut doc = sc.doc.clone().unwrap();
            let before = doc.clone();
            let got = migrate_value(&mut doc, &steps, &opts);
            if got.is_err() {
                assert_eq!(doc, before, "{name} (value): input changed on error");
            }
            check("value", got.map(|r| (doc, r)));
        }
    }
}
