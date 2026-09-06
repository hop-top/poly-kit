//! Cross-language parity gate for the httpcache wire format.
//!
//! These tests execute the fixtures in `contracts/httpcache-v1` — the
//! same three JSON files the Go suite consumes. The fixtures are the
//! contract of record; this port and the Go port are gated by one
//! artifact, so a divergence fails on both sides rather than silently
//! forking the format.

#![cfg(feature = "httpcache")]

use std::collections::BTreeMap;
use std::path::PathBuf;

use base64::engine::general_purpose::STANDARD;
use base64::Engine as _;
use hop_top_kit::httpcache::{
    cacheable_request, cacheable_response, Cache, Config, Headers, Request, Response, Url,
    DEFAULT_PREFIX,
};
use hop_top_kit::kv::Config as KvConfig;
use serde::Deserialize;

/// Resolves `contracts/httpcache-v1/<name>` from the crate directory
/// (`sdk/experimental/rs` → repo root is three levels up).
fn contract_path(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../contracts/httpcache-v1")
        .join(name)
}

/// Loads and parses a fixture, failing loudly if it is absent.
fn read_contract<T: for<'de> Deserialize<'de>>(name: &str) -> T {
    let path = contract_path(name);
    let raw =
        std::fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {}: {e}", path.display()))
}

// ---------------------------------------------------------------------
// keying.json
// ---------------------------------------------------------------------

#[derive(Deserialize)]
struct Keying {
    default_prefix: String,
    derivation: Derivation,
    normalization: Normalization,
    cases: Vec<KeyCase>,
    prefix_cases: Vec<PrefixCase>,
}

#[derive(Deserialize)]
struct Derivation {
    hash: String,
    digest_encoding: String,
    preimage: String,
    separator: String,
    key: String,
    vary_aware: bool,
}

#[derive(Deserialize)]
struct Normalization {
    method_case_folded: bool,
    host_case_folded: bool,
    default_port_stripped: bool,
    query_params_sorted: bool,
    empty_query_marker_dropped: bool,
    fragment_stripped: bool,
    dot_segments_resolved: bool,
    path_non_ascii_percent_encoded: bool,
    path_space_percent_encoded: bool,
    query_non_ascii_percent_encoded: bool,
    existing_percent_escapes_preserved: bool,
}

#[derive(Deserialize)]
struct KeyCase {
    name: String,
    method: String,
    url: String,
    want_url: String,
    want_digest: String,
    want_key: String,
}

#[derive(Deserialize)]
struct PrefixCase {
    name: String,
    prefix: String,
    want_effective_prefix: String,
}

/// Pins the derivation metadata itself, so a port cannot quietly switch
/// hash, separator, or key layout while still matching the vectors.
#[test]
fn contract_keying_derivation() {
    let k: Keying = read_contract("keying.json");

    assert_eq!(k.default_prefix, DEFAULT_PREFIX, "default_prefix");
    assert_eq!(k.derivation.hash, "sha256");
    assert_eq!(k.derivation.digest_encoding, "lowercase hex, 64 chars");
    assert_eq!(k.derivation.preimage, "method + \" \" + url");
    assert_eq!(k.derivation.separator, " ");
    assert_eq!(k.derivation.key, "prefix + digest");
    assert!(
        !k.derivation.vary_aware,
        "v1 keys on method+URL only; request headers never participate"
    );
}

/// The `normalization` block states which transforms the contract does
/// and does not apply. Asserting it here means a fixture edit that
/// relaxes a rule fails the port that relies on the rule.
#[test]
fn contract_keying_normalization_flags() {
    let n: Normalization = read_contract::<Keying>("keying.json").normalization;

    assert!(!n.method_case_folded);
    assert!(!n.host_case_folded);
    assert!(!n.default_port_stripped);
    assert!(!n.query_params_sorted);
    assert!(!n.empty_query_marker_dropped);
    assert!(!n.fragment_stripped);
    assert!(!n.dot_segments_resolved);
    assert!(n.path_non_ascii_percent_encoded);
    assert!(n.path_space_percent_encoded);
    assert!(
        !n.query_non_ascii_percent_encoded,
        "the path/query asymmetry is the likeliest divergence; it must hold"
    );
    assert!(n.existing_percent_escapes_preserved);
}

