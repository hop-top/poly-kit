//! Runs the shared breaker corpus in `contracts/breaker-v1`. The
//! vectors are the source of truth: a failure means this port drifted
//! from the contract (Go is canonical), not that a vector should be
//! edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
#![cfg(feature = "breaker")]

#[path = "breaker_support/mod.rs"]
mod support;

use std::collections::BTreeMap;
use std::fs;
use std::panic::catch_unwind;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use hop_top_kit::breaker::{
    Action, Breaker, CircuitOpts, Topics, Value, DEFAULT_CLOSED, DEFAULT_HALF_OPENED,
    DEFAULT_OPENED, DEFAULT_TRIPPED, SOURCE,
};
use hop_top_kit::bus::Topic;
use serde::Deserialize;
use serde_json::{json, Value as Json};

use support::{epoch, Events, FakeClock, Inline, Logs};

const FILES: &[&str] = &["breaker.json"];

#[derive(Deserialize, Default, PartialEq, Debug)]
#[serde(deny_unknown_fields)]
struct TopicsCase {
    #[serde(default)]
    tripped: String,
    #[serde(default)]
    opened: String,
    #[serde(default)]
    closed: String,
    #[serde(default)]
    half_opened: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CircuitCase {
    #[serde(default)]
    failure_threshold: u32,
    #[serde(default)]
    success_threshold: u32,
    #[serde(default)]
    delay_ms: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RateCase {
    max: u32,
    period_ms: u64,
}

#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields)]
struct Config {
    circuit: Option<CircuitCase>,
    max_ops: Option<u64>,
    max_bytes: Option<u64>,
    rate: Option<RateCase>,
    #[serde(default)]
    on_trip: String,
    #[serde(default)]
    publisher: bool,
    #[serde(default)]
    topic_prefix: String,
    topics: Option<TopicsCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Step {
    op: String,
    #[serde(default)]
    want: String,
    #[serde(default)]
    success: bool,
    #[serde(default)]
    n: u64,
    #[serde(default)]
    ms: u64,
    #[serde(default)]
    reason: String,
    #[serde(default)]
    trips: u64,
    #[serde(default)]
    last_trip_reason: String,
    #[serde(default)]
    last_trip_at_ms: Option<u64>,
    #[serde(default)]
    counters: BTreeMap<String, u64>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Scenario {
    name: String,
    config: Config,
    steps: Vec<Step>,
    logs: Vec<Json>,
    events: Option<Vec<Json>>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PrefixCase {
    name: String,
    prefix: String,
    topics: Option<TopicsCase>,
    panic: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Corpus {
    #[serde(rename = "$schema")]
    _schema: String,
    #[serde(rename = "$id")]
    _id: String,
    version: String,
    description: String,
    source: String,
    default_topics: TopicsCase,
    scenarios: Vec<Scenario>,
    topic_prefixes: Vec<PrefixCase>,
}

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/breaker-v1")
}

fn corpus() -> Corpus {
    let mut found: Vec<String> = fs::read_dir(dir())
        .expect("corpus dir")
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    found.sort();
    assert_eq!(found, FILES, "corpus files without a runner");
    let raw = fs::read_to_string(dir().join("breaker.json")).unwrap();
    let c: Corpus = serde_json::from_str(&raw).expect("decode breaker.json");
    assert_eq!(c.version, "v1");
    assert!(!c.description.is_empty() && !c.scenarios.is_empty());
    c
}

fn topics_of(t: &Topics) -> TopicsCase {
    TopicsCase {
        tripped: t.tripped.as_str().into(),
        opened: t.opened.as_str().into(),
        closed: t.closed.as_str().into(),
        half_opened: t.half_opened.as_str().into(),
    }
}

#[test]
fn constants() {
    let c = corpus();
    assert_eq!(c.source, SOURCE);
    let want = TopicsCase {
        tripped: DEFAULT_TRIPPED.into(),
        opened: DEFAULT_OPENED.into(),
        closed: DEFAULT_CLOSED.into(),
        half_opened: DEFAULT_HALF_OPENED.into(),
    };
    assert_eq!(c.default_topics, want);
    assert_eq!(topics_of(&Topics::default()), want);
}

#[test]
fn topic_prefixes() {
    for v in corpus().topic_prefixes {
        let prefix = v.prefix.clone();
        let got = catch_unwind(move || Topics::from_prefix(&prefix));
        match (got, v.panic, v.topics) {
            (Err(e), Some(want), None) => {
                let msg = e.downcast_ref::<String>().cloned().unwrap_or_default();
                assert_eq!(msg, want, "{}", v.name);
            }
            (Ok(t), None, Some(want)) => assert_eq!(topics_of(&t), want, "{}", v.name),
            (got, _, _) => panic!("{}: unexpected outcome {:?}", v.name, got.is_ok()),
        }
    }
}

#[test]
fn scenarios() {
    for sc in corpus().scenarios {
        run(&sc);
    }
}

fn build(sc: &Scenario, clock: Arc<FakeClock>, logs: Arc<Logs>, events: Arc<Events>) -> Breaker {
    let c = &sc.config;
    let mut b = Breaker::builder(sc.name.as_str())
        .clock(clock)
        .logger(logs)
        .spawner(Arc::new(Inline));
    if let Some(cc) = &c.circuit {
        b = b.circuit(CircuitOpts {
            failure_threshold: cc.failure_threshold,
            success_threshold: cc.success_threshold,
            delay: Duration::from_millis(cc.delay_ms),
        });
    }
    if let Some(n) = c.max_ops {
        b = b.max_ops(n);
    }
    if let Some(n) = c.max_bytes {
        b = b.max_bytes(n);
    }
    if let Some(r) = &c.rate {
        b = b.max_per_interval(r.max, Duration::from_millis(r.period_ms));
    }
    b = b.on_trip(match c.on_trip.as_str() {
        "" | "halt" => Action::Halt,
        "warn" => Action::Warn,
        "degrade" => Action::Degrade,
        other => panic!("{}: unknown on_trip {other}", sc.name),
    });
    if c.publisher {
        b = b.publisher(events);
    }
    if !c.topic_prefix.is_empty() {
        b = b.topic_prefix(&c.topic_prefix);
    }
    if let Some(t) = &c.topics {
        b = b.topics(Topics {
            tripped: Topic::from(t.tripped.as_str()),
            opened: Topic::from(t.opened.as_str()),
            closed: Topic::from(t.closed.as_str()),
            half_opened: Topic::from(t.half_opened.as_str()),
        });
    }
    assert_eq!(
        c.publisher,
        sc.events.is_some(),
        "{}: events iff publisher",
        sc.name
    );
    b.build()
}

fn run(sc: &Scenario) {
    let (clock, logs, events) = (FakeClock::new(), Logs::new(), Events::new());
    let b = build(sc, clock.clone(), logs.clone(), events.clone());
    for (i, s) in sc.steps.iter().enumerate() {
        let at = format!("{} step {i} ({})", sc.name, s.op);
        match s.op.as_str() {
            "allow" => {
                let got = if b.allow().is_ok() { "ok" } else { "broken" };
                assert_eq!(got, s.want, "{at}");
            }
            "record" => b.record(s.success, s.n),
            "advance" => clock.advance(Duration::from_millis(s.ms)),
            "state" => assert_eq!(b.state().as_str(), s.want, "{at}"),
            "trip" => b.trip(&s.reason),
            "reset" => b.reset(),
            "stats" => {
                let st = b.stats();
                let at_ms = st
                    .last_trip_at
                    .map(|t| t.duration_since(epoch()).unwrap().as_millis() as u64);
                assert_eq!(st.trips, s.trips, "{at}: trips");
                assert_eq!(st.last_trip_reason, s.last_trip_reason, "{at}: reason");
                assert_eq!(at_ms, s.last_trip_at_ms, "{at}: last_trip_at_ms");
                assert_eq!(st.counters, s.counters, "{at}: counters");
            }
            other => panic!("{at}: unknown op {other}"),
        }
    }

    let got: Vec<Json> = logs
        .records()
        .iter()
        .map(|r| {
            let fields: serde_json::Map<String, Json> = r
                .fields
                .iter()
                .map(|(k, v)| {
                    let v = match v {
                        Value::Str(s) => json!(s),
                        Value::U64(n) => json!(n),
                    };
                    (k.to_string(), v)
                })
                .collect();
            json!({"level": r.level.as_str(), "msg": r.message, "fields": fields})
        })
        .collect();
    assert_eq!(got, sc.logs, "{}: logs", sc.name);

    if let Some(want) = &sc.events {
        let mut got: Vec<String> = events
            .all()
            .into_iter()
            .map(|(topic, source, payload)| {
                assert_eq!(source, SOURCE, "{}: source", sc.name);
                let payload: Json = serde_json::from_str(&payload.to_json()).unwrap();
                json!({"topic": topic, "payload": payload}).to_string()
            })
            .collect();
        let mut want: Vec<String> = want.iter().map(Json::to_string).collect();
        got.sort();
        want.sort();
        assert_eq!(got, want, "{}: events", sc.name);
    }
}
