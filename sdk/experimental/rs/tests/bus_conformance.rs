//! Runs the shared bus corpus in `contracts/bus-v1`. The vectors are the
//! source of truth: a failure means this port drifted from the contract
//! (Go is canonical), not that a vector should be edited to fit.
//!
//! Strict by construction: vector files deserialize into typed structs
//! that deny unknown fields, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
//!
//! Sections the port cannot satisfy yet are listed in [`PENDING`]. Each is
//! skipped with a printed line; when the capability lands its probe flips
//! and the test fails until the entry is removed and the section asserted.

#![cfg(feature = "bus")]

use std::collections::BTreeMap;
use std::fs;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::path::PathBuf;

use hop_top_kit::bus::{
    parse_topic, prefix_topics, prefixed_topic_of, qualifiers_from, validate, validate_topic,
    Event, Mode, Qualifiers, Topic,
};
use serde::de::DeserializeOwned;
use serde::Deserialize;
use serde_json::{json, Value};

/// Every vector file this runner consumes.
const FILES: &[&str] = &[
    "builder.json",
    "event_json.json",
    "match.json",
    "mode_parse.json",
    "parse_topic.json",
    "prefix_topics.json",
    "qualifiers.json",
    "validate.json",
    "validate_topic.json",
];

/// (file, section) pairs skipped until the capability is ported.
const PENDING: &[(&str, &str)] = &[
    // timestamp validation on decode
    ("event_json.json", "decode_reject"),
];

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/bus-v1")
}

fn load<T: DeserializeOwned>(name: &str) -> T {
    assert!(FILES.contains(&name), "{name} is not registered in FILES");
    let p = dir().join(name);
    let raw = fs::read_to_string(&p).unwrap_or_else(|e| panic!("read {}: {e}", p.display()));
    let v: Value = serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {name}: {e}"));
    assert_eq!(v["version"], "v1", "{name}: version");
    serde_json::from_value(v).unwrap_or_else(|e| panic!("{name}: {e}"))
}

/// Collects every mismatch so a run reports all divergences at once.
fn check(fails: Vec<String>) {
    assert!(
        fails.is_empty(),
        "{} divergences:\n{}",
        fails.len(),
        fails.join("\n")
    );
}

fn non_empty<T>(v: &[T], what: &str) {
    assert!(!v.is_empty(), "{what} is empty");
}

/// Error expectation shared by the validator vectors: exact `error`, else
/// every `error_contains` substring (the language-neutral core).
struct Expect<'a> {
    ok: bool,
    error: &'a Option<String>,
    error_contains: &'a Option<Vec<String>>,
}

fn expect_err(fails: &mut Vec<String>, name: &str, want: &Expect<'_>, got: Option<String>) {
    let Some(e) = got else {
        if !want.ok {
            fails.push(format!("{name}: want an error, got none"));
        }
        return;
    };
    if want.ok {
        fails.push(format!("{name}: unexpected error {e:?}"));
    } else if let Some(exact) = &want.error {
        if &e != exact {
            fails.push(format!("{name}:\n   got  {e:?}\n   want {exact:?}"));
        }
    } else if let Some(subs) = want.error_contains.as_deref().filter(|s| !s.is_empty()) {
        for sub in subs {
            if !e.contains(sub.as_str()) {
                fails.push(format!("{name}: error {e:?} lacks {sub:?}"));
            }
        }
    } else {
        fails.push(format!(
            "{name}: failing case has neither error nor error_contains"
        ));
    }
}

macro_rules! file {
    ($name:ident { $($body:tt)* }) => {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        #[allow(dead_code)]
        struct $name {
            #[serde(rename = "$schema")]
            schema: String,
            #[serde(rename = "$id")]
            id: String,
            version: String,
            description: String,
            $($body)*
        }
    };
}

// --- match -------------------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct MatchCase {
    name: String,
    pattern: String,
    topic: String,
    #[serde(rename = "match")]
    matches: bool,
}
file!(MatchFile { cases: Vec<MatchCase> });

#[test]
fn match_vectors() {
    let f: MatchFile = load("match.json");
    non_empty(&f.cases, "cases");
    let mut fails = vec![];
    for c in &f.cases {
        let got = Topic::from(c.topic.as_str()).matches(&c.pattern);
        if got != c.matches {
            fails.push(format!("{}: matches = {got}", c.name));
        }
    }
    check(fails);
}

// --- validators --------------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ValidateCase {
    name: String,
    topic: String,
    ok: bool,
    error: Option<String>,
    error_contains: Option<Vec<String>>,
}
file!(ValidateFile { cases: Vec<ValidateCase> });