/// The cache-key vectors. A port that normalizes method case, host case,
/// default ports, query order, dot segments, or fragments fails here.
#[test]
fn contract_keying_cases() {
    let k: Keying = read_contract("keying.json");
    assert!(!k.cases.is_empty(), "keying.json has no cases");

    let dir = tempfile::tempdir().expect("tempdir");
    let store = KvConfig::sqlite(dir.path().join("kv.db").to_str().expect("utf-8 path"))
        .open()
        .expect("open store");
    let cache = Cache::new(&store);

    for case in &k.cases {
        let url = Url::parse(&case.url)
            .unwrap_or_else(|e| panic!("{}: parse {:?}: {e}", case.name, case.url));

        // The re-serialization is itself contractual: the digest is only
        // reproducible if want_url matches byte for byte.
        assert_eq!(
            url.as_string(),
            case.want_url,
            "{}: re-serialized URL",
            case.name
        );
        assert_eq!(case.want_digest.len(), 64, "{}: digest width", case.name);

        let req = Request::new(case.method.clone(), &case.url).expect("build request");
        assert_eq!(
            req.method, case.method,
            "{}: method carried verbatim (no case folding)",
            case.name
        );

        let got = cache.key(&req);
        assert_eq!(got, case.want_key, "{}: cache key", case.name);
        assert_eq!(
            got,
            format!("{}{}", k.default_prefix, case.want_digest),
            "{}: key is default prefix + digest",
            case.name
        );
    }
}

/// The two paired cases that pin the path/query encoding asymmetry.
///
/// An encoded path and its decoded spelling must collapse to ONE key,
/// while an encoded query and its decoded spelling must stay DISTINCT.
#[test]
fn contract_keying_path_query_asymmetry() {
    let k: Keying = read_contract("keying.json");
    let digest_of = |name: &str| {
        k.cases
            .iter()
            .find(|c| c.name == name)
            .unwrap_or_else(|| panic!("fixture case {name:?} missing"))
            .want_digest
            .clone()
    };

    assert_eq!(
        digest_of("percent-encoded non-ASCII path"),
        digest_of("literal non-ASCII path is percent-encoded"),
        "path: encoded and decoded spellings must collapse to one key"
    );
    assert_ne!(
        digest_of("percent-encoded non-ASCII query is preserved"),
        digest_of("literal non-ASCII query is NOT percent-encoded"),
        "query: encoded and decoded spellings must key differently"
    );
}

/// The prefix namespace, including the empty-means-default rule.
#[test]
fn contract_keying_prefix_cases() {
    let k: Keying = read_contract("keying.json");
    let baseline = &k.cases[0];

    let dir = tempfile::tempdir().expect("tempdir");
    let store = KvConfig::sqlite(dir.path().join("kv.db").to_str().expect("utf-8 path"))
        .open()
        .expect("open store");

    for case in &k.prefix_cases {
        let cache = Cache::with_config(&store, Config::default().with_prefix(case.prefix.clone()));
        let req = Request::get(&baseline.url).expect("build request");
        assert_eq!(
            cache.key(&req),
            format!("{}{}", case.want_effective_prefix, baseline.want_digest),
            "prefix/{}",
            case.name
        );
    }
}

// ---------------------------------------------------------------------
// entry.json
// ---------------------------------------------------------------------

#[derive(Deserialize)]
struct EntryContract {
    schema: Schema,
    framing_headers: Framing,
    encode_cases: Vec<EncodeCase>,
    decode_cases: Vec<DecodeCase>,
}

#[derive(Deserialize)]
struct Schema {
    fields: Vec<String>,
}

#[derive(Deserialize)]
struct Framing {
    strip: Vec<String>,
    strip_is_case_insensitive: bool,
    strip_mutates_source_response: bool,
}

#[derive(Deserialize)]
struct EncodeCase {
    name: String,
    status: u16,
    headers: BTreeMap<String, Vec<String>>,
    body_utf8: Option<String>,
    body_base64: Option<String>,
    want_json: String,
}

#[derive(Deserialize)]
struct DecodeCase {
    name: String,
    json: String,
    want_ok: bool,
    #[serde(default)]
    want_status: u16,
    want_headers: Option<BTreeMap<String, Vec<String>>>,
    #[serde(default)]
    want_body_utf8: String,
    #[serde(default)]
    want_content_length: u64,
}

/// Resolves a fixture body from its UTF-8 or base64 spelling; exactly one
/// must be present.
fn contract_body(case: &EncodeCase) -> Vec<u8> {
    match (&case.body_utf8, &case.body_base64) {
        (Some(_), Some(_)) => {
            panic!("{}: set exactly one of body_utf8 / body_base64", case.name)
        }
        (Some(s), None) => s.clone().into_bytes(),
        (None, Some(b64)) => STANDARD
            .decode(b64)
            .unwrap_or_else(|e| panic!("{}: body_base64 must be standard base64: {e}", case.name)),
        (None, None) => panic!("{}: set one of body_utf8 / body_base64", case.name),
    }
}

