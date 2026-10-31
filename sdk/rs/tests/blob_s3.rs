//! S3 backend conformance, replayed from xrr cassettes by default.
//!
//! Default `cargo test`: every HTTP exchange is answered from
//! `tests/cassettes/blob-s3/`, so no network and no LocalStack is needed.
//!
//! Re-record against a local S3 (LocalStack) with:
//!
//! ```sh
//! localstack start -d                       # endpoint http://localhost:4566
//! rm -f tests/cassettes/blob-s3/*.yaml      # drop stale pairs
//! XRR_MODE=record cargo test --features blob-s3 --test blob_s3
//! localstack stop
//! ```
//!
//! `S3_TEST_ENDPOINT` points at another local server; `AWS_ACCESS_KEY_ID` /
//! `AWS_SECRET_ACCESS_KEY` override the dummy `test` pair. Recording refuses
//! any non-loopback endpoint: cassettes are never recorded against AWS.
//!
//! What a cassette keeps is sanitized (see [`sanitize_headers`]): no
//! `Authorization`, signing date, payload hash, token or request id ever
//! reaches disk, and server timestamps are pinned, so a re-recording differs
//! only in xrr's own `recorded_at` stamp and a signature's timestamp never
//! affects replay.
#![cfg(feature = "blob-s3")]

use std::collections::{BTreeMap, HashMap};
use std::fmt;
use std::io::Read;
use std::path::PathBuf;
use std::sync::{Arc, Mutex, Once};

use hop_top_kit::blob::s3::{
    Credentials, GuardedTransport, HttpRequest, HttpResponse, S3Config, S3Store, Transport,
    TransportError,
};
use hop_top_kit::blob::{BlobError, Object, Stater, Store};
use hop_top_kit::netpolicy::NetPolicy;
use hop_top_xrr::{Adapter, FileCassette, Mode, Session, XrrError};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

mod blobtest;
use blobtest::Fixture;

const BUCKET: &str = "kit-blob-s3-conformance";
const REGION: &str = "us-east-1";

fn recording() -> bool {
    match std::env::var("XRR_MODE").as_deref() {
        Ok("record") => true,
        Ok("replay") | Err(_) => false,
        Ok(other) => panic!("XRR_MODE={other:?}: want record or replay"),
    }
}

fn endpoint() -> String {
    std::env::var("S3_TEST_ENDPOINT").unwrap_or_else(|_| "http://localhost:4566".into())
}

fn cassette_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/cassettes/blob-s3")
}

/// Dummy key pair, built at run time: LocalStack accepts any.
fn key_pair() -> (String, String) {
    let id = std::env::var("AWS_ACCESS_KEY_ID").unwrap_or_else(|_| ["te", "st"].concat());
    let secret = std::env::var("AWS_SECRET_ACCESS_KEY").unwrap_or_else(|_| ["te", "st"].concat());
    (id, secret)
}

fn credentials() -> Credentials {
    let (id, secret) = key_pair();
    Credentials::new(id, secret)
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

fn sha256_hex(data: &[u8]) -> String {
    hex(&Sha256::digest(data))
}

// ---------------------------------------------------------------- cassettes

/// One recorded request, reduced to what identifies and explains it. The
/// host, scheme and every signing header are gone: they vary per run or are
/// credentials, and none of them is needed to pick the answer.
#[derive(Debug, Serialize, Deserialize)]
struct Req {
    method: String,
    /// Path and query exactly as sent.
    target: String,
    headers: BTreeMap<String, String>,
    body: String,
    /// Nth identical (method, target, body) request of this store.
    occurrence: u32,
}

#[derive(Debug, Serialize, Deserialize)]
struct Resp {
    status: u16,
    headers: BTreeMap<String, String>,
    body: String,
}

/// xrr adapter for S3 exchanges (id `s3-http`).
///
/// xrr's stock `http` fingerprint (method, path+query, body hash) cannot
/// tell the second `HEAD k` from the first, yet S3 answers them differently
/// once the object changed in between. This adapter adds the occurrence
/// index to the fingerprint; record and replay count identically.
struct S3Http;

impl Adapter for S3Http {
    type Req = Req;
    type Resp = Resp;

    fn id(&self) -> &str {
        "s3-http"
    }

    fn fingerprint(&self, r: &Req) -> Result<String, XrrError> {
        // Canonical JSON, keys in code-point order, as the cassette spec
        // asks (written out so key order never depends on serde_json's
        // map implementation).
        let canonical = format!(
            "{{\"body_hash\":\"{}\",\"method\":{:?},\"occurrence\":{},\"path\":{:?}}}",
            sha256_hex(r.body.as_bytes()),
            r.method,
            r.occurrence,
            r.target
        );
        Ok(sha256_hex(canonical.as_bytes())[..8].to_string())
    }
}

/// Keep only headers that explain the exchange; everything else is a
/// credential, a clock reading or a server-generated id.
fn sanitize_headers<'a>(
    keep: &[&str],
    headers: impl Iterator<Item = &'a (String, String)>,
) -> BTreeMap<String, String> {
    headers
        .filter(|(k, _)| keep.contains(&k.as_str()))
        .cloned()
        .collect()
}

