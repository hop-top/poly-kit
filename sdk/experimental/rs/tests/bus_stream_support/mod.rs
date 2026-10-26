//! Shared loader for the stream runners in `contracts/bus-v1/streams`.
//!
//! Strict like the Go runner: vector files deserialize into typed structs
//! that deny unknown fields, `version` must be `v1` with a non-empty
//! `description`, and the corpus must match [`FILES`] exactly.

#![allow(dead_code)]

pub mod client;

use std::fs;
use std::path::PathBuf;

use hop_top_kit::bus::stream::{Scope, SubState};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Deserializer};
use serde_json::Value;

/// Every vector file a Rust runner consumes.
pub const FILES: &[&str] = &[
    "control.json",
    "cursor.json",
    "event_frames.json",
    "filter.json",
    "handshake.json",
    "inbound.json",
    "offsets.json",
    "outbox.json",
    "patterns.json",
    "resume.json",
];

/// Wire transcripts replay against a live acceptor over a WebSocket: the
/// network half runs them, not the I/O-free core.
pub const WIRE: &[&str] = &[
    "plain_ack.jsonl",
    "replay.jsonl",
    "reset_ack.jsonl",
    "subscribe.jsonl",
];

pub fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/bus-v1/streams")
}

/// Loads `name` strictly: a registered file, `version` v1, a description,
/// and no unknown fields at any level.
pub fn load<T: DeserializeOwned>(name: &str) -> T {
    assert!(FILES.contains(&name), "{name} is not registered");
    let p = dir().join(name);
    let raw = fs::read_to_string(&p).unwrap_or_else(|e| panic!("read {}: {e}", p.display()));
    let v: Value = serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {name}: {e}"));
    assert_eq!(v["version"], "v1", "{name}: version");
    assert!(
        v["description"].as_str().is_some_and(|d| !d.is_empty()),
        "{name}: description"
    );
    serde_json::from_value(v).unwrap_or_else(|e| panic!("{name}: {e}"))
}

/// Collects every mismatch so a run reports all divergences at once.
pub fn check(fails: Vec<String>) {
    assert!(
        fails.is_empty(),
        "{} divergences:\n{}",
        fails.len(),
        fails.join("\n")
    );
}

pub fn non_empty<T>(v: &[T], what: &str) {
    assert!(!v.is_empty(), "{what} is empty");
}

/// Deserializes a member that may be JSON null into `Some(Value::Null)`;
/// with `#[serde(default)]` an absent member stays `None`. Go's runner
/// tells the two apart through `len(json.RawMessage) > 0`.
pub fn present<'de, D: Deserializer<'de>>(d: D) -> Result<Option<Value>, D::Error> {
    Value::deserialize(d).map(Some)
}

/// The header every stream vector file carries.
#[allow(unused_macros)]
macro_rules! stream_file {
    ($name:ident { $($body:tt)* }) => {
        #[derive(serde::Deserialize)]
        #[serde(deny_unknown_fields)]
        #[allow(dead_code)]
        struct $name {
            version: String,
            description: String,
            $($body)*
        }
    };
}
#[allow(unused_imports)]
pub(crate) use stream_file;

/// A scope in vector form.
#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct VScope {
    pub workspaces: Vec<String>,
}

/// Parses a vector offset, failing the run on an invalid one.
pub fn must_offset(s: &str) -> u64 {
    hop_top_kit::bus::stream::parse_offset(s)
        .unwrap_or_else(|| panic!("vector offset {s:?} is invalid"))
}

/// A subscription state in vector form; `patterns` null is the implicit
/// `#`.
#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct ConfSub {
    #[serde(default)]
    pub patterns: Option<Vec<String>>,
    #[serde(default)]
    pub scope: Option<VScope>,
}

pub fn scope_of(v: &Option<VScope>) -> Option<Scope> {
    v.as_ref().map(|s| Scope {
        workspaces: s.workspaces.clone(),
    })
}

pub fn vscope_of(s: &Option<Scope>) -> Option<VScope> {
    s.as_ref().map(|s| VScope {
        workspaces: s.workspaces.clone(),
    })
}

impl ConfSub {
    pub fn state(&self) -> SubState {
        let mut s = SubState::new();
        if let Some(p) = &self.patterns {
            s = s.with_patterns(p.iter().cloned());
        }
        s.scope = scope_of(&self.scope);
        s
    }

    pub fn of(s: &SubState) -> ConfSub {
        ConfSub {
            patterns: (!s.implicit).then(|| s.sorted_patterns()),
            scope: vscope_of(&s.scope),
        }
    }
}