/// Pins the envelope's field set and the framing-header rule.
#[test]
fn contract_entry_schema() {
    let c: EntryContract = read_contract("entry.json");

    assert_eq!(
        c.schema.fields,
        ["status", "headers", "body"],
        "envelope field set and order"
    );
    assert_eq!(
        c.framing_headers.strip,
        ["Connection", "Content-Length", "Transfer-Encoding"],
        "framing strip list"
    );
    assert!(c.framing_headers.strip_is_case_insensitive);
    assert!(
        !c.framing_headers.strip_mutates_source_response,
        "the strip must leave the source response untouched"
    );
}

/// Encode vectors, compared BYTE-EXACTLY.
///
/// Byte-exactness — not mere semantic equality — is what later ports have
/// to reproduce: field order and base64 spelling are both contractual.
#[test]
fn contract_entry_encode_cases() {
    let c: EntryContract = read_contract("entry.json");
    assert!(!c.encode_cases.is_empty(), "entry.json has no encode_cases");

    for case in &c.encode_cases {
        let body = contract_body(case);
        let mut headers = Headers::new();
        for (name, values) in &case.headers {
            for value in values {
                headers.append(name, value.clone());
            }
        }
        let resp = Response {
            status: case.status,
            headers: headers.clone(),
            content_length: body.len() as u64,
            body: body.clone(),
        };

        let got = encode(&resp);
        assert_eq!(got, case.want_json, "encode/{}", case.name);

        // The strip must not mutate the source response.
        assert_eq!(
            resp.headers, headers,
            "encode/{}: source headers mutated",
            case.name
        );
        assert_eq!(resp.body, body, "encode/{}: source body mutated", case.name);
    }
}

/// Decode vectors, including the leniency and rejection rules.
#[test]
fn contract_entry_decode_cases() {
    let c: EntryContract = read_contract("entry.json");
    assert!(!c.decode_cases.is_empty(), "entry.json has no decode_cases");

    for case in &c.decode_cases {
        let decoded = decode(case.json.as_bytes());

        if !case.want_ok {
            assert!(
                decoded.is_none(),
                "decode/{}: malformed envelope must be rejected",
                case.name
            );
            continue;
        }

        let resp = decoded.unwrap_or_else(|| panic!("decode/{}: unexpected rejection", case.name));
        assert_eq!(
            resp.status, case.want_status,
            "decode/{}: status",
            case.name
        );
        assert_eq!(
            resp.body_string(),
            case.want_body_utf8,
            "decode/{}: body",
            case.name
        );
        assert_eq!(
            resp.content_length, case.want_content_length,
            "decode/{}: content length recomputed from the body",
            case.name
        );

        if let Some(want) = &case.want_headers {
            let got: BTreeMap<String, Vec<String>> = resp
                .headers
                .iter()
                .map(|(k, v)| (k.to_string(), v.clone()))
                .collect();
            assert_eq!(&got, want, "decode/{}: headers", case.name);
        }
    }
}

