//! Runs the CloudEvents corpus in `contracts/bus-v1/cloudevents`. Go is
//! canonical: a failure means this port drifted, not that a vector should
//! be edited to fit.
//!
//! Strict like the Go runner: vector files deserialize into typed structs
//! that deny unknown fields, `version` must be `v1`, the corpus directory
//! must match [`FILES`] exactly, and a case may carry only the fields of
//! its outcome.
//!
//! Comparisons follow the corpus README: JSON outputs compare as values
//! (numbers as `f64`, as Go decodes them), `text` compares bytes, and
//! event timestamps compare as instants.

#![cfg(feature = "bus")]

mod bus_cloudevents_support;

use std::fs;
use std::path::PathBuf;

use hop_top_kit::bus::cloudevents::{
    decode_frame, encode_frame, frame_kind, from_cloud_event, to_cloud_event,
    to_cloud_event_binary, CloudEvent, DecodedEvent, FrameFormat,
};
use serde::de::DeserializeOwned;
use serde::Deserialize;
use serde_json::Value;

use bus_cloudevents_support::{is_evt_typeid, json_eq, parse_json, VEvent};

/// Every vector file this runner consumes.
const FILES: &[&str] = &["decode.json", "encode.json", "frames.json"];

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/bus-v1/cloudevents")
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

macro_rules! file {
    ($name:ident { $($body:tt)* }) => {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        #[allow(dead_code)]
        struct $name {
            #[serde(rename = "$schema", default)]
            schema: Option<String>,
            #[serde(rename = "$id", default)]
            id: Option<String>,
            version: String,
            description: String,
            $($body)*
        }
    };
}

// --- corpus ------------------------------------------------------------

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

// --- decode ------------------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DecodeCase {
    name: String,
    input: String,
    ok: bool,
    #[serde(default)]
    error: String,
    canonical: Option<Value>,
    event: Option<VEvent>,
    #[serde(default)]
    event_error: String,
}
file!(DecodeFile { cases: Vec<DecodeCase> });

#[test]
fn decode_vectors() {
    let f: DecodeFile = load("decode.json");
    assert!(!f.cases.is_empty(), "cases is empty");
    let mut fails = vec![];
    for c in &f.cases {
        let n = &c.name;
        if c.ok && !c.error.is_empty() {
            fails.push(format!("{n}: vector: ok case with error"));
            continue;
        }
        if c.ok && c.canonical.is_none() {
            fails.push(format!("{n}: vector: ok case without canonical"));
            continue;
        }
        if !c.ok && (c.canonical.is_some() || c.event.is_some() || !c.event_error.is_empty()) {
            fails.push(format!(
                "{n}: vector: failing case with canonical, event or event_error"
            ));
            continue;
        }
        if c.event.is_some() && !c.event_error.is_empty() {
            fails.push(format!("{n}: vector: event and event_error together"));
            continue;
        }
        let got = CloudEvent::from_json(c.input.as_bytes());
        if !c.ok {
            if c.error.is_empty() {
                fails.push(format!("{n}: vector: failing case without error"));
                continue;
            }
            match got {
                Err(e) if e.to_string() == c.error => {}
                Err(e) => fails.push(format!(
                    "{n}:\n   got  {:?}\n   want {:?}",
                    e.to_string(),
                    c.error
                )),
                Ok(_) => fails.push(format!("{n}: want error {:?}, got none", c.error)),
            }
            continue;
        }
        let ce = match got {
            Ok(ce) => ce,
            Err(e) => {
                fails.push(format!("{n}: decode: {e}"));
                continue;
            }
        };
        match ce.to_json() {
            Ok(out) => {
                let want = c.canonical.as_ref().unwrap();
                if !parse_json(&out).is_some_and(|g| json_eq(&g, want)) {
                    fails.push(format!("{n}: canonical\n   got  {out}\n   want {want}"));
                }
            }
            Err(e) => fails.push(format!("{n}: re-encode: {e}")),
        }
        match from_cloud_event(ce) {
            Err(e) if c.event_error.is_empty() => fails.push(format!("{n}: to event: {e}")),
            Err(e) if e.to_string() != c.event_error => fails.push(format!(
                "{n}: event error\n   got  {:?}\n   want {:?}",
                e.to_string(),
                c.event_error
            )),
            Err(_) => {}
            Ok(_) if !c.event_error.is_empty() => {
                fails.push(format!("{n}: want event error {:?}", c.event_error))
            }
            Ok(got) => {
                if let Some(v) = &c.event {
                    fails.extend(v.diff(&got).into_iter().map(|d| format!("{n}: {d}")));
                }
            }
        }
    }
    check(fails);
}

// --- encode ------------------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EncodeCase {
    name: String,
    event: VEvent,
    ok: bool,
    #[serde(default)]
    error: String,
    cloudevent: Option<Value>,
    #[serde(default)]
    text: String,
    #[serde(default)]
    mint_id: bool,
}
file!(EncodeFile { cases: Vec<EncodeCase> });

