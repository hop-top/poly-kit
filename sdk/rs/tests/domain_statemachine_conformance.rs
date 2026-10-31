//! Runs the shared state machine corpus in `contracts/statemachine-v1`.
//! The vectors are the source of truth: a failure means this port
//! drifted from the contract (Go is canonical), not that a vector should
//! be edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
#![cfg(feature = "domain")]

#[path = "domain_statemachine_support/mod.rs"]
mod support;

use std::collections::HashMap;
use std::error::Error;
use std::fmt;
use std::fs;
use std::panic::catch_unwind;
use std::path::PathBuf;
use std::sync::Arc;

use hop_top_kit::bus::{Context, Topic};
use hop_top_kit::domain::{
    default_state_machine_topics, Payload, PublishError, Rules, State, StateMachine,
    StateMachineTopics, TransitionError, TransitionPayload,
};
use serde::Deserialize;

use support::{within, Manual, Recording};

const FILES: &[&str] = &["statemachine.json"];

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Topics {
    pre_transitioned: String,
    post_transitioned: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TransitionCase {
    name: String,
    from: String,
    to: String,
    force: bool,
    #[serde(default)]
    error: String,
    #[serde(default)]
    payload: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ErrorCase {
    name: String,
    from: String,
    to: String,
    allowed: Option<Vec<String>>,
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct VetoCase {
    name: String,
    veto: String,
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PayloadCase {
    name: String,
    from: String,
    to: String,
    force: bool,
    json: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PrefixCase {
    name: String,
    prefix: String,
    #[serde(default)]
    pre_transitioned: String,
    #[serde(default)]
    post_transitioned: String,
    #[serde(default)]
    panic: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct OverrideCase {
    name: String,
    pre_transitioned: String,
    post_transitioned: String,
    want_pre: String,
    want_post: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Corpus {
    #[serde(rename = "$schema")]
    _schema: String,
    #[serde(rename = "$id")]
    _id: String,
    version: String,
    #[serde(rename = "description")]
    _description: String,
    source: String,
    default_topics: Topics,
    rules: HashMap<String, Vec<String>>,
    transitions: Vec<TransitionCase>,
    transition_errors: Vec<ErrorCase>,
    vetoes: Vec<VetoCase>,
    payloads: Vec<PayloadCase>,
    topic_prefixes: Vec<PrefixCase>,
    topic_overrides: Vec<OverrideCase>,
}

impl Corpus {
    fn rules(&self) -> Rules {
        self.rules
            .iter()
            .map(|(k, v)| (State::from(k.as_str()), states(v)))
            .collect()
    }
}

fn states(v: &[String]) -> Vec<State> {
    v.iter().map(|s| State::from(s.as_str())).collect()
}

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/statemachine-v1")
}

fn load() -> Corpus {
    let mut found: Vec<String> = fs::read_dir(dir())
        .expect("contracts/statemachine-v1")
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .filter(|n| n.ends_with(".json"))
        .collect();
    found.sort();
    assert_eq!(found, FILES, "corpus files without a runner, or missing");
    let raw = fs::read_to_string(dir().join("statemachine.json")).unwrap();
    let c: Corpus = serde_json::from_str(&raw).expect("parse statemachine.json");
    assert_eq!(c.version, "v1");
    c
}

fn bg() -> Context {
    Context::background()
}

#[test]
fn conformance_state_machine_defaults() {
    let c = load();
    let d = default_state_machine_topics();
    assert_eq!(
        d.pre_transitioned.as_str(),
        c.default_topics.pre_transitioned
    );
    assert_eq!(
        d.post_transitioned.as_str(),
        c.default_topics.post_transitioned
    );
    assert_eq!(StateMachine::new(c.rules(), None).topics(), &d);
}

#[test]
fn conformance_state_machine_transitions() {
    let c = load();
    assert!(!c.transitions.is_empty(), "transitions section is empty");
    for t in &c.transitions {
        assert!(
            t.error.is_empty() != t.payload.is_empty(),
            "{}: a vector names either an error or a payload",
            t.name
        );
        let rec = Recording::new();
        let spawner = Manual::default();
        let sm =
            StateMachine::new(c.rules(), Some(rec.clone())).with_spawner(Arc::new(spawner.clone()));
        let (from, to, force) = (t.from.clone(), t.to.clone(), t.force);
        let res = within(move || sm.transition(&bg(), from, to, force));
        spawner.run_all();
        if !t.error.is_empty() {
            let err = res.expect_err(&t.name);
            assert_eq!(err.to_string(), t.error, "{}", t.name);
            assert!(err.is_invalid_transition(), "{}", t.name);
            assert!(
                rec.events().is_empty(),
                "{}: a rejected transition published",
                t.name
            );
            continue;
        }
        res.unwrap_or_else(|e| panic!("{}: {e}", t.name));
        let events = rec.events();
        let topics: Vec<&str> = events.iter().map(|e| e.topic.as_str()).collect();
        assert_eq!(
            topics,
            [
                &*c.default_topics.pre_transitioned,
                &*c.default_topics.post_transitioned
            ],
            "{}",
            t.name
        );
        for e in &events {
            assert_eq!(e.source, c.source, "{}", t.name);
            assert_eq!(e.payload.to_json(), t.payload, "{}", t.name);
        }
    }
}

#[test]
fn conformance_state_machine_errors() {
    let c = load();
    assert!(
        !c.transition_errors.is_empty(),
        "transition_errors section is empty"
    );
    for e in &c.transition_errors {
        let te = TransitionError {
            from: e.from.as_str().into(),
            to: e.to.as_str().into(),
            allowed: e.allowed.as_deref().map(states),
        };
        assert_eq!(te.to_string(), e.error, "{}", e.name);
        assert!(te.is_invalid_transition(), "{}", e.name);
    }
}

#[derive(Debug)]
struct Veto(String);

impl fmt::Display for Veto {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl Error for Veto {}

#[test]
fn conformance_state_machine_vetoes() {
    let c = load();
    assert!(!c.vetoes.is_empty(), "vetoes section is empty");
    for v in &c.vetoes {
        let text = v.veto.clone();
        let publisher =
            move |_: &Context, _: &str, _: &str, _: &Payload| -> Result<(), PublishError> {
                Err(Box::new(Veto(text.clone())))
            };
        let sm = StateMachine::new(c.rules(), Some(Arc::new(publisher)));
        let err = within(move || sm.transition(&bg(), "open", "closed", false)).expect_err(&v.name);
        assert_eq!(err.to_string(), v.error, "{}", v.name);
        let src = err.source().and_then(|s| s.downcast_ref::<Veto>());
        assert_eq!(
            src.map(|s| s.0.as_str()),
            Some(v.veto.as_str()),
            "{}",
            v.name
        );
        assert!(!err.is_invalid_transition(), "{}", v.name);
    }
}

#[test]
fn conformance_state_machine_payloads() {
    let c = load();
    assert!(!c.payloads.is_empty(), "payloads section is empty");
    for p in &c.payloads {
        let payload = TransitionPayload {
            from: p.from.as_str().into(),
            to: p.to.as_str().into(),
            force: p.force,
        };
        assert_eq!(payload.to_json(), p.json, "{}", p.name);
    }
}

#[test]
fn conformance_state_machine_topic_prefixes() {
    let c = load();
    assert!(
        !c.topic_prefixes.is_empty(),
        "topic_prefixes section is empty"
    );
    for p in &c.topic_prefixes {
        let topics = !p.pre_transitioned.is_empty() && !p.post_transitioned.is_empty();
        let no_topics = p.pre_transitioned.is_empty() && p.post_transitioned.is_empty();
        assert!(
            if p.panic.is_empty() {
                topics
            } else {
                no_topics
            },
            "{}: a vector names either a panic or both topics",
            p.name
        );
        let prefix = p.prefix.clone();
        let res = catch_unwind(move || {
            StateMachine::new(Rules::new(), None)
                .with_topic_prefix(&prefix)
                .topics()
                .clone()
        });
        match res {
            Ok(t) => {
                assert!(p.panic.is_empty(), "{}: want panic {:?}", p.name, p.panic);
                assert_eq!(
                    t.pre_transitioned.as_str(),
                    p.pre_transitioned,
                    "{}",
                    p.name
                );
                assert_eq!(
                    t.post_transitioned.as_str(),
                    p.post_transitioned,
                    "{}",
                    p.name
                );
            }
            Err(payload) => {
                let msg = panic_text(payload.as_ref());
                assert!(!p.panic.is_empty(), "{}: unexpected panic {msg:?}", p.name);
                assert_eq!(msg.as_deref(), Some(p.panic.as_str()), "{}", p.name);
            }
        }
    }
}

/// A panic's message, whatever `panic!` produced: a `String` when
/// formatted, a `&'static str` for a literal. `None` for any other
/// payload, which then never equals a vector's text.
fn panic_text(payload: &(dyn std::any::Any + Send)) -> Option<String> {
    payload
        .downcast_ref::<String>()
        .cloned()
        .or_else(|| payload.downcast_ref::<&str>().map(|s| s.to_string()))
}

#[test]
fn conformance_state_machine_topic_overrides() {
    let c = load();
    assert!(
        !c.topic_overrides.is_empty(),
        "topic_overrides section is empty"
    );
    for o in &c.topic_overrides {
        let rec = Recording::new();
        let spawner = Manual::default();
        let sm = StateMachine::new(c.rules(), Some(rec.clone()))
            .with_spawner(Arc::new(spawner.clone()))
            .with_topics(StateMachineTopics {
                pre_transitioned: Topic::from(o.pre_transitioned.as_str()),
                post_transitioned: Topic::from(o.post_transitioned.as_str()),
            });
        within(move || sm.transition(&bg(), "open", "closed", false)).unwrap();
        spawner.run_all();
        assert_eq!(
            rec.topics(),
            [o.want_pre.clone(), o.want_post.clone()],
            "{}",
            o.name
        );
    }
}
