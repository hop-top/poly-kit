//! Runs the shared bus corpus in `contracts/bus-v1`. The vectors are the
//! source of truth: a failure means this port drifted from the contract
//! (Go is canonical), not that a vector should be edited to fit.
//!
//! Strict by construction: vector files deserialize into typed structs
//! that deny unknown fields, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
//!
//! Nothing is skipped. A section the port cannot satisfy yet is listed in a
//! `PENDING` constant of (file, section) pairs, skipped with a printed
//! line, and guarded by a probe that fails once the section passes.

#![cfg(feature = "bus")]

use std::collections::BTreeMap;
use std::fs;
use std::panic::{catch_unwind, AssertUnwindSafe};
use std::path::PathBuf;

use hop_top_kit::bus::{
    mode_from_string, parse_topic, prefix_topics, prefixed_topic_of, qualifiers_from, validate,
    validate_topic, Event, Mode, Qualifiers, Topic,
};
use serde::de::DeserializeOwned;
use serde::Deserialize;
use serde_json::{json, Value};

/// Every vector file this runner consumes.
const FILES: &[&str] = &[
    "builder.json",
    "event_extensions.json",
    "event_id.json",
    "event_json.json",
    "event_timestamp.json",
    "match.json",
    "mode_parse.json",
    "parse_topic.json",
    "prefix_topics.json",
    "qualifiers.json",
    "validate.json",
    "validate_topic.json",
];

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/bus-v1")
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
        let (got, err) = mode_from_string(&c.input);
        if got != want {
            fails.push(format!("{}: mode = {got}, want {want}", c.name));
        }
        let parsed = c.input.parse::<Mode>();
        if parsed.is_ok() != err.is_none() || parsed.as_ref().ok().is_some_and(|m| *m != want) {
            fails.push(format!("{}: from_str = {parsed:?}", c.name));
        }
        expect_err(
            &mut fails,
            &c.name,
            &Expect {
                ok: c.ok,
                error: &c.error,
                error_contains: &None,
            },
            err.map(|e| e.to_string()),
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
        let err_topic = parsed.as_ref().err().map(|e| e.topic.as_str().to_string());
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
            if err_topic.as_deref() != Some(c.topic.as_str()) {
                fails.push(format!("{}: error topic = {err_topic:?}", c.name));
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
    event: serde_json::Map<String, Value>,
    wire_extra: Option<serde_json::Map<String, Value>>,
    text: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
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
    non_empty(&f.decode_reject, "decode_reject");
    const KNOWN: &[&str] = &[
        "topic",
        "source",
        "timestamp",
        "payload",
        "workspace_id",
        "id",
        "subject",
        "datacontenttype",
        "dataschema",
        "extensions",
    ];
    let mut fails = vec![];
    for c in &f.cases {
        if let Some(k) = c.event.keys().find(|k| !KNOWN.contains(&k.as_str())) {
            fails.push(format!("{}: unknown event key {k:?}", c.name));
            continue;
        }
        let expected = Value::Object(c.event.clone());
        let mut wire = c.event.clone();
        for (k, v) in c.wire_extra.iter().flatten() {
            wire.insert(k.clone(), v.clone());
        }
        let dec: Event = match serde_json::from_value(Value::Object(wire)) {
            Ok(e) => e,
            Err(e) => {
                fails.push(format!("{}: decode: {e}", c.name));
                continue;
            }
        };
        let enc = serde_json::to_value(&dec).unwrap();
        if enc != expected {
            fails.push(format!("{}: encoded {enc}, want {expected}", c.name));
        }
        if let Some(text) = &c.text {
            let got = serde_json::to_string(&dec).unwrap();
            if &got != text {
                fails.push(format!("{}: text\n   got  {got}\n   want {text}", c.name));
            }
        }
        if let EventMode::Roundtrip = c.mode {
            if c.wire_extra.is_some() {
                fails.push(format!("{}: wire_extra needs mode decode", c.name));
            }
        }
    }
    for c in &f.decode_reject {
        if serde_json::from_value::<Event>(c.event.clone()).is_ok() {
            fails.push(format!("{}: decode succeeded, want error", c.name));
        }
    }
    check(fails);
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TsCase {
    name: String,
    timestamp: String,
    canonical: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TsReject {
    name: String,
    timestamp: Value,
    error_contains: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TsDefault {
    name: String,
    event: Value,
    timestamp: String,
}
file!(TimestampFile {
    cases: Vec<TsCase>,
    reject: Vec<TsReject>,
    defaults: Vec<TsDefault>
});

#[test]
fn event_timestamp_vectors() {
    let f: TimestampFile = load("event_timestamp.json");
    non_empty(&f.cases, "cases");
    non_empty(&f.reject, "reject");
    non_empty(&f.defaults, "defaults");
    let mut fails = vec![];
    for c in &f.cases {
        match serde_json::from_value::<Event>(json!({ "timestamp": c.timestamp })) {
            Ok(e) if e.timestamp == c.canonical => {}
            Ok(e) => fails.push(format!(
                "{}: re-encoded {:?}, want {:?}",
                c.name, e.timestamp, c.canonical
            )),
            Err(e) => fails.push(format!("{}: decode: {e}", c.name)),
        }
    }
    for c in &f.reject {
        match serde_json::from_value::<Event>(json!({ "timestamp": c.timestamp })) {
            Ok(_) => fails.push(format!("{}: decode succeeded, want error", c.name)),
            Err(e) => {
                let want = c.error_contains.as_deref().unwrap_or("");
                if !e.to_string().contains(want) {
                    fails.push(format!("{}: error {e} lacks {want:?}", c.name));
                }
            }
        }
    }
    for c in &f.defaults {
        match serde_json::from_value::<Event>(c.event.clone()) {
            Ok(e) if e.timestamp == c.timestamp => {}
            Ok(e) => fails.push(format!(
                "{}: timestamp {:?}, want {:?}",
                c.name, e.timestamp, c.timestamp
            )),
            Err(e) => fails.push(format!("{}: decode: {e}", c.name)),
        }
    }
    check(fails);
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ExtCase {
    name: String,
    workspace_id: Option<String>,
    extensions: serde_json::Map<String, Value>,
    error: Option<String>,
}
file!(ExtFile {
    cases: Vec<ExtCase>
});

#[test]
fn event_extension_vectors() {
    let f: ExtFile = load("event_extensions.json");
    non_empty(&f.cases, "cases");
    let mut fails = vec![];
    for c in &f.cases {
        let mut e = Event::new_at("a.b.c.d", "s", json!(null), std::time::UNIX_EPOCH);
        e.workspace_id = c.workspace_id.clone().unwrap_or_default();
        e.extensions = c.extensions.clone().into_iter().collect();
        let got = e.validate_extensions().err().map(|e| e.to_string());
        match (&c.error, got) {
            (None, None) => {}
            (None, Some(g)) => fails.push(format!("{}: unexpected error {g:?}", c.name)),
            (Some(w), None) => fails.push(format!("{}: want error {w:?}, got none", c.name)),
            (Some(w), Some(g)) if *w != g => {
                fails.push(format!("{}:\n   got  {g:?}\n   want {w:?}", c.name))
            }
            _ => {}
        }
    }
    check(fails);
}

file!(IdFile {
    prefix: String,
    suffix_length: usize,
    alphabet: String,
    first_char_max: String,
    uuid_version: u8,
    distinct: usize
});

/// Decodes a 26-char TypeID suffix into its 16 UUID bytes.
fn suffix_bytes(suffix: &str, alphabet: &str) -> [u8; 16] {
    let mut acc: u128 = 0;
    for ch in suffix.chars() {
        acc = (acc << 5) | alphabet.find(ch).unwrap() as u128;
    }
    acc.to_be_bytes()
}

fn check_event_id(f: &IdFile, label: &str, s: &str, fails: &mut Vec<String>) {
    let Some(suffix) = s.strip_prefix(f.prefix.as_str()) else {
        fails.push(format!("{label}: {s:?} lacks prefix {:?}", f.prefix));
        return;
    };
    if suffix.chars().count() != f.suffix_length
        || suffix.chars().any(|c| !f.alphabet.contains(c))
        || suffix.as_bytes()[0] > f.first_char_max.as_bytes()[0]
    {
        fails.push(format!("{label}: {s:?} is not a valid suffix"));
        return;
    }
    let b = suffix_bytes(suffix, &f.alphabet);
    if b[6] >> 4 != f.uuid_version || b[8] >> 6 != 0b10 {
        fails.push(format!("{label}: {s:?} uuid version/variant {b:02x?}"));
    }
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[test]
fn event_id_vectors() {
    let f: IdFile = load("event_id.json");
    let mut fails = vec![];
    let mut seen = std::collections::BTreeSet::new();
    for _ in 0..f.distinct {
        let e = Event::new("a.b.c.d", "s", json!(null));
        check_event_id(&f, "Event::new", &e.id, &mut fails);
        if !seen.insert(e.id.clone()) {
            fails.push(format!("duplicate id {:?}", e.id));
        }
    }
    check(fails);
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
