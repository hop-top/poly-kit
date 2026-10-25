//! Runs the shared netpolicy corpus in `contracts/netpolicy-v1`. The
//! vectors are the source of truth: a failure means this port drifted
//! from the contract (Go is canonical), not that a vector should be
//! edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly. `is_loopback` and
//! `check_dial` run under `netpolicy`; `url_allows` and `refusal` need
//! the reqwest client and run under `api`.
#![cfg(feature = "netpolicy")]

use std::fs;
use std::path::PathBuf;

use hop_top_kit::netpolicy::{is_loopback, NetPolicy, OFFLINE_MESSAGE};
use serde::Deserialize;

const FILES: &[&str] = &["vectors.json"];

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct LoopbackCase {
    name: String,
    host: String,
    loopback: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DialCase {
    name: String,
    offline: bool,
    network: String,
    addr: String,
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[cfg_attr(not(feature = "api"), allow(dead_code))]
struct UrlCase {
    name: String,
    url: String,
    allowed: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[cfg_attr(not(feature = "api"), allow(dead_code))]
struct RefusalCase {
    name: String,
    method: String,
    url: String,
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Vectors {
    version: String,
    #[allow(dead_code)]
    description: String,
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    offline_message: String,
    is_loopback: Vec<LoopbackCase>,
    check_dial: Vec<DialCase>,
    #[cfg_attr(not(feature = "api"), allow(dead_code))]
    url_allows: Vec<UrlCase>,
    #[cfg_attr(not(feature = "api"), allow(dead_code))]
    refusal: Vec<RefusalCase>,
}

fn load() -> Vectors {
    let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/netpolicy-v1");
    let mut found: Vec<String> = fs::read_dir(&dir)
        .expect("read corpus dir")
        .map(|e| e.expect("dir entry").file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    found.sort();
    assert_eq!(found, FILES, "corpus files without a runner");
    let raw = fs::read_to_string(dir.join("vectors.json")).expect("read vectors.json");
    let v: Vectors = serde_json::from_str(&raw).expect("parse vectors.json");
    assert_eq!(v.version, "v1");
    v
}

#[test]
fn offline_message() {
    assert_eq!(OFFLINE_MESSAGE, load().offline_message);
}

#[test]
fn is_loopback_vectors() {
    let cases = load().is_loopback;
    assert!(!cases.is_empty(), "is_loopback section is empty");
    for c in cases {
        assert_eq!(is_loopback(&c.host), c.loopback, "{}: {:?}", c.name, c.host);
    }
}

#[test]
fn check_dial_vectors() {
    let cases = load().check_dial;
    assert!(!cases.is_empty(), "check_dial section is empty");
    for c in cases {
        let got = NetPolicy::new(c.offline).check_dial(&c.network, &c.addr);
        let got = got.err().map(|e| e.to_string()).unwrap_or_default();
        assert_eq!(got, c.error, "{}", c.name);
    }
}

#[cfg(feature = "api")]
#[test]
fn url_allows_vectors() {
    let cases = load().url_allows;
    assert!(!cases.is_empty(), "url_allows section is empty");
    for c in cases {
        let url: reqwest::Url = c.url.parse().expect("vector url parses");
        assert_eq!(
            NetPolicy::offline().allows(&url),
            c.allowed,
            "{}: {}",
            c.name,
            c.url
        );
    }
}

#[cfg(feature = "api")]
#[tokio::test]
async fn refusal_vectors() {
    use hop_top_kit::netpolicy::GuardedClient;

    let cases = load().refusal;
    assert!(!cases.is_empty(), "refusal section is empty");
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    for c in cases {
        let method: reqwest::Method = c.method.parse().expect("vector method");
        let err = client
            .request(method, c.url.as_str())
            .send()
            .await
            .expect_err("offline request was sent");
        assert!(err.is_offline(), "{}: not a refusal: {err}", c.name);
        assert_eq!(err.to_string(), c.error, "{}", c.name);
    }
}