#[test]
fn encode_vectors() {
    let f: EncodeFile = load("encode.json");
    assert!(!f.cases.is_empty(), "cases is empty");
    let mut fails = vec![];
    for c in &f.cases {
        let n = &c.name;
        if c.ok && (!c.error.is_empty() || c.cloudevent.is_none()) {
            fails.push(format!(
                "{n}: vector: ok case needs cloudevent and no error"
            ));
            continue;
        }
        if !c.ok && (c.cloudevent.is_some() || !c.text.is_empty() || c.mint_id) {
            fails.push(format!(
                "{n}: vector: failing case with cloudevent, text or mint_id"
            ));
            continue;
        }
        let event = c.event.event();
        let got = match c.event.binary() {
            Some(b) => to_cloud_event_binary(&event, b),
            None => to_cloud_event(&event),
        };
        if !c.ok {
            if c.error.is_empty() {
                fails.push(format!("{n}: vector: failing case without error"));
                continue;
            }
            match got {
                Err(e) if e.to_string() == c.error => {}
                Err(e) => fails.push(format!(
                    "{n}:\n   got  {:?}\n   want {:?}",
                    e.to_string(),
                    c.error
                )),
                Ok(_) => fails.push(format!("{n}: want error {:?}, got none", c.error)),
            }
            continue;
        }
        let ce = match got {
            Ok(ce) => ce,
            Err(e) => {
                fails.push(format!("{n}: {e}"));
                continue;
            }
        };
        if c.mint_id && !is_evt_typeid(&ce.id) {
            fails.push(format!("{n}: minted id {:?} is not an evt TypeID", ce.id));
        }
        let out = match ce.to_json() {
            Ok(out) => out,
            Err(e) => {
                fails.push(format!("{n}: marshal: {e}"));
                continue;
            }
        };
        if !c.text.is_empty() && out != c.text {
            fails.push(format!("{n}: text\n   got  {out}\n   want {}", c.text));
        }
        let mut got = parse_json(&out).unwrap_or(Value::Null);
        if c.mint_id {
            if let Value::Object(m) = &mut got {
                m.remove("id");
            }
        }
        let want = c.cloudevent.as_ref().unwrap();
        if !json_eq(&got, want) {
            fails.push(format!("{n}: cloudevent\n   got  {got}\n   want {want}"));
        }
    }
    check(fails);
}

// --- frames ------------------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FrameDecodeCase {
    name: String,
    frame: String,
    ok: bool,
    #[serde(default)]
    kind: String,
    origin: Option<String>,
    event: Option<VEvent>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FrameEncodeCase {
    name: String,
    format: String,
    origin: String,
    event: VEvent,
    frame: Value,
}
file!(FramesFile {
    decode: Vec<FrameDecodeCase>,
    encode: Vec<FrameEncodeCase>,
});

#[test]
fn frame_vectors() {
    let f: FramesFile = load("frames.json");
    assert!(!f.decode.is_empty(), "decode is empty");
    assert!(!f.encode.is_empty(), "encode is empty");
    let mut fails = vec![];
    for c in &f.decode {
        let n = format!("decode/{}", c.name);
        if c.ok && (c.kind.is_empty() || c.origin.is_none()) {
            fails.push(format!("{n}: vector: ok frame needs kind and origin"));
            continue;
        }
        if !c.ok && (!c.kind.is_empty() || c.origin.is_some() || c.event.is_some()) {
            fails.push(format!(
                "{n}: vector: dropped frame with kind, origin or event"
            ));
            continue;
        }
        let got = decode_frame(c.frame.as_bytes());
        if !c.ok {
            if got.is_ok() {
                fails.push(format!("{n}: want frame dropped"));
            }
            continue;
        }
        let frame = match got {
            Ok(f) => f,
            Err(e) => {
                fails.push(format!("{n}: {e}"));
                continue;
            }
        };
        match frame_kind(c.frame.as_bytes()) {
            Ok(k) if k.to_string() == c.kind => {}
            Ok(k) => fails.push(format!("{n}: kind = {k}, want {}", c.kind)),
            Err(e) => fails.push(format!("{n}: kind: {e}")),
        }
        if frame.format.to_string() != c.kind {
            fails.push(format!(
                "{n}: decoded format = {}, want {}",
                frame.format, c.kind
            ));
        }
        let want_origin = c.origin.as_deref().unwrap();
        if frame.origin != want_origin {
            fails.push(format!(
                "{n}: origin = {:?}, want {want_origin:?}",
                frame.origin
            ));
        }
        if let Some(v) = &c.event {
            let got = DecodedEvent {
                event: frame.event,
                binary: frame.binary,
            };
            fails.extend(v.diff(&got).into_iter().map(|d| format!("{n}: {d}")));
        }
    }
    for c in &f.encode {
        let n = format!("encode/{}", c.name);
        let format = match c.format.as_str() {
            "kit" => FrameFormat::Kit,
            "cloudevents" => FrameFormat::CloudEvents,
            other => {
                fails.push(format!("{n}: unknown format {other:?}"));
                continue;
            }
        };
        match encode_frame(format, &c.event.event(), &c.origin) {
            Ok(out) => {
                if !parse_json(&out).is_some_and(|g| json_eq(&g, &c.frame)) {
                    fails.push(format!("{n}:\n   got  {out}\n   want {}", c.frame));
                }
            }
            Err(e) => fails.push(format!("{n}: {e}")),
        }
    }
    check(fails);
}