/// The base64 alphabet rules, asserted directly rather than only through
/// the fixture's two rejection cases.
#[test]
fn contract_entry_rejects_nonconforming_base64() {
    // Unpadded and URL-safe bodies are rejected on decode.
    assert!(decode(br#"{"status":200,"headers":{},"body":"aGk"}"#).is_none());
    assert!(decode(br#"{"status":200,"headers":{},"body":"-_8="}"#).is_none());

    // The standard alphabet round-trips + and / rather than - and _.
    let resp = Response::new(200, STANDARD.decode("+/+/").expect("decode fixture bytes"));
    assert_eq!(
        encode(&resp),
        r#"{"status":200,"headers":{},"body":"+/+/"}"#
    );
}

/// The body must be a base64 STRING, never serde's default array of
/// numbers. This is the single divergence the contract calls out by name.
#[test]
fn contract_entry_body_is_a_base64_string_not_a_byte_array() {
    let json = encode(&Response::new(200, "hi"));
    assert!(
        json.contains(r#""body":"aGk=""#),
        "body must serialize as a padded base64 string, got {json}"
    );
    assert!(
        !json.contains("[104,105]"),
        "body must not serialize as an array of byte numbers, got {json}"
    );
}

// ---------------------------------------------------------------------
// cacheability.json
// ---------------------------------------------------------------------

#[derive(Deserialize)]
struct Cacheability {
    request_cacheable: Vec<RequestCase>,
    response_cacheable: Vec<ResponseCase>,
}

#[derive(Deserialize)]
struct RequestCase {
    name: String,
    method: String,
    headers: BTreeMap<String, String>,
    want: bool,
}

#[derive(Deserialize)]
struct ResponseCase {
    name: String,
    status: u16,
    headers: BTreeMap<String, String>,
    want: bool,
}

/// The request and response cacheability gates.
#[test]
fn contract_cacheability_cases() {
    let c: Cacheability = read_contract("cacheability.json");
    assert!(!c.request_cacheable.is_empty());
    assert!(!c.response_cacheable.is_empty());

    for case in &c.request_cacheable {
        let mut req = Request::new(case.method.clone(), "https://example.com/a").expect("request");
        for (name, value) in &case.headers {
            req.headers.set(name, value.clone());
        }
        assert_eq!(cacheable_request(&req), case.want, "req/{}", case.name);
    }

    for case in &c.response_cacheable {
        let mut resp = Response::new(case.status, "x");
        for (name, value) in &case.headers {
            resp.headers.set(name, value.clone());
        }
        assert_eq!(cacheable_response(&resp), case.want, "resp/{}", case.name);
    }
}

/// The cacheability vectors again, but observed END-TO-END through the
/// cache rather than through the predicates.
///
/// A cacheable exchange must serve its second call without a fetch; a
/// non-cacheable one must fetch twice. This is the shape the Go suite
/// asserts, and it catches a gate that is correct in isolation but
/// mis-wired into `fetch`.
#[test]
fn contract_cacheability_end_to_end() {
    let c: Cacheability = read_contract("cacheability.json");
    let dir = tempfile::tempdir().expect("tempdir");

    for (i, case) in c.request_cacheable.iter().enumerate() {
        let store = KvConfig::sqlite(
            dir.path()
                .join(format!("req{i}.db"))
                .to_str()
                .expect("utf-8 path"),
        )
        .open()
        .expect("open store");
        let cache = Cache::new(&store);

        let mut req = Request::new(case.method.clone(), "https://example.com/a").expect("request");
        for (name, value) in &case.headers {
            req.headers.set(name, value.clone());
        }

        let mut fetches = 0;
        let mut fetch = |_: &Request| {
            fetches += 1;
            Ok::<_, std::convert::Infallible>(Response::new(200, "x"))
        };
        cache.fetch(&req, &mut fetch).expect("first fetch");
        cache.fetch(&req, &mut fetch).expect("second fetch");

        let want = if case.want { 1 } else { 2 };
        assert_eq!(fetches, want, "req/{}", case.name);
    }

    for (i, case) in c.response_cacheable.iter().enumerate() {
        let store = KvConfig::sqlite(
            dir.path()
                .join(format!("resp{i}.db"))
                .to_str()
                .expect("utf-8 path"),
        )
        .open()
        .expect("open store");
        let cache = Cache::new(&store);

        let req = Request::get("https://example.com/a").expect("request");
        let mut fetches = 0;
        let mut fetch = |_: &Request| {
            fetches += 1;
            let mut resp = Response::new(case.status, "x");
            for (name, value) in &case.headers {
                resp.headers.set(name, value.clone());
            }
            Ok::<_, std::convert::Infallible>(resp)
        };
        cache.fetch(&req, &mut fetch).expect("first fetch");
        cache.fetch(&req, &mut fetch).expect("second fetch");

        let want = if case.want { 1 } else { 2 };
        assert_eq!(fetches, want, "resp/{}", case.name);
    }
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

/// Encodes a response to its stored envelope, as the cache does.
fn encode(resp: &Response) -> String {
    let dir = tempfile::tempdir().expect("tempdir");
    let store = KvConfig::sqlite(dir.path().join("kv.db").to_str().expect("utf-8 path"))
        .open()
        .expect("open store");
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");

    cache
        .fetch(&req, |_: &Request| {
            Ok::<_, std::convert::Infallible>(resp.clone())
        })
        .expect("fetch");

    let raw = hop_top_kit::kv::Store::get(&store, cache.key(&req).as_bytes())
        .expect("store read")
        .expect("entry stored");
    String::from_utf8(raw).expect("envelope is utf-8")
}

/// Decodes a stored envelope, reporting `None` when the cache would treat
/// it as a miss.
fn decode(raw: &[u8]) -> Option<Response> {
    let dir = tempfile::tempdir().expect("tempdir");
    let store = KvConfig::sqlite(dir.path().join("kv.db").to_str().expect("utf-8 path"))
        .open()
        .expect("open store");
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");

    hop_top_kit::kv::Store::put(&store, cache.key(&req).as_bytes(), raw).expect("store write");

    let mut fetched = false;
    let resp = cache
        .fetch(&req, |_: &Request| {
            fetched = true;
            Ok::<_, std::convert::Infallible>(Response::new(599, ""))
        })
        .expect("fetch");

    // A malformed entry degrades to a miss, which means the fetch ran.
    if fetched {
        None
    } else {
        Some(resp)
    }
}
