//! Runs the shared blob corpus in `contracts/blob-v1`. The vectors are
//! the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited to
//! fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
#![cfg(feature = "blob")]

use std::fs;
use std::io::Read;
use std::path::PathBuf;

use hop_top_kit::blob::local::LocalStore;
use hop_top_kit::blob::{BlobError, Store};
use serde::Deserialize;
use tempfile::TempDir;

const FILES: &[&str] = &["blob.json"];

#[derive(Deserialize, PartialEq, Debug)]
#[serde(deny_unknown_fields)]
struct Obj {
    key: String,
    size: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ErrCase {
    kind: String,
    #[serde(default)]
    text: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Step {
    op: String,
    #[serde(default)]
    key: String,
    #[serde(default)]
    path: String,
    #[serde(default)]
    prefix: String,
    #[serde(default)]
    data: String,
    #[serde(default)]
    data_hex: String,
    #[serde(default)]
    #[allow(dead_code)]
    content_type: String,
    want_data: Option<String>,
    want_hex: Option<String>,
    want_bool: Option<bool>,
    want_objects: Option<Vec<Obj>>,
    error: Option<ErrCase>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Scenario {
    name: String,
    steps: Vec<Step>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct Corpus {
    #[serde(rename = "$schema")]
    schema: String,
    #[serde(rename = "$id")]
    id: String,
    version: String,
    description: String,
    source: String,
    scenarios: Vec<Scenario>,
}

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/blob-v1")
}

fn corpus() -> Corpus {
    let mut found: Vec<String> = fs::read_dir(dir())
        .expect("corpus dir")
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    found.sort();
    assert_eq!(found, FILES, "corpus files without a runner");
    let raw = fs::read_to_string(dir().join("blob.json")).unwrap();
    let c: Corpus = serde_json::from_str(&raw).expect("decode blob.json");
    assert_eq!(c.version, "v1");
    assert!(!c.description.is_empty() && !c.scenarios.is_empty());
    c
}

fn hex_decode(s: &str) -> Vec<u8> {
    assert!(s.len().is_multiple_of(2), "odd hex length");
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("hex"))
        .collect()
}

fn hex_encode(b: &[u8]) -> String {
    b.iter().map(|x| format!("{x:02x}")).collect()
}

fn payload(s: &Step) -> Vec<u8> {
    if s.data_hex.is_empty() {
        s.data.clone().into_bytes()
    } else {
        hex_decode(&s.data_hex)
    }
}

/// Check `r` against the step's expected error. Returns the Ok value
/// when no error is expected and none occurred, `None` when the
/// expected error occurred.
fn check<T>(where_: &str, want: &Option<ErrCase>, r: Result<T, BlobError>) -> Option<T> {
    match (want, r) {
        (None, Ok(v)) => Some(v),
        (None, Err(e)) => panic!("{where_}: unexpected error: {e}"),
        (Some(w), Ok(_)) => panic!("{where_}: want {} error, got none", w.kind),
        (Some(w), Err(e)) => {
            match w.kind.as_str() {
                "not_found" => assert!(
                    matches!(e, BlobError::NotFound(_)),
                    "{where_}: want not_found, got {e}"
                ),
                "escapes_root" => assert_eq!(e.to_string(), w.text, "{where_}"),
                other => panic!("{where_}: unknown error kind {other:?}"),
            }
            None
        }
    }
}

#[test]
fn scenarios() {
    for sc in corpus().scenarios {
        let tmp = TempDir::new().expect("tempdir");
        let s = LocalStore::new(tmp.path()).expect("new store");
        for (i, st) in sc.steps.iter().enumerate() {
            let w = format!("{}#{} (step {i})", sc.name, st.op);
            match st.op.as_str() {
                "put" => {
                    let r = s.put(&st.key, payload(st).as_slice(), &st.content_type);
                    check(&w, &st.error, r);
                }
                "get" => {
                    let Some(mut rc) = check(&w, &st.error, s.get(&st.key)) else {
                        continue;
                    };
                    let mut got = Vec::new();
                    rc.read_to_end(&mut got).expect("read");
                    if let Some(h) = &st.want_hex {
                        assert_eq!(&hex_encode(&got), h, "{w}");
                    } else if let Some(d) = &st.want_data {
                        assert_eq!(String::from_utf8_lossy(&got), d.as_str(), "{w}");
                    } else {
                        panic!("{w}: get needs want_data or want_hex");
                    }
                }
                "delete" => {
                    check(&w, &st.error, s.delete(&st.key));
                }
                "exists" => {
                    if let Some(ok) = check(&w, &st.error, s.exists(&st.key)) {
                        assert_eq!(Some(ok), st.want_bool, "{w}");
                    }
                }
                "list" => {
                    let Some(objs) = check(&w, &st.error, s.list(&st.prefix)) else {
                        continue;
                    };
                    let got: Vec<Obj> = objs
                        .into_iter()
                        .map(|o| Obj {
                            key: o.key,
                            size: o.size,
                        })
                        .collect();
                    let want = st.want_objects.as_ref().expect("list needs want_objects");
                    assert_eq!(&got, want, "{w}");
                }
                "raw_write" => {
                    let p = tmp.path().join(&st.path);
                    fs::create_dir_all(p.parent().unwrap()).unwrap();
                    fs::write(p, &st.data).unwrap();
                }
                other => panic!("{w}: unknown op {other:?}"),
            }
        }
    }
}
