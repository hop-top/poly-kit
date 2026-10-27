//! Runs `contracts/consent-v1` (capabilities, records, scenarios,
//! migration). Go is canonical: a failure means this port drifted, not
//! that a vector should change. Strict: unknown fields, another
//! `version`, and a corpus file no runner reads all fail.
#![cfg(all(feature = "consent-file", feature = "output"))]

mod consent_support;

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::SystemTime;

use consent_support::*;
use hop_top_kit::consent::{
    validate_capability, validate_subject, Answer, AuditEvent, DecisionSource, Error, FileStore,
    GrantStore, Grants, Prompt, Prompter,
};
use serde::Deserialize;
use serde_json::Value;

#[test]
fn corpus_files_all_have_a_runner() {
    for e in std::fs::read_dir(dir()).unwrap() {
        let name = e.unwrap().file_name().into_string().unwrap();
        assert!(
            FILES.contains(&name.as_str()),
            "corpus file {name} has no runner"
        );
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct IdCase {
    #[serde(default)]
    id: Option<String>,
    #[serde(default)]
    subject: Option<String>,
    ok: bool,
    #[serde(default)]
    error_kind: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Capabilities {
    version: String,
    #[allow(dead_code)]
    description: String,
    capabilities: Vec<IdCase>,
    subjects: Vec<IdCase>,
}

impl Versioned for Capabilities {
    fn version(&self) -> &str {
        &self.version
    }
}

fn assert_kind(
    what: &str,
    got: Result<(), hop_top_kit::consent::GrantError>,
    ok: bool,
    kind: &str,
) {
    match (got, ok) {
        (Ok(()), true) => {}
        (Err(e), true) => panic!("{what}: unexpected error {e}"),
        (Ok(()), false) => panic!("{what}: want {kind}, got ok"),
        (Err(e), false) => assert_eq!(e.kind.as_str(), kind, "{what}: {e}"),
    }
}

#[test]
fn capabilities() {
    let doc: Capabilities = load("capabilities.json");
    for c in &doc.capabilities {
        let id = c.id.as_deref().unwrap();
        assert_kind(
            &format!("capability {id:?}"),
            validate_capability(id),
            c.ok,
            &c.error_kind,
        );
    }
    for c in &doc.subjects {
        let s = c.subject.as_deref().unwrap();
        assert_kind(
            &format!("subject {s:?}"),
            validate_subject(s),
            c.ok,
            &c.error_kind,
        );
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecordCase {
    name: String,
    #[serde(default)]
    config_yaml: Option<String>,
    grants_yaml: Option<String>,
    ok: bool,
    #[serde(default)]
    grants: Vec<GrantJson>,
    #[serde(default)]
    error_kind: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Records {
    version: String,
    #[allow(dead_code)]
    description: String,
    cases: Vec<RecordCase>,
}

impl Versioned for Records {
    fn version(&self) -> &str {
        &self.version
    }
}

/// A file store for [`TOOL`] under a fresh temp dir, seeded.
fn store(
    tmp: &std::path::Path,
    config: Option<&str>,
    legacy: Option<&str>,
    grants: Option<&str>,
) -> FileStore {
    let s = FileStore::at(
        tmp.join("state")
            .join(TOOL)
            .join("consent")
            .join("grants.yaml"),
        tmp.join("config").join("kit").join("config.yaml"),
        tmp.join("config").join("kit").join("telemetry.yaml"),
    );
    write(s.config_path(), config);
    write(s.legacy_path(), legacy);
    write(s.grants_path(), grants);
    s
}

#[test]
fn records() {
    let doc: Records = load("records.json");
    for c in &doc.cases {
        let tmp = tempfile::tempdir().unwrap();
        let s = store(
            tmp.path(),
            c.config_yaml.as_deref(),
            None,
            c.grants_yaml.as_deref(),
        );
        match (s.grants(), c.ok) {
            (Ok(got), true) => assert_grants(&c.name, &got, &c.grants),
            (Err(e), true) => panic!("{}: {e}", c.name),
            (Ok(got), false) => panic!("{}: want {}, got {got:?}", c.name, c.error_kind),
            (Err(e), false) => assert_eq!(
                e.kind().map(|k| k.as_str()),
                Some(c.error_kind.as_str()),
                "{}: {e}",
                c.name
            ),
        }
    }
}

#[derive(Deserialize, PartialEq, Eq, Debug)]
#[serde(deny_unknown_fields)]
struct RefusedJson {
    reason: String,
    code: String,
    exit_code: i32,
    transience: String,
}

#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields)]
struct Expect {
    allowed: Option<bool>,
    #[serde(default)]
    reason: String,
    ok: Option<bool>,
    prompted: Option<bool>,
    refused: Option<RefusedJson>,
    #[serde(default)]
    error_kind: String,
    removed: Option<bool>,
    #[serde(default)]
    grants: Vec<GrantJson>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AnswerJson {
    decision: String,
    duration: String,
    #[serde(default)]
    expires_at: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Step {
    op: String,
    grant: Option<GrantJson>,
    #[serde(default)]
    capability: String,
    #[serde(default)]
    subject: String,
    #[serde(default)]
    source: String,
    #[serde(default)]
    seconds: i64,
    #[serde(default)]
    prompter: String,
    answer: Option<AnswerJson>,
    expect: Option<Expect>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Scenario {
    name: String,
    now: String,
    #[serde(default)]
    env: BTreeMap<String, String>,
    prompt_version: Option<i64>,
    config_yaml: Option<String>,
    legacy_yaml: Option<String>,
    grants_yaml: Option<String>,
    steps: Vec<Step>,
    #[serde(default)]
    audit: Vec<Value>,
    #[serde(default, deserialize_with = "present")]
    grants_file: Option<Value>,
    #[serde(default, deserialize_with = "present")]
    config_file: Option<Value>,
    #[serde(default, deserialize_with = "present")]
    legacy_file: Option<Value>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Scenarios {
    version: String,
    #[allow(dead_code)]
    description: String,
    scenarios: Vec<Scenario>,
}

impl Versioned for Scenarios {
    fn version(&self) -> &str {
        &self.version
    }
}

#[test]
fn scenarios() {
    run_file("scenarios.json");
}

#[test]
fn migration() {
    run_file("migration.json");
}

fn run_file(name: &str) {
    let doc: Scenarios = load(name);
    for sc in &doc.scenarios {
        run_scenario(sc);
    }
}

struct Run {
    store: Arc<FileStore>,
    now: Arc<Mutex<SystemTime>>,
    audit: Arc<Mutex<Vec<AuditEvent>>>,
    env: BTreeMap<String, String>,
    prompt_version: i64,
}

impl Run {
    fn open(&self) -> Grants {
        let now = self.now.clone();
        let env = self.env.clone();
        let audit = self.audit.clone();
        Grants::builder(self.store.clone())
            .clock(move || *now.lock().unwrap())
            .env(move |k| env.get(k).cloned().unwrap_or_default())
            .prompt_version(self.prompt_version)
            .auditor(Arc::new(move |e: &AuditEvent| {
                audit.lock().unwrap().push(e.clone());
                Ok(())
            }))
            .build()
    }
}

fn run_scenario(sc: &Scenario) {
    let tmp = tempfile::tempdir().unwrap();
    let store = Arc::new(store(
        tmp.path(),
        sc.config_yaml.as_deref(),
        sc.legacy_yaml.as_deref(),
        sc.grants_yaml.as_deref(),
    ));
    let run = Run {
        store: store.clone(),
        now: Arc::new(Mutex::new(time(&sc.now).unwrap())),
        audit: Arc::new(Mutex::new(Vec::new())),
        env: sc.env.clone(),
        prompt_version: sc.prompt_version.unwrap_or(1),
    };
    let mut g = run.open();
    for (i, st) in sc.steps.iter().enumerate() {
        let where_ = format!("{}: step {i} {}", sc.name, st.op);
        step(&where_, st, &mut g, &run);
    }
    let got: Vec<Value> = run
        .audit
        .lock()
        .unwrap()
        .iter()
        .map(AuditEvent::to_value)
        .collect();
    assert_eq!(got, sc.audit, "{}: audit", sc.name);
    for (what, path, want) in [
        ("grants_file", store.grants_path(), &sc.grants_file),
        ("config_file", store.config_path(), &sc.config_file),
        ("legacy_file", store.legacy_path(), &sc.legacy_file),
    ] {
        if let Some(want) = want {
            assert_file(&format!("{}: {what}", sc.name), path, want);
        }
    }
}

fn expect_err<T: std::fmt::Debug>(where_: &str, got: Result<T, Error>, kind: &str) {
    match got {
        Ok(v) if kind.is_empty() => drop(v),
        Ok(v) => panic!("{where_}: want {kind}, got {v:?}"),
        Err(e) if kind.is_empty() => panic!("{where_}: unexpected error {e}"),
        Err(e) => assert_eq!(e.kind().map(|k| k.as_str()), Some(kind), "{where_}: {e}"),
    }
}

fn expect_outcome<T: std::fmt::Debug>(where_: &str, got: Result<T, Error>, ex: &Expect) {
    if !ex.error_kind.is_empty() {
        if let Err(e) = &got {
            assert!(
                e.refusal().is_none(),
                "{where_}: a store failure must not read as a refusal: {e}"
            );
        }
        return expect_err(where_, got, &ex.error_kind);
    }
    if ex.ok == Some(true) {
        return expect_err(where_, got, "");
    }
    let err = got.expect_err(where_);
    let re = err
        .refusal()
        .unwrap_or_else(|| panic!("{where_}: want refusal, got {err}"));
    let ce = re.to_cli_error();
    assert_eq!(
        (re.code(), re.exit_code(), re.transience()),
        (ce.code.as_str(), ce.exit_code, ce.transience.as_str())
    );
    let got = RefusedJson {
        reason: re.reason.as_str().into(),
        code: ce.code,
        exit_code: ce.exit_code,
        transience: ce.transience,
    };
    assert_eq!(Some(&got), ex.refused.as_ref(), "{where_}: refusal");
}

struct Asking {
    answer: Option<Answer>,
    asked: Mutex<bool>,
}

impl Prompter for Asking {
    fn ask(&self, _: &Prompt) -> Result<Answer, String> {
        *self.asked.lock().unwrap() = true;
        self.answer
            .clone()
            .ok_or_else(|| "prompt: no tty".to_string())
    }
}

fn step(where_: &str, st: &Step, g: &mut Grants, run: &Run) {
    let none = Expect::default();
    let ex = st.expect.as_ref().unwrap_or(&none);
    let (cap, subj) = (st.capability.as_str(), st.subject.as_str());
    match st.op.as_str() {
        "decide" => {
            let gr = st.grant.as_ref().unwrap().to_grant();
            expect_err(
                where_,
                gr.map_err(Error::from).and_then(|gr| g.decide(gr)),
                &ex.error_kind,
            );
        }
        "check" => {
            let v = g.check(cap, subj);
            if !ex.error_kind.is_empty() {
                return expect_err(where_, v, &ex.error_kind);
            }
            let v = v.unwrap_or_else(|e| panic!("{where_}: {e}"));
            assert_eq!(
                (v.allowed, v.reason.as_str()),
                (ex.allowed.unwrap(), ex.reason.as_str()),
                "{where_}"
            );
        }
        "use" => expect_outcome(where_, g.use_grant(cap, subj), ex),
        "request" => {
            let p = match (st.prompter.as_str(), &st.answer) {
                ("none", _) => None,
                ("error", _) => Some(Asking {
                    answer: None,
                    asked: Mutex::new(false),
                }),
                ("", Some(a)) => Some(Asking {
                    answer: Some(Answer {
                        decision: a.decision.parse().unwrap(),
                        duration: a.duration.parse().unwrap(),
                        expires_at: time(&a.expires_at),
                    }),
                    asked: Mutex::new(false),
                }),
                _ => panic!("{where_}: request needs prompter or answer"),
            };
            expect_outcome(
                where_,
                g.request(cap, subj, p.as_ref().map(|p| p as &dyn Prompter)),
                ex,
            );
            let asked = p.as_ref().is_some_and(|p| *p.asked.lock().unwrap());
            if let Some(want) = ex.prompted {
                assert_eq!(asked, want, "{where_}: prompted");
            }
        }
        "revoke" => {
            let r = g.revoke(cap, subj, DecisionSource::from_wire(&st.source));
            if !ex.error_kind.is_empty() {
                return expect_err(where_, r, &ex.error_kind);
            }
            let removed = r.unwrap_or_else(|e| panic!("{where_}: {e}"));
            assert_eq!(removed, ex.removed.unwrap(), "{where_}: removed");
        }
        "list" => assert_grants(
            where_,
            &g.list().unwrap_or_else(|e| panic!("{where_}: {e}")),
            &ex.grants,
        ),
        "advance" => {
            let mut now = run.now.lock().unwrap();
            *now += std::time::Duration::from_secs(st.seconds as u64);
        }
        "end_session" => g.end_session(),
        "reopen" => *g = run.open(),
        other => panic!("{where_}: unknown op {other:?}"),
    }
}