fn run_validate(file: &str, f: impl Fn(&Topic) -> Option<String>) {
    let v: ValidateFile = load(file);
    non_empty(&v.cases, "cases");
    let mut fails = vec![];
    for c in &v.cases {
        expect_err(
            &mut fails,
            &c.name,
            &Expect {
                ok: c.ok,
                error: &c.error,
                error_contains: &c.error_contains,
            },
            f(&Topic::from(c.topic.as_str())),
        );
    }
    check(fails);
}

#[test]
fn validate_vectors() {
    run_validate("validate.json", |t| {
        validate(t).err().map(|e| e.to_string())
    });
}

#[test]
fn validate_topic_vectors() {
    run_validate("validate_topic.json", |t| validate_topic(t).err());
}

// --- prefix_topics -----------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PrefixCase {
    name: String,
    prefix: String,
    actions: Vec<String>,
    topics: BTreeMap<String, String>,
    ok: bool,
    error: Option<String>,
    error_contains: Option<Vec<String>>,
}
file!(PrefixFile { cases: Vec<PrefixCase> });

#[test]
fn prefix_topics_vectors() {
    let f: PrefixFile = load("prefix_topics.json");
    non_empty(&f.cases, "cases");
    let mut fails = vec![];
    for c in &f.cases {
        let actions: Vec<&str> = c.actions.iter().map(String::as_str).collect();
        let (map, err) = prefix_topics(&c.prefix, &actions);
        expect_err(
            &mut fails,
            &c.name,
            &Expect {
                ok: c.ok,
                error: &c.error,
                error_contains: &c.error_contains,
            },
            err,
        );
        let got: BTreeMap<String, String> = map
            .into_iter()
            .map(|(k, v)| (k, v.as_str().to_string()))
            .collect();
        if got != c.topics {
            fails.push(format!("{}: map = {got:?}, want {:?}", c.name, c.topics));
        }
    }
    check(fails);
}

// --- mode --------------------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ModeCase {
    name: String,
    input: String,
    mode: String,
    ok: bool,
    error: Option<String>,
}
file!(ModeFile {
    display: BTreeMap<String, String>,
    cases: Vec<ModeCase>
});

#[test]
fn mode_display_vectors() {
    let f: ModeFile = load("mode_parse.json");
    assert_eq!(f.display.len(), 3);
    for (key, mode) in [
        ("off", Mode::Off),
        ("warn", Mode::Warn),
        ("strict", Mode::Strict),
    ] {
        assert_eq!(mode.to_string(), f.display[key]);
    }
}

#[test]
fn mode_parse_vectors() {
    let f: ModeFile = load("mode_parse.json");
    non_empty(&f.cases, "cases");
    let mut fails = vec![];
    for c in &f.cases {
        let want = match c.mode.as_str() {
            "off" => Mode::Off,
            "warn" => Mode::Warn,
            "strict" => Mode::Strict,
            other => panic!("{}: unknown mode {other:?}", c.name),
        };
        let got = c.input.parse::<Mode>();
        // Go returns the fallback mode alongside the error.
        if got.clone().unwrap_or_default() != want {
            fails.push(format!("{}: mode = {got:?}, want {want}", c.name));
        }
        expect_err(
            &mut fails,
            &c.name,
            &Expect {
                ok: c.ok,
                error: &c.error,
                error_contains: &None,
            },
            got.err().map(|e| e.to_string()),
        );
    }
    check(fails);
}

// --- builder and parse_topic -------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct BuildCase {
    name: String,
    source: String,
    category: String,
    object: String,
    ctor_modifiers: Option<Vec<String>>,
    mods: Option<Vec<String>>,
    action: String,
    ok: bool,
    topic: Option<String>,
    error: Option<String>,
    error_contains: Option<Vec<String>>,
}
file!(BuildFile { cases: Vec<BuildCase> });

/// Runs `f`, returning the text it panics with, if any.
fn panic_text<T>(f: impl FnOnce() -> T) -> (Option<T>, Option<String>) {
    match catch_unwind(AssertUnwindSafe(f)) {
        Ok(v) => (Some(v), None),
        Err(p) => {
            let msg = p
                .downcast_ref::<String>()
                .cloned()
                .or_else(|| p.downcast_ref::<&str>().map(|s| s.to_string()))
                .expect("panic payload is text");
            (None, Some(msg))
        }
    }
}

