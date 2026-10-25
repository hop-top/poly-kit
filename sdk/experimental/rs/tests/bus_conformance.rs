//! Runs the shared bus corpus in `contracts/bus-v1`. The vectors are the
//! source of truth: a failure means this port drifted from the contract
//! (Go is canonical), not that a vector should be edited to fit.
//!
//! pending (capability not ported yet; flip each to a real assertion when
//! it lands):
//! - mode parsing: `mode_parse.json` `cases` (string parser, error text)
//! - event timestamp validation: `event_json.json` `decode_reject`

#![cfg(feature = "bus")]

use std::collections::BTreeMap;
use std::fs;
use std::path::PathBuf;

use hop_top_kit::bus::{
    prefix_topics, qualifiers_from, validate, validate_topic, Event, Mode, Qualifiers, Topic,
};
use serde_json::{json, Value};

fn load(name: &str) -> Value {
    let p = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../contracts/bus-v1")
        .join(name);
    let raw = fs::read_to_string(&p).unwrap_or_else(|e| panic!("read {}: {e}", p.display()));
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {}: {e}", p.display()))
}

fn arr<'a>(v: &'a Value, key: &str) -> &'a Vec<Value> {
    let a = v[key].as_array().unwrap_or_else(|| panic!("{key} missing"));
    assert!(!a.is_empty(), "{key} is empty");
    a
}

fn s<'a>(v: &'a Value, key: &str) -> &'a str {
    v[key]
        .as_str()
        .unwrap_or_else(|| panic!("{key} not a string"))
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

/// Asserts an error against a case: exact text when the vector has `error`,
/// else every `error_contains` substring (the language-neutral core).
fn expect_err(fails: &mut Vec<String>, name: &str, case: &Value, got: Option<String>) {
    let ok = case["ok"].as_bool().unwrap();
    let Some(e) = got else {
        if !ok {
            fails.push(format!("{name}: want an error, got none"));
        }
        return;
    };
    if ok {
        fails.push(format!("{name}: unexpected error {e:?}"));
    } else if let Some(want) = case["error"].as_str() {
        if e != want {
            fails.push(format!("{name}:\n   got  {e:?}\n   want {want:?}"));
        }
    } else {
        for sub in arr(case, "error_contains") {
            if !e.contains(sub.as_str().unwrap()) {
                fails.push(format!("{name}: error {e:?} lacks {sub}"));
            }
        }
    }
}

#[test]
fn match_vectors() {
    let f = load("match.json");
    let mut fails = vec![];
    for c in arr(&f, "cases") {
        let got = Topic::from(s(c, "topic")).matches(s(c, "pattern"));
        if got != c["match"].as_bool().unwrap() {
            fails.push(format!("{}: matches = {got}", s(c, "name")));
        }
    }
    check(fails);
}

#[test]
fn validate_vectors() {
    let f = load("validate.json");
    let mut fails = vec![];
    for c in arr(&f, "cases") {
        let got = validate(&Topic::from(s(c, "topic")))
            .err()
            .map(|e| e.to_string());
        expect_err(&mut fails, s(c, "name"), c, got);
    }
    check(fails);
}

#[test]
fn validate_topic_vectors() {
    let f = load("validate_topic.json");
    let mut fails = vec![];
    for c in arr(&f, "cases") {
        let got = validate_topic(&Topic::from(s(c, "topic"))).err();
        expect_err(&mut fails, s(c, "name"), c, got);
    }
    check(fails);
}

#[test]
fn prefix_topics_vectors() {
    let f = load("prefix_topics.json");
    let mut fails = vec![];
    for c in arr(&f, "cases") {
        let actions: Vec<&str> = c["actions"]
            .as_array()
            .unwrap()
            .iter()
            .map(|a| a.as_str().unwrap())
            .collect();
        let (map, err) = prefix_topics(s(c, "prefix"), &actions);
        let name = s(c, "name");
        expect_err(&mut fails, name, c, err);
        let want: BTreeMap<String, String> = c["topics"]
            .as_object()
            .unwrap()
            .iter()
            .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_string()))
            .collect();
        let got: BTreeMap<String, String> = map
            .into_iter()
            .map(|(k, v)| (k, v.as_str().to_string()))
            .collect();
        if got != want {
            fails.push(format!("{name}: map = {got:?}, want {want:?}"));
        }
    }
    check(fails);
}

