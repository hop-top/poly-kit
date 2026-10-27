//! Runs `contracts/consent-v1/delivery.json`: the audit log as an
//! outbox, relayed onto an in-process bus with one sync subscriber.
//! A crash between publish and ack is a real failed cursor write: the
//! subscriber puts a non-empty directory where the cursor is renamed to.
#![cfg(feature = "consent-file")]

mod consent_support;

use std::fs;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use consent_support::*;
use hop_top_kit::bus::{Bus, BusError, SharedBus};
use hop_top_kit::consent::{
    AuditEvent, AuditKind, Auditor, DecisionSource, FileAuditor, Publisher, Relay,
};
use serde::Deserialize;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Record {
    event: String,
    capability: String,
    #[serde(default)]
    subject: String,
    #[serde(default)]
    decision: String,
    #[serde(default)]
    duration: String,
    #[serde(default)]
    decision_source: String,
    #[serde(default)]
    prompt_version: i64,
    #[serde(default)]
    reason: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Expect {
    #[serde(default)]
    seq: u64,
    delivered: Option<Vec<u64>>,
    cursor: Option<u64>,
    pending: Option<usize>,
    #[serde(default)]
    error: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Step {
    op: String,
    record: Option<Record>,
    fail_seq: Option<u64>,
    expect: Option<Expect>,
}

#[derive(Deserialize, Debug, PartialEq, Eq, Clone)]
#[serde(deny_unknown_fields)]
struct Published {
    seq: u64,
    topic: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CursorFile {
    seq: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Scenario {
    name: String,
    now: String,
    cursor_json: Option<String>,
    steps: Vec<Step>,
    published: Vec<Published>,
    cursor_file: Option<CursorFile>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Delivery {
    version: String,
    #[allow(dead_code)]
    description: String,
    scenarios: Vec<Scenario>,
}

impl Versioned for Delivery {
    fn version(&self) -> &str {
        &self.version
    }
}

fn opt<T: std::str::FromStr>(s: &str) -> Option<T>
where
    T::Err: std::fmt::Debug,
{
    (!s.is_empty()).then(|| s.parse().unwrap())
}

/// Makes the next cursor write fail: the cursor path becomes a
/// non-empty directory until [`Crash::restore`].
struct Crash {
    cursor: PathBuf,
    armed: AtomicBool,
    fired: AtomicBool,
}

impl Crash {
    fn fire(&self) {
        if !self.armed.swap(false, Ordering::SeqCst) {
            return;
        }
        let backup = self.cursor.with_extension("bak");
        if self.cursor.exists() {
            fs::rename(&self.cursor, &backup).unwrap();
        }
        fs::create_dir_all(self.cursor.join("x")).unwrap();
        self.fired.store(true, Ordering::SeqCst);
    }

    fn restore(&self) {
        if !self.fired.swap(false, Ordering::SeqCst) {
            return;
        }
        fs::remove_dir_all(&self.cursor).unwrap();
        let backup = self.cursor.with_extension("bak");
        if backup.exists() {
            fs::rename(&backup, &self.cursor).unwrap();
        }
    }
}

#[test]
fn delivery() {
    let doc: Delivery = load("delivery.json");
    for sc in &doc.scenarios {
        run(sc);
    }
}

fn run(sc: &Scenario) {
    let tmp = tempfile::tempdir().unwrap();
    let log_path = tmp.path().join(TOOL).join("consent").join("audit.jsonl");
    let at = time(&sc.now).unwrap();
    let cursor = log_path.parent().unwrap().join("audit.cursor");
    let crash = Arc::new(Crash {
        cursor: cursor.clone(),
        armed: false.into(),
        fired: false.into(),
    });

    let bus = SharedBus::new(Bus::new());
    let fail_seq = Arc::new(AtomicU64::new(0));
    let got = Arc::new(Mutex::new(Vec::<Published>::new()));
    {
        let (fail_seq, got, crash, name) = (
            fail_seq.clone(),
            got.clone(),
            crash.clone(),
            sc.name.clone(),
        );
        bus.subscribe("kit.core.consent.#", move |e| {
            let seq = e.payload["seq"].as_u64().unwrap();
            let key = format!("{}:{seq}", e.payload["log"].as_str().unwrap());
            assert_eq!(
                (e.id.as_str(), e.source.as_str()),
                (key.as_str(), "core.consent"),
                "{name}"
            );
            assert_eq!(
                e.subject,
                e.payload["capability"].as_str().unwrap(),
                "{name}"
            );
            got.lock().unwrap().push(Published {
                seq,
                topic: e.topic.as_str().into(),
            });
            if seq == fail_seq.load(Ordering::SeqCst) {
                return Err(BusError::handler(format!("subscriber refuses seq {seq}")));
            }
            crash.fire();
            Ok(())
        });
    }
    let publisher: Arc<dyn Publisher> = Arc::new(bus);
    let open = || {
        let a = Arc::new(FileAuditor::at(&log_path));
        let r = Relay::new(a.clone(), publisher.clone());
        (a, r)
    };
    let (mut a, mut r) = open();
    assert_eq!(r.cursor_path(), cursor.as_path());
    if let Some(body) = &sc.cursor_json {
        write(&cursor, Some(body));
    }
    for (i, st) in sc.steps.iter().enumerate() {
        let where_ = format!("{}: step {i} {}", sc.name, st.op);
        match st.op.as_str() {
            "record" => {
                let rc = st.record.as_ref().unwrap();
                let mut e = AuditEvent::new(
                    rc.event.parse::<AuditKind>().unwrap(),
                    at,
                    &rc.capability,
                    &rc.subject,
                );
                e.decision = opt(&rc.decision);
                e.duration = opt(&rc.duration);
                e.decision_source = (!rc.decision_source.is_empty())
                    .then(|| DecisionSource::from_wire(&rc.decision_source));
                e.prompt_version = rc.prompt_version;
                e.reason = opt(&rc.reason);
                a.record(&e).unwrap_or_else(|err| panic!("{where_}: {err}"));
                let want = st.expect.as_ref().unwrap().seq;
                let recs = a.read_after(want - 1).unwrap();
                assert_eq!(
                    recs.iter().map(|r| r.seq).collect::<Vec<_>>(),
                    vec![want],
                    "{where_}"
                );
            }
            "subscriber" => fail_seq.store(st.fail_seq.unwrap(), Ordering::SeqCst),
            "crash_after_publish" => crash.armed.store(true, Ordering::SeqCst),
            "restart" => (a, r) = open(),
            "flush" => {
                let res = r.flush();
                crash.restore();
                let ex = st.expect.as_ref().unwrap();
                assert_eq!(res.is_err(), ex.error, "{where_}: {res:?}");
                let res = res.unwrap_or_else(|e| e.progress);
                assert_eq!(
                    (res.delivered, res.cursor, res.pending),
                    (
                        ex.delivered.clone().unwrap(),
                        ex.cursor.unwrap(),
                        ex.pending.unwrap()
                    ),
                    "{where_}"
                );
            }
            other => panic!("{where_}: unknown op {other:?}"),
        }
    }
    assert_eq!(*got.lock().unwrap(), sc.published, "{}: published", sc.name);
    if let Some(want) = &sc.cursor_file {
        check_cursor(&sc.name, &cursor, want.seq, &a.log_id().unwrap());
    }
}

fn check_cursor(name: &str, path: &Path, seq: u64, log: &str) {
    let c: serde_json::Value = serde_json::from_str(&fs::read_to_string(path).unwrap()).unwrap();
    assert_eq!(
        (c["seq"].as_u64(), c["log"].as_str()),
        (Some(seq), Some(log)),
        "{name}: cursor"
    );
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mode = fs::metadata(path).unwrap().permissions().mode() & 0o777;
        assert_eq!(mode, 0o600, "{name}: cursor perm");
    }
}