/// Pin server-generated values inside an XML body.
fn normalize_body(body: &str) -> String {
    let mut out = body.to_string();
    for tag in ["LastModified", "RequestId", "HostId"] {
        let (open, close) = (format!("<{tag}>"), format!("</{tag}>"));
        let mut from = 0;
        while let Some(i) = out[from..].find(&open) {
            let start = from + i + open.len();
            let Some(len) = out[start..].find(&close) else {
                break;
            };
            out.replace_range(start..start + len, "normalized");
            from = start + "normalized".len() + close.len();
        }
    }
    out
}

fn utf8(body: &[u8]) -> String {
    String::from_utf8(body.to_vec()).expect("cassette bodies must be UTF-8")
}

/// `/path?query` of an absolute URL.
fn target_of(url: &str) -> String {
    let rest = url.split_once("://").expect("absolute url").1;
    rest.find('/')
        .map_or("/".to_string(), |i| rest[i..].to_string())
}

/// A [`Transport`] that answers from a cassette, or, when recording, asks
/// the real guarded client and tapes the sanitized answer.
struct XrrTransport {
    session: Session,
    live: Option<GuardedTransport>,
    seen: Mutex<HashMap<String, u32>>,
}

impl fmt::Debug for XrrTransport {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("XrrTransport")
            .field("live", &self.live.is_some())
            .finish()
    }
}

impl XrrTransport {
    fn new() -> Self {
        let cassette = FileCassette::new(cassette_dir());
        if recording() {
            let ep = endpoint();
            let host = ep.split_once("://").map_or(ep.as_str(), |x| x.1);
            assert!(
                ["localhost", "127.0.0.1", "[::1]"]
                    .iter()
                    .any(|h| host.starts_with(h)),
                "refusing to record against non-loopback endpoint {ep}"
            );
            XrrTransport {
                session: Session::new(Mode::Record, cassette),
                live: Some(GuardedTransport::new(NetPolicy::online()).expect("client")),
                seen: Mutex::default(),
            }
        } else {
            XrrTransport {
                session: Session::new(Mode::Replay, cassette),
                live: None,
                seen: Mutex::default(),
            }
        }
    }
}

impl Transport for XrrTransport {
    fn send(&self, req: HttpRequest) -> Result<HttpResponse, TransportError> {
        let target = target_of(&req.url);
        let n = {
            let mut seen = self.seen.lock().unwrap();
            let c = seen
                .entry(format!("{} {target} {}", req.method, sha256_hex(&req.body)))
                .or_insert(0);
            *c += 1;
            *c - 1
        };
        let recorded = Req {
            method: req.method.clone(),
            target,
            headers: sanitize_headers(&["content-type"], req.headers.iter()),
            body: utf8(&req.body),
            occurrence: n,
        };
        let resp = self.session.record(&S3Http, &recorded, || {
            let live = self
                .live
                .as_ref()
                .expect("live client exists only when recording");
            let out = live
                .send(req)
                .map_err(|e| XrrError::Io(std::io::Error::other(e)))?;
            Ok(Resp {
                status: out.status,
                headers: sanitize_headers(
                    &["content-type", "content-length", "etag"],
                    out.headers.iter(),
                ),
                body: normalize_body(&utf8(&out.body)),
            })
        })?;
        Ok(HttpResponse {
            status: resp.status,
            headers: resp.headers.into_iter().collect(),
            body: resp.body.into_bytes(),
        })
    }
}

// ----------------------------------------------------------------- fixtures

fn config(prefix: &str) -> S3Config {
    S3Config::new(BUCKET, REGION, credentials())
        .with_endpoint(endpoint())
        .with_prefix(prefix)
}