#[test]
fn mode_display_vectors() {
    let f = load("mode_parse.json");
    for (key, mode) in [
        ("off", Mode::Off),
        ("warn", Mode::Warn),
        ("strict", Mode::Strict),
    ] {
        assert_eq!(mode.to_string(), s(&f["display"], key));
    }
    assert!(!arr(&f, "cases").is_empty());
    // pending: mode parsing (`cases` need a string parser)
}

fn quals(v: &Value) -> Qualifiers {
    let g = |k: &str| v[k].as_str().unwrap_or("").to_string();
    Qualifiers {
        reason: g("reason"),
        mechanism: g("mechanism"),
        property: g("property"),
        circumstance: g("circumstance"),
    }
}

#[test]
fn qualifiers_vectors() {
    let f = load("qualifiers.json");
    let mut fails = vec![];
    for c in arr(&f, "serialize") {
        let name = s(c, "name");
        let q = quals(&c["qualifiers"]);
        if serde_json::to_value(&q).unwrap() != c["json"] {
            fails.push(format!(
                "{name}: json = {}",
                serde_json::to_value(&q).unwrap()
            ));
        }
        if q.is_zero() != c["is_zero"].as_bool().unwrap() {
            fails.push(format!("{name}: is_zero = {}", q.is_zero()));
        }
    }
    for c in arr(&f, "extract") {
        let name = s(c, "name");
        let q = quals(&c["qualifiers"]);
        let qv = serde_json::to_value(&q).unwrap();
        let mut payload = match s(c, "shape") {
            "embedded" | "pointer_embedded" => qv.clone(),
            "named" => json!({ "qualifiers": qv }),
            "none" => json!({}),
            "nil" => Value::Null,
            "scalar" => json!("x"),
            "array" => json!([]),
            other => panic!("{name}: unknown shape {other}"),
        };
        if let Some(o) = payload.as_object_mut() {
            o.insert("snapshot_id".into(), json!(""));
        }
        let got = qualifiers_from(&payload);
        let found = c["found"].as_bool().unwrap();
        match (found, got) {
            (true, Some(g)) if g == q => {}
            (false, None) => {}
            (_, g) => fails.push(format!("{name}: got {g:?}, want found={found} {q:?}")),
        }
    }
    check(fails);
}

#[test]
fn event_json_vectors() {
    let f = load("event_json.json");
    let mut fails = vec![];
    for c in arr(&f, "cases") {
        let name = s(c, "name");
        let want = &c["event"];
        let mut wire = want.clone();
        if let Some(extra) = c["wire_extra"].as_object() {
            for (k, v) in extra {
                wire[k] = v.clone();
            }
        }
        let dec: Event = match serde_json::from_value(wire) {
            Ok(e) => e,
            Err(e) => {
                fails.push(format!("{name}: decode: {e}"));
                continue;
            }
        };
        if dec.topic.as_str() != s(want, "topic")
            || dec.source != s(want, "source")
            || dec.timestamp != s(want, "timestamp")
            || dec.payload != want["payload"]
            || dec.workspace_id != want["workspace_id"].as_str().unwrap_or("")
        {
            fails.push(format!("{name}: decoded {dec:?}"));
        }
        if s(c, "mode") == "roundtrip" {
            let enc = serde_json::to_value(&dec).unwrap();
            if &enc != want {
                fails.push(format!("{name}: encoded {enc}, want {want}"));
            }
        }
    }
    assert!(!arr(&f, "decode_reject").is_empty());
    // pending: event timestamp validation (`decode_reject`)
    check(fails);
}
