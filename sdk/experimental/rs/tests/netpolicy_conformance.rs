//! Runs the shared netpolicy corpus in `contracts/netpolicy-v1`. The
//! vectors are the source of truth: a failure means this port drifted
//! from the contract (Go is canonical), not that a vector should be
//! edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly. `is_loopback` and
//! `check_dial` run under `netpolicy`; `url_allows` and `refusal` need
//! the reqwest client and run under `api`. The `allow_*` and
//! `host_allowed` sections pin the host allowlist the same way.
#![cfg(feature = "netpolicy")]

use std::fs;
use std::path::PathBuf;

use hop_top_kit::netpolicy::{is_loopback, NetPolicy, HOST_NOT_ALLOWED_MESSAGE, OFFLINE_MESSAGE};
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
struct HostCase {
    name: String,
    allow_hosts: Vec<String>,
    host: String,
    allowed: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AllowDialCase {
    name: String,
    allow_hosts: Vec<String>,
    network: String,
    addr: String,
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[cfg_attr(not(feature = "api"), allow(dead_code))]
struct AllowUrlCase {
    name: String,
    allow_hosts: Vec<String>,
    method: String,
    url: String,
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[cfg_attr(not(feature = "api"), allow(dead_code))]
struct RedirectCase {
    name: String,
    cross_origin: bool,
    headers: std::collections::BTreeMap<String, String>,
    refused: bool,
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
    allow_message: String,
    host_allowed: Vec<HostCase>,
    allow_check_dial: Vec<AllowDialCase>,
    #[cfg_attr(not(feature = "api"), allow(dead_code))]
    allow_url: Vec<AllowUrlCase>,
    #[cfg_attr(not(feature = "api"), allow(dead_code))]
    redirect_message: String,
    #[cfg_attr(not(feature = "api"), allow(dead_code))]
    redirect_headers: Vec<RedirectCase>,
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

#[test]
fn allow_message() {
    assert_eq!(HOST_NOT_ALLOWED_MESSAGE, load().allow_message);
}

#[test]
fn host_allowed_vectors() {
    let cases = load().host_allowed;
    assert!(!cases.is_empty(), "host_allowed section is empty");
    for c in cases {
        let p = NetPolicy::online().with_allow_hosts(&c.allow_hosts);
        assert_eq!(
            p.allows_host(&c.host),
            c.allowed,
            "{}: {:?} against {:?}",
            c.name,
            c.host,
            c.allow_hosts
        );
    }
}

#[test]
fn allow_check_dial_vectors() {
    let cases = load().allow_check_dial;
    assert!(!cases.is_empty(), "allow_check_dial section is empty");
    for c in cases {
        let p = NetPolicy::online().with_allow_hosts(&c.allow_hosts);
        let got = p.check_dial(&c.network, &c.addr);
        let got = got.err().map(|e| e.to_string()).unwrap_or_default();
        assert_eq!(got, c.error, "{}", c.name);
    }
}

#[cfg(feature = "api")]
#[tokio::test]
async fn allow_url_vectors() {
    use hop_top_kit::netpolicy::GuardedClient;

    let cases = load().allow_url;
    assert!(!cases.is_empty(), "allow_url section is empty");
    for c in cases {
        let p = NetPolicy::online().with_allow_hosts(&c.allow_hosts);
        let url: reqwest::Url = c.url.parse().expect("vector url parses");
        if c.error.is_empty() {
            assert!(p.allows(&url), "{}: {} refused", c.name, c.url);
            continue;
        }
        let client = GuardedClient::new(p).expect("build client");
        let method: reqwest::Method = c.method.parse().expect("vector method");
        let err = client
            .request(method, c.url.as_str())
            .send()
            .await
            .expect_err("unlisted request was sent");
        assert!(
            err.is_host_not_allowed(),
            "{}: not a refusal: {err}",
            c.name
        );
        assert_eq!(err.to_string(), c.error, "{}", c.name);
    }
}

#[cfg(feature = "api")]
#[tokio::test]
async fn redirect_header_vectors() {
    use hop_top_kit::netpolicy::{GuardedClient, NetError};

    let v = load();
    assert!(!v.redirect_headers.is_empty(), "redirect_headers is empty");
    for c in v.redirect_headers {
        let mut a = mockito::Server::new_async().await;
        let mut b = mockito::Server::new_async().await;
        let to = format!(
            "{}/final",
            if c.cross_origin { b.url() } else { a.url() }
        );
        let _hop = a
            .mock("GET", "/hop")
            .with_status(302)
            .with_header("location", &to)
            .create_async()
            .await;
        let target = if c.cross_origin { &mut b } else { &mut a };
        let mut fin = target.mock("GET", "/final").with_status(204);
        if !c.refused {
            for (k, val) in &c.headers {
                // Allowed across origins, but an HTTP library may drop it
                // on a bodiless hop; the contract does not assert it.
                if k.eq_ignore_ascii_case("content-type") {
                    continue;
                }
                fin = fin.match_header(k.to_ascii_lowercase().as_str(), val.as_str());
            }
        }
        let fin = fin.expect(if c.refused { 0 } else { 1 }).create_async().await;

        let client = GuardedClient::new(NetPolicy::online()).expect("build client");
        let mut rb = client.get(format!("{}/hop", a.url()));
        for (k, val) in &c.headers {
            rb = rb.header(k.as_str(), val.as_str());
        }
        let res = rb.send().await;
        if c.refused {
            let err = res.expect_err(&c.name);
            assert!(matches!(err, NetError::Redirect(_)), "{}: {err}", c.name);
            assert!(
                err.to_string().ends_with(&v.redirect_message),
                "{}: {err}",
                c.name
            );
            for val in c.headers.values() {
                assert!(!err.to_string().contains(val.as_str()), "{}: {err}", c.name);
            }
        } else {
            let resp = res.unwrap_or_else(|e| panic!("{}: {e}", c.name));
            assert_eq!(resp.status().as_u16(), 204, "{}", c.name);
        }
        fin.assert_async().await;
    }
}