/// Record mode only: create the bucket once, outside any cassette.
fn ensure_bucket() {
    static ONCE: Once = Once::new();
    ONCE.call_once(|| {
        let (id, secret) = key_pair();
        let out = std::process::Command::new("curl")
            .args(["-sS", "-o", "/dev/null", "-w", "%{http_code}", "-X", "PUT"])
            .args(["--aws-sigv4", &format!("aws:amz:{REGION}:s3")])
            .args(["--user", &format!("{id}:{secret}")])
            .arg(format!("{}/{BUCKET}", endpoint()))
            .output()
            .expect("curl is needed to create the bucket when recording");
        let code = String::from_utf8_lossy(&out.stdout).to_string();
        assert!(
            matches!(code.as_str(), "200" | "409"),
            "create bucket: HTTP {code}: {}",
            String::from_utf8_lossy(&out.stderr)
        );
    });
}

/// Record mode only: empty the prefix through an unrecorded client, so the
/// cassette always starts from the same empty state.
fn purge(cfg: &S3Config, ns: &str) {
    let raw = S3Store::new(cfg.clone(), NetPolicy::online()).expect("raw store");
    for o in raw.list(ns).expect("purge list") {
        raw.delete(&o.key).expect("purge delete");
    }
}

/// Store under `prefix`; `ns` is the part of it the test writes into and
/// recording empties first (all of it, for a prefixed store).
fn s3_fixture_ns(prefix: &str, ns: &str) -> Fixture<S3Store> {
    let cfg = config(prefix);
    if recording() {
        ensure_bucket();
        purge(&cfg, ns);
    }
    let store = S3Store::with_transport(cfg, Arc::new(XrrTransport::new())).expect("store");
    Fixture::new(store)
}

fn s3_fixture(prefix: &str) -> Fixture<S3Store> {
    s3_fixture_ns(prefix, "")
}

fn conformance_fixture(case: &str) -> Fixture<S3Store> {
    s3_fixture(&format!("conformance/{}", case.to_lowercase()))
}

// -------------------------------------------------------------------- tests

#[test]
fn s3_conformance() {
    blobtest::run(conformance_fixture);
}

#[test]
fn s3_conformance_stat() {
    blobtest::run_stat(conformance_fixture);
}

/// Keys that need URL encoding on the way out and `encoding-type=url`
/// decoding on the way back.
#[test]
fn special_character_keys_round_trip() {
    let s = s3_fixture("special").store;
    let keys = [
        "dir/with space.txt",
        "plus+sign/é-ü.txt",
        "amp&ersand/<tag>.txt",
        "tilde~/q?mark=1.txt",
    ];
    for k in keys {
        s.put(k, k.as_bytes(), "text/plain").expect("put");
    }
    for k in keys {
        let mut body = String::new();
        s.get(k).expect("get").read_to_string(&mut body).unwrap();
        assert_eq!(body, k);
        assert!(s.exists(k).unwrap());
    }
    let mut got: Vec<String> = s
        .list("")
        .expect("list")
        .into_iter()
        .map(|o| o.key)
        .collect();
    got.sort();
    let mut want: Vec<String> = keys.iter().map(|k| k.to_string()).collect();
    want.sort();
    assert_eq!(got, want);
}

/// A store with no prefix addresses the bucket root.
#[test]
fn unprefixed_store_round_trip() {
    let s = s3_fixture_ns("", "unprefixed-root/").store;
    s.put("unprefixed-root/a.txt", "a".as_bytes(), "text/plain")
        .expect("put");
    let objs = s.list("unprefixed-root/").expect("list");
    assert_eq!(
        objs,
        vec![Object {
            key: "unprefixed-root/a.txt".into(),
            size: 1,
            content_type: String::new()
        }]
    );
    assert_eq!(
        s.stat("unprefixed-root/a.txt").unwrap().content_type,
        "text/plain"
    );
    s.delete("unprefixed-root/a.txt").expect("delete");
    assert!(matches!(
        s.get("unprefixed-root/a.txt"),
        Err(BlobError::NotFound(_))
    ));
}

/// Two stores under different prefixes never see each other's keys.
#[test]
fn prefixes_isolate_stores() {
    let (a, b) = (s3_fixture("iso/a").store, s3_fixture("iso/b").store);
    a.put("k", "from-a".as_bytes(), "text/plain")
        .expect("put a");
    assert!(a.exists("k").unwrap());
    assert!(!b.exists("k").unwrap());
    assert!(b.list("").unwrap().is_empty());
}
