//! Runs the pure stream vectors in `contracts/bus-v1/streams`: offsets,
//! patterns, the delivery filter and the resume decision, and checks that
//! every vector file has a runner. Control frames, the handshake, the
//! cursor, the inbound queue and the outbox have their own
//! `bus_stream_conformance_*.rs`. Go is canonical
//! (`go/runtime/bus/stream_conformance_test.go`): a failure means this
//! port drifted, not that a vector should be edited to fit.
//!
//! Every negative case asserts its exact error code or protocol kind, so
//! no case can pass for the wrong reason.

#![cfg(feature = "bus")]

mod bus_stream_support;

use std::fs;
use std::time::UNIX_EPOCH;

use bus_stream_support::{
    check, dir, load, must_offset, non_empty, stream_file, ConfSub, FILES, WIRE,
};
use hop_top_kit::bus::stream::{
    decide_resume, entry_delivers, format_offset, parse_offset_token, validate_pattern, LogEntry,
    CODE_INVALID_PATTERN,
};
use hop_top_kit::bus::Event;
use serde::Deserialize;
use serde_json::Value;

#[test]
fn stream_conformance_all_files_consumed() {
    let mut seen = Vec::new();
    for e in fs::read_dir(dir()).expect("read corpus") {
        let e = e.unwrap();
        let name = e.file_name().into_string().unwrap();
        if e.file_type().unwrap().is_dir() || !name.ends_with(".json") {
            continue;
        }
        assert!(FILES.contains(&name.as_str()), "{name} has no runner");
        seen.push(name);
    }
    for n in FILES {
        assert!(
            seen.iter().any(|s| s == n),
            "{n} registered but missing from the corpus"
        );
    }
    let mut wire = Vec::new();
    for e in fs::read_dir(dir().join("wire")).expect("read wire") {
        let name = e.unwrap().file_name().into_string().unwrap();
        if name == "README.md" {
            continue;
        }
        assert!(
            name.ends_with(".jsonl"),
            "wire/{name}: only .jsonl transcripts belong here"
        );
        assert!(WIRE.contains(&name.as_str()), "wire/{name} is not listed");
        wire.push(name);
    }
    assert_eq!(wire.len(), WIRE.len(), "wire transcripts: {wire:?}");
}

stream_file!(OffsetsFile { cases: Vec<OffsetCase>, });
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct OffsetCase {
    name: String,
    token: Value,
    ok: bool,
    #[serde(default)]
    value: Option<String>,
}

#[test]
fn stream_conformance_offsets() {
    let f: OffsetsFile = load("offsets.json");
    non_empty(&f.cases, "offsets cases");
    let mut fails = Vec::new();
    for tc in &f.cases {
        let got = parse_offset_token(Some(&tc.token));
        match (got, tc.ok) {
            (Some(v), true) => {
                let want = tc.value.as_deref().expect("vector: ok case needs a value");
                if format_offset(v) != want {
                    fails.push(format!("{}: value {v}, want {want}", tc.name));
                }
            }
            (None, false) => {}
            (g, _) => fails.push(format!("{}: got {g:?}, want ok {}", tc.name, tc.ok)),
        }
    }
    check(fails);
}

stream_file!(PatternsFile { cases: Vec<PatternCase>, });
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PatternCase {
    name: String,
    pattern: String,
    ok: bool,
}

#[test]
fn stream_conformance_patterns() {
    let f: PatternsFile = load("patterns.json");
    non_empty(&f.cases, "patterns cases");
    let mut fails = Vec::new();
    for tc in &f.cases {
        match (validate_pattern(&tc.pattern), tc.ok) {
            (Ok(()), true) => {}
            (Err(e), false) if e.code == CODE_INVALID_PATTERN => {}
            (got, _) => fails.push(format!("{}: got {got:?}, want ok {}", tc.name, tc.ok)),
        }
    }
    check(fails);
}

stream_file!(FilterFile { cases: Vec<FilterCase>, });
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FilterCase {
    name: String,
    subscription: ConfSub,
    peer_origin: String,
    entry: FilterEntry,
    deliver: bool,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FilterEntry {
    #[serde(default)]
    marker: bool,
    #[serde(default)]
    origin: String,
    #[serde(default)]
    topic: String,
    #[serde(default)]
    workspace_id: String,
}

#[test]
fn stream_conformance_filter() {
    let f: FilterFile = load("filter.json");
    non_empty(&f.cases, "filter cases");
    let mut fails = Vec::new();
    for tc in &f.cases {
        let e = LogEntry {
            offset: 0,
            event: Event::new_at(tc.entry.topic.as_str(), "", Value::Null, UNIX_EPOCH)
                .with_workspace_id(tc.entry.workspace_id.as_str()),
            origin: tc.entry.origin.clone(),
            marker: tc.entry.marker,
        };
        let got = entry_delivers(&tc.subscription.state(), &tc.peer_origin, &e);
        if got != tc.deliver {
            fails.push(format!("{}: deliver {got}, want {}", tc.name, tc.deliver));
        }
    }
    check(fails);
}

stream_file!(ResumeFile { cases: Vec<ResumeCase>, });
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ResumeCase {
    name: String,
    log: ResumeLog,
    #[serde(default)]
    request: Option<ResumeRequest>,
    outcome: Option<ResumeOutcomeV>,
    cursor: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ResumeLog {
    stream: String,
    oldest: String,
    head: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ResumeRequest {
    stream: String,
    after: String,
}
#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct ResumeOutcomeV {
    status: String,
    #[serde(default)]
    reason: String,
    after: String,
    head: String,
}

#[test]
fn stream_conformance_resume() {
    let f: ResumeFile = load("resume.json");
    non_empty(&f.cases, "resume cases");
    let mut fails = Vec::new();
    for tc in &f.cases {
        let req = tc
            .request
            .as_ref()
            .map(|r| hop_top_kit::bus::stream::ResumeReq {
                stream: r.stream.clone(),
                after: must_offset(&r.after),
            });
        let (ro, cursor) = decide_resume(
            &tc.log.stream,
            must_offset(&tc.log.oldest),
            must_offset(&tc.log.head),
            req.as_ref(),
        );
        if format_offset(cursor) != tc.cursor {
            fails.push(format!("{}: cursor {cursor}, want {}", tc.name, tc.cursor));
        }
        let got = ro.map(|o| ResumeOutcomeV {
            status: o.status.as_str().to_string(),
            reason: o.reason,
            after: format_offset(o.after),
            head: format_offset(o.head),
        });
        if got != tc.outcome {
            fails.push(format!(
                "{}: outcome\n   got  {got:?}\n   want {:?}",
                tc.name, tc.outcome
            ));
        }
    }
    check(fails);
}