#[test]
fn builder_vectors() {
    let f: BuildFile = load("builder.json");
    non_empty(&f.cases, "cases");
    let mut fails = vec![];
    for c in &f.cases {
        let ctor: Vec<&str> = c
            .ctor_modifiers
            .iter()
            .flatten()
            .map(String::as_str)
            .collect();
        let mut b = prefixed_topic_of(&c.source, &c.category, &c.object, &ctor);
        for m in c.mods.iter().flatten() {
            b = b.modifier(m.as_str());
        }
        let (got, text) = panic_text(|| b.action(&c.action));
        expect_err(
            &mut fails,
            &c.name,
            &Expect {
                ok: c.ok,
                error: &c.error,
                error_contains: &c.error_contains,
            },
            text,
        );
        if let (Some(t), true) = (got, c.ok) {
            if Some(t.as_str()) != c.topic.as_deref() {
                fails.push(format!("{}: topic = {t}, want {:?}", c.name, c.topic));
            }
        }
        // try_action agrees with action and never panics.
        let tried = b.try_action(&c.action);
        if tried.is_ok() != c.ok {
            fails.push(format!("{}: try_action = {tried:?}", c.name));
        }
    }
    check(fails);
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ParseCase {
    name: String,
    topic: String,
    ok: bool,
    source: String,
    category: String,
    object: String,
    modifier: String,
    action: String,
    rerender: Option<String>,
    rerender_error: Option<String>,
    error: Option<String>,
    error_contains: Option<Vec<String>>,
}
file!(ParseFile { cases: Vec<ParseCase> });

#[test]
fn parse_topic_vectors() {
    let f: ParseFile = load("parse_topic.json");
    non_empty(&f.cases, "cases");
    let mut fails = vec![];
    for c in &f.cases {
        let parsed = parse_topic(&c.topic);
        expect_err(
            &mut fails,
            &c.name,
            &Expect {
                ok: c.ok,
                error: &c.error,
                error_contains: &c.error_contains,
            },
            parsed.as_ref().err().map(|e| e.to_string()),
        );
        let Ok((b, action)) = parsed else {
            if let Err(e) = parse_topic(&c.topic) {
                if e.topic.as_str() != c.topic {
                    fails.push(format!("{}: error topic = {}", c.name, e.topic));
                }
            }
            continue;
        };
        let got = (
            b.source_seg(),
            b.category_seg(),
            b.object_seg(),
            b.modifier_seg(),
            action.as_str(),
        );
        let want = (
            c.source.as_str(),
            c.category.as_str(),
            c.object.as_str(),
            c.modifier.as_str(),
            c.action.as_str(),
        );
        if got != want {
            fails.push(format!("{}: parsed {got:?}, want {want:?}", c.name));
        }
        let (re, text) = panic_text(|| b.action(&action));
        match (&c.rerender_error, &text, &re, &c.rerender) {
            (Some(w), Some(t), _, _) if w == t => {}
            (None, None, Some(r), Some(w)) if r.as_str() == w => {}
            _ => fails.push(format!(
                "{}: rerender = {re:?} (panic {text:?}), want {:?} / {:?}",
                c.name, c.rerender, c.rerender_error
            )),
        }
    }
    check(fails);
}

// --- qualifiers --------------------------------------------------------

#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields)]
struct Quals {
    reason: Option<String>,
    mechanism: Option<String>,
    property: Option<String>,
    circumstance: Option<String>,
}

impl Quals {
    fn build(&self) -> Qualifiers {
        let g = |o: &Option<String>| o.clone().unwrap_or_default();
        Qualifiers {
            reason: g(&self.reason),
            mechanism: g(&self.mechanism),
            property: g(&self.property),
            circumstance: g(&self.circumstance),
        }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SerializeCase {
    name: String,
    qualifiers: Quals,
    json: Value,
    is_zero: bool,
}

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
enum Shape {
    Embedded,
    PointerEmbedded,
    Named,
    None,
    Nil,
    Scalar,
    Array,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ExtractCase {
    name: String,
    shape: Shape,
    qualifiers: Quals,
    found: bool,
}
file!(QualFile {
    serialize: Vec<SerializeCase>,
    extract: Vec<ExtractCase>
});

#[test]
fn qualifiers_vectors() {
    let f: QualFile = load("qualifiers.json");
    non_empty(&f.serialize, "serialize");
    non_empty(&f.extract, "extract");
    let mut fails = vec![];
    for c in &f.serialize {
        let q = c.qualifiers.build();
        let got = serde_json::to_value(&q).unwrap();
        if got != c.json {
            fails.push(format!("{}: json = {got}", c.name));
        }
        if q.is_zero() != c.is_zero {
            fails.push(format!("{}: is_zero = {}", c.name, q.is_zero()));
        }
    }
    for c in &f.extract {
        let q = c.qualifiers.build();
        let qv = serde_json::to_value(&q).unwrap();
        let mut payload = match c.shape {
            Shape::Embedded | Shape::PointerEmbedded => qv.clone(),
            Shape::Named => json!({ "qualifiers": qv }),
            Shape::None => json!({}),
            Shape::Nil => Value::Null,
            Shape::Scalar => json!("x"),
            Shape::Array => json!([]),
        };
        if let Some(o) = payload.as_object_mut() {
            o.insert("snapshot_id".into(), json!(""));
        }
        match (c.found, qualifiers_from(&payload)) {
            (true, Some(g)) if g == q => {}
            (false, None) => {}
            (_, g) => fails.push(format!(
                "{}: got {g:?}, want found={} {q:?}",
                c.name, c.found
            )),
        }
    }
    check(fails);
}

// --- event envelope ----------------------------------------------------

#[derive(Deserialize, Clone)]
#[serde(deny_unknown_fields)]
struct WireEvent {
    topic: String,
    source: String,
    timestamp: String,
    payload: Value,
    workspace_id: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "snake_case")]
enum EventMode {
    Roundtrip,
    Decode,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EventCase {
    name: String,
    mode: EventMode,
    event: WireEvent,
    wire_extra: Option<serde_json::Map<String, Value>>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct RejectCase {
    name: String,
    event: Value,
}
file!(EventFile {
    cases: Vec<EventCase>,
    decode_reject: Vec<RejectCase>
});

#[test]
fn event_json_vectors() {
    let f: EventFile = load("event_json.json");
    non_empty(&f.cases, "cases");
    let mut fails = vec![];
    for c in &f.cases {
        let want = &c.event;
        let mut wire = serde_json::to_value(json!({
            "topic": want.topic, "source": want.source,
            "timestamp": want.timestamp, "payload": want.payload,
        }))
        .unwrap();
        if let Some(ws) = &want.workspace_id {
            wire["workspace_id"] = json!(ws);
        }
        let expected = wire.clone();
        for (k, v) in c.wire_extra.iter().flatten() {
            wire[k] = v.clone();
        }
        let dec: Event = match serde_json::from_value(wire) {
            Ok(e) => e,
            Err(e) => {
                fails.push(format!("{}: decode: {e}", c.name));
                continue;
            }
        };
        if dec.topic.as_str() != want.topic
            || dec.source != want.source
            || dec.timestamp != want.timestamp
            || dec.payload != want.payload
            || dec.workspace_id != want.workspace_id.clone().unwrap_or_default()
        {
            fails.push(format!("{}: decoded {dec:?}", c.name));
        }
        if let EventMode::Roundtrip = c.mode {
            let enc = serde_json::to_value(&dec).unwrap();
            if enc != expected {
                fails.push(format!("{}: encoded {enc}, want {expected}", c.name));
            }
        }
    }
    check(fails);
}

// --- pending sections --------------------------------------------------

fn landed(file: &str, section: &str) -> bool {
    match (file, section) {
        ("event_json.json", "decode_reject") => {
            let f: EventFile = load(file);
            f.decode_reject
                .iter()
                .all(|c| serde_json::from_value::<Event>(c.event.clone()).is_err())
        }
        _ => panic!("no probe for pending {file}:{section}"),
    }
}

#[test]
fn pending_sections() {
    for (file, section) in PENDING {
        assert!(FILES.contains(file), "pending file {file} unknown");
        let raw = fs::read_to_string(dir().join(file)).unwrap();
        let v: Value = serde_json::from_str(&raw).unwrap();
        let sec = v[*section]
            .as_array()
            .unwrap_or_else(|| panic!("pending {file}:{section} no longer exists"));
        assert!(!sec.is_empty(), "pending {file}:{section} is empty");
        assert!(
            !landed(file, section),
            "{file}:{section} now passes; remove it from PENDING and assert it"
        );
        println!("SKIP {file}:{section} ({} cases) pending", sec.len());
    }
}

#[test]
fn corpus_files_all_consumed() {
    let mut on_disk: Vec<String> = fs::read_dir(dir())
        .unwrap()
        .map(|e| e.unwrap().file_name().to_string_lossy().into_owned())
        .filter(|n| n.ends_with(".json"))
        .collect();
    on_disk.sort();
    let mut want: Vec<&str> = FILES.to_vec();
    want.sort();
    assert_eq!(on_disk, want, "corpus files and FILES differ");
}
