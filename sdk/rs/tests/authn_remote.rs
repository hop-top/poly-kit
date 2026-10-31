//! Remote JWKS and OIDC discovery: the Go `remote_test.go` and
//! `oidc_cassette_test.go` suites ported. The cache rules run over a
//! scripted `Fetch` and an injected clock; Go's recorded Google
//! exchanges replay through the same seam; the guarded HTTP fetcher is
//! exercised against a local server.
#![cfg(feature = "authn-remote")]

use std::collections::HashMap;
use std::path::PathBuf;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use hop_top_kit::authn::{
    check_url, new_jwks, new_oidc, AuthnError, Fetch, HttpFetch, Kind, Options, Remote, Verifier,
};
use hop_top_kit::netpolicy::NetPolicy;
use serde_json::{json, Value};

const NOW: u64 = 1_790_000_000;

fn b64(v: &[u8]) -> String {
    URL_SAFE_NO_PAD.encode(v)
}

/// A settable clock.
#[derive(Clone)]
struct Clock(Arc<Mutex<SystemTime>>);

impl Clock {
    fn new() -> Self {
        Self(Arc::new(Mutex::new(UNIX_EPOCH + Duration::from_secs(NOW))))
    }
    fn advance(&self, d: Duration) {
        *self.0.lock().unwrap() += d;
    }
    fn secs(&self) -> u64 {
        self.0
            .lock()
            .unwrap()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs()
    }
    fn opts(&self) -> Options {
        let c = self.0.clone();
        Options {
            now: Some(Arc::new(move || *c.lock().unwrap())),
            ..Options::default()
        }
    }
}

fn signer(seed: u8) -> SigningKey {
    SigningKey::from_bytes(&[seed; 32])
}

fn jwk(sk: &SigningKey, kid: &str) -> Value {
    json!({"kty": "OKP", "crv": "Ed25519", "kid": kid, "use": "sig",
           "x": b64(&sk.verifying_key().to_bytes())})
}

fn token(sk: &SigningKey, kid: &str, iss: &str, clock: &Clock) -> String {
    let now = clock.secs();
    let input = format!(
        "{}.{}",
        b64(json!({"alg": "EdDSA", "kid": kid}).to_string().as_bytes()),
        b64(
            json!({"sub": "alice", "iss": iss, "iat": now, "exp": now + 3600})
                .to_string()
                .as_bytes()
        )
    );
    format!("{input}.{}", b64(&sk.sign(input.as_bytes()).to_bytes()))
}

type Reply = Result<Vec<u8>, String>;

/// An issuer under test control: scripted replies per URL, with hit
/// counts.
#[derive(Default)]
struct Script {
    replies: Mutex<HashMap<String, Reply>>,
    hits: Mutex<HashMap<String, usize>>,
}

impl Script {
    fn set(&self, url: &str, reply: Reply) {
        self.replies.lock().unwrap().insert(url.into(), reply);
    }
    fn set_json(&self, url: &str, v: &Value) {
        self.set(url, Ok(v.to_string().into_bytes()));
    }
    fn hits(&self, url: &str) -> usize {
        *self.hits.lock().unwrap().get(url).unwrap_or(&0)
    }
}

impl Fetch for Script {
    fn get(&self, url: &str) -> Reply {
        *self.hits.lock().unwrap().entry(url.into()).or_default() += 1;
        self.replies
            .lock()
            .unwrap()
            .get(url)
            .cloned()
            .unwrap_or_else(|| Err(format!("GET {url}: status 404")))
    }
}

fn remote(s: &Arc<Script>, refresh: u64, min_refresh: u64) -> Remote {
    Remote {
        fetch: Some(s.clone()),
        refresh: Duration::from_secs(refresh),
        min_refresh: Duration::from_secs(min_refresh),
        ..Remote::default()
    }
}

const JWKS: &str = "https://idp.test/jwks.json";

fn refusal(v: &Verifier, raw: &str) -> AuthnError {
    v.verify(raw).unwrap_err()
}

#[test]
fn jwks_verifies_and_caches() {
    let s = Arc::new(Script::default());
    let (a, b) = (signer(1), signer(2));
    s.set_json(JWKS, &json!({"keys": [jwk(&a, "a"), jwk(&b, "b")]}));
    let clock = Clock::new();
    let v = new_jwks(JWKS, remote(&s, 3600, 60), clock.opts()).unwrap();

    for _ in 0..3 {
        for (sk, kid) in [(&a, "a"), (&b, "b")] {
            let t = v.verify(&token(sk, kid, "x", &clock)).unwrap();
            assert_eq!(t.key_id, kid);
        }
    }
    assert_eq!(s.hits(JWKS), 1, "one fetch serves every verification");

    clock.advance(Duration::from_secs(2 * 3600));
    v.verify(&token(&a, "a", "x", &clock)).unwrap();
    assert_eq!(s.hits(JWKS), 2, "a set older than refresh is fetched again");
}

#[test]
fn jwks_follows_key_rotation_at_most_once_per_min_refresh() {
    let s = Arc::new(Script::default());
    let (old, new) = (signer(1), signer(2));
    s.set_json(JWKS, &json!({"keys": [jwk(&old, "old")]}));
    let clock = Clock::new();
    let v = new_jwks(JWKS, remote(&s, 3600, 60), clock.opts()).unwrap();
    v.verify(&token(&old, "old", "x", &clock)).unwrap();
    assert_eq!(s.hits(JWKS), 1);

    clock.advance(Duration::from_secs(120));
    s.set_json(JWKS, &json!({"keys": [jwk(&new, "new")]}));
    v.verify(&token(&new, "new", "x", &clock))
        .expect("an unknown kid refetches the set");
    assert_eq!(s.hits(JWKS), 2);

    let e = refusal(&v, &token(&old, "old", "x", &clock));
    assert_eq!(e.kind(), Kind::InvalidToken);
    assert!(e.to_string().contains(r#"kid "old""#), "{e}");
    assert_eq!(s.hits(JWKS), 2, "no refetch within min_refresh");

    for _ in 0..5 {
        let e = refusal(&v, &token(&new, "made-up", "x", &clock));
        assert_eq!(e.kind(), Kind::InvalidToken);
    }
    assert_eq!(s.hits(JWKS), 2, "unknown kids cannot hammer the issuer");

    clock.advance(Duration::from_secs(61));
    let _ = v.verify(&token(&new, "made-up", "x", &clock));
    assert_eq!(s.hits(JWKS), 3, "past min_refresh an unknown kid refetches");
}

#[test]
fn jwks_unavailable_then_recovers_and_keeps_the_last_good_set() {
    let s = Arc::new(Script::default());
    let k = signer(1);
    s.set(JWKS, Err(format!("GET {JWKS}: status 503")));
    let clock = Clock::new();
    let v = new_jwks(JWKS, remote(&s, 3600, 60), clock.opts()).unwrap();

    let raw = token(&k, "k", "x", &clock);
    let e = refusal(&v, &raw);
    assert_eq!(e.kind(), Kind::KeySetUnavailable);
    assert!(e.to_string().contains("status 503"), "{e}");
    assert_eq!(refusal(&v, &raw).kind(), Kind::KeySetUnavailable);
    assert_eq!(s.hits(JWKS), 1, "no retry within min_refresh");

    s.set_json(JWKS, &json!({"keys": [jwk(&k, "k")]}));
    clock.advance(Duration::from_secs(120));
    v.verify(&token(&k, "k", "x", &clock)).unwrap();

    s.set(JWKS, Err(format!("GET {JWKS}: status 503")));
    clock.advance(Duration::from_secs(2 * 3600));
    v.verify(&token(&k, "k", "x", &clock))
        .expect("a failed refetch keeps the last good set");
    assert_eq!(s.hits(JWKS), 3);

    // An empty set is a failure too, and keeps the last good set.
    s.set_json(JWKS, &json!({"keys": []}));
    clock.advance(Duration::from_secs(2 * 3600));
    v.verify(&token(&k, "k", "x", &clock)).unwrap();
    assert_eq!(s.hits(JWKS), 4);
}

#[test]
fn a_set_with_no_signing_key_is_unavailable_and_non_sig_keys_are_skipped() {
    let s = Arc::new(Script::default());
    let k = signer(1);
    let mut enc = jwk(&signer(2), "enc");
    enc["use"] = json!("enc");
    s.set_json(JWKS, &json!({"keys": [enc.clone()]}));
    let clock = Clock::new();
    let v = new_jwks(JWKS, remote(&s, 3600, 60), clock.opts()).unwrap();
    let e = refusal(&v, &token(&k, "k", "x", &clock));
    assert_eq!(e.kind(), Kind::KeySetUnavailable);
    assert!(e.to_string().contains("holds no signing key"), "{e}");

    s.set_json(JWKS, &json!({"keys": [enc, jwk(&k, "k")]}));
    clock.advance(Duration::from_secs(120));
    v.verify(&token(&k, "k", "x", &clock)).unwrap();
    let e = refusal(&v, &token(&signer(2), "enc", "x", &clock));
    assert!(e.to_string().contains(r#"kid "enc""#), "{e}");
}

#[test]
fn an_oversize_or_malformed_document_is_refused() {
    let s = Arc::new(Script::default());
    let k = signer(1);
    let clock = Clock::new();
    // Even a fetcher that did not cap its read cannot slip one past.
    s.set(JWKS, Ok(vec![b' '; (1 << 20) + 1]));
    let v = new_jwks(JWKS, remote(&s, 3600, 60), clock.opts()).unwrap();
    let e = refusal(&v, &token(&k, "k", "x", &clock));
    assert_eq!(e.kind(), Kind::KeySetUnavailable);
    assert!(e.to_string().contains("larger than 1048576 bytes"), "{e}");

    s.set(JWKS, Ok(b"not json".to_vec()));
    clock.advance(Duration::from_secs(120));
    let e = refusal(&v, &token(&k, "k", "x", &clock));
    assert_eq!(e.kind(), Kind::KeySetUnavailable);
    assert!(e.to_string().contains(&format!("GET {JWKS}:")), "{e}");
}

const ISS: &str = "https://issuer.test";
const WELL_KNOWN: &str = "https://issuer.test/.well-known/openid-configuration";

#[test]
fn oidc_discovery_resolves_jwks_uri_and_pins_the_issuer() {
    let s = Arc::new(Script::default());
    let k = signer(1);
    s.set_json(WELL_KNOWN, &json!({"issuer": ISS, "jwks_uri": JWKS}));
    s.set_json(JWKS, &json!({"keys": [jwk(&k, "k")]}));
    let clock = Clock::new();
    let v = new_oidc(ISS, remote(&s, 3600, 60), clock.opts()).unwrap();

    let t = v.verify(&token(&k, "k", ISS, &clock)).unwrap();
    assert_eq!(t.issuer, ISS);
    let e = refusal(&v, &token(&k, "k", "https://other.test", &clock));
    assert_eq!(e.kind(), Kind::InvalidToken, "iss must be the issuer");
    assert_eq!(s.hits(WELL_KNOWN), 1, "discovery runs once");
}

#[test]
fn oidc_discovery_refusals() {
    let k = signer(1);
    let clock = Clock::new();
    let unavailable = |doc: Value, want: &str| {
        let s = Arc::new(Script::default());
        s.set_json(WELL_KNOWN, &doc);
        s.set_json(JWKS, &json!({"keys": [jwk(&k, "k")]}));
        let v = new_oidc(ISS, remote(&s, 3600, 60), clock.opts()).unwrap();
        let e = refusal(&v, &token(&k, "k", ISS, &clock));
        assert_eq!(e.kind(), Kind::KeySetUnavailable, "{doc}");
        assert!(e.to_string().contains(want), "{doc}: {e}");
        assert_eq!(s.hits(JWKS), 0, "{doc}: the key set is never fetched");
    };
    unavailable(
        json!({"issuer": "https://impostor.test", "jwks_uri": JWKS}),
        "impostor",
    );
    // Exact match: a trailing slash is a different issuer.
    unavailable(
        json!({"issuer": "https://issuer.test/", "jwks_uri": JWKS}),
        "names issuer",
    );
    unavailable(json!({"issuer": ISS}), "names no jwks_uri");
    unavailable(
        json!({"issuer": ISS, "jwks_uri": "http://idp.test/jwks.json"}),
        "jwks_uri: ",
    );
    unavailable(
        json!({"issuer": ISS, "jwks_uri": "ftp://idp.test/jwks.json"}),
        "scheme must be https",
    );
}

#[test]
fn oidc_discovery_is_retried_at_most_once_per_min_refresh() {
    let s = Arc::new(Script::default());
    let k = signer(1);
    let clock = Clock::new();
    let v = new_oidc(ISS, remote(&s, 3600, 60), clock.opts()).unwrap();
    let raw = token(&k, "k", ISS, &clock);
    assert_eq!(refusal(&v, &raw).kind(), Kind::KeySetUnavailable);
    assert_eq!(refusal(&v, &raw).kind(), Kind::KeySetUnavailable);
    assert_eq!(s.hits(WELL_KNOWN), 1);

    s.set_json(WELL_KNOWN, &json!({"issuer": ISS, "jwks_uri": JWKS}));
    s.set_json(JWKS, &json!({"keys": [jwk(&k, "k")]}));
    clock.advance(Duration::from_secs(61));
    v.verify(&token(&k, "k", ISS, &clock)).unwrap();
}

#[test]
fn urls_must_be_https_or_loopback_http() {
    for (raw, ok) in [
        ("https://idp.example/jwks", true),
        ("http://127.0.0.1:8080/jwks", true),
        ("http://[::1]/jwks", true),
        ("http://localhost/jwks", true),
        ("http://idp.example/jwks", false),
        ("ftp://idp.example/jwks", false),
        ("/relative", false),
        ("https://", false),
        ("http://127.0.0.1.evil.example", false),
        ("http://127.0.0.1@evil.example/jwks", false),
    ] {
        assert_eq!(check_url(raw).is_ok(), ok, "{raw}: {:?}", check_url(raw));
    }
    let s = Arc::new(Script::default());
    let r = || remote(&s, 0, 0);
    assert_eq!(
        new_jwks("http://idp.example/jwks", r(), Options::default())
            .unwrap_err()
            .kind(),
        Kind::Config
    );
    assert_eq!(
        new_oidc("http://idp.example", r(), Options::default())
            .unwrap_err()
            .kind(),
        Kind::Config
    );
}

// --- Go's recorded exchanges -------------------------------------------

/// Replays the xrr cassette Go recorded: each `http-<fp>.req.yaml` has
/// a `.resp.yaml` twin.
struct Cassette(HashMap<String, (u16, String)>);

impl Cassette {
    fn load(name: &str) -> Self {
        let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../go/transport/authn/testdata/cassettes")
            .join(name);
        let mut m = HashMap::new();
        for e in std::fs::read_dir(&dir).unwrap() {
            let p = e.unwrap().path();
            let f = p.file_name().unwrap().to_str().unwrap().to_string();
            let Some(stem) = f.strip_suffix(".req.yaml") else {
                continue;
            };
            let read = |suffix: &str| -> Value {
                serde_norway::from_str(
                    &std::fs::read_to_string(dir.join(format!("{stem}.{suffix}.yaml"))).unwrap(),
                )
                .unwrap()
            };
            let (req, resp) = (read("req"), read("resp"));
            assert_eq!(req["payload"]["method"], "GET");
            m.insert(
                req["payload"]["url"].as_str().unwrap().to_string(),
                (
                    resp["payload"]["status"].as_u64().unwrap() as u16,
                    resp["payload"]["body"].as_str().unwrap().to_string(),
                ),
            );
        }
        assert!(!m.is_empty());
        Self(m)
    }
}

impl Fetch for Cassette {
    fn get(&self, url: &str) -> Reply {
        match self.0.get(url) {
            Some((200, body)) => Ok(body.clone().into_bytes()),
            Some((s, _)) => Err(format!("GET {url}: status {s}")),
            None => Err(format!("GET {url}: not in the cassette")),
        }
    }
}

const GOOGLE: &str = "https://accounts.google.com";
const GOOGLE_JWKS: &str = "https://www.googleapis.com/oauth2/v3/certs";

fn google_kids(c: &Cassette) -> Vec<String> {
    let doc: Value = serde_json::from_slice(c.get(GOOGLE_JWKS).unwrap().as_slice()).unwrap();
    let kids: Vec<String> = doc["keys"]
        .as_array()
        .unwrap()
        .iter()
        .map(|k| k["kid"].as_str().unwrap().to_string())
        .collect();
    assert!(!kids.is_empty());
    kids
}

/// A token Google did not sign: its header names `kid` and RS256, its
/// signature is noise.
fn forged(kid: &str, iss: &str, clock: &Clock) -> String {
    let now = clock.secs();
    format!(
        "{}.{}.{}",
        b64(json!({"alg": "RS256", "kid": kid}).to_string().as_bytes()),
        b64(
            json!({"sub": "alice", "iss": iss, "aud": "kit-test.apps.googleusercontent.com",
                   "iat": now, "exp": now + 3600})
            .to_string()
            .as_bytes()
        ),
        b64(&[7u8; 256])
    )
}

#[test]
fn cassette_oidc_discovery_against_google() {
    let c = Arc::new(Cassette::load("google"));
    let kids = google_kids(&c);
    let clock = Clock::new();
    let mut opts = clock.opts();
    opts.audience = vec!["kit-test.apps.googleusercontent.com".into()];
    let r = Remote {
        fetch: Some(c.clone()),
        ..Remote::default()
    };
    let v = new_oidc(GOOGLE, r, opts).unwrap();

    let e = refusal(&v, &forged(&kids[0], GOOGLE, &clock));
    assert_eq!(e.kind(), Kind::InvalidToken);
    assert!(
        e.to_string()
            .contains("signature verifies with no trusted key"),
        "the kid resolved to one of Google's keys, whose signature check failed: {e}"
    );
    let e = refusal(&v, &forged("not-a-google-kid", GOOGLE, &clock));
    assert!(
        e.to_string()
            .contains(r#"no trusted key has kid "not-a-google-kid""#),
        "{e}"
    );
}

#[test]
fn cassette_jwks_against_google() {
    let c = Arc::new(Cassette::load("google"));
    let kids = google_kids(&c);
    let clock = Clock::new();
    let r = Remote {
        fetch: Some(c.clone()),
        ..Remote::default()
    };
    let v = new_jwks(GOOGLE_JWKS, r, clock.opts()).unwrap();
    let e = refusal(&v, &forged(kids.last().unwrap(), GOOGLE, &clock));
    assert!(
        e.to_string()
            .contains("signature verifies with no trusted key"),
        "{e}"
    );
}

// --- the guarded HTTP fetcher -------------------------------------------

fn fetcher() -> HttpFetch {
    HttpFetch::new(NetPolicy::online(), Duration::from_secs(5)).unwrap()
}

#[test]
fn http_fetch_verifies_end_to_end_over_loopback() {
    let mut server = mockito::Server::new();
    let k = signer(1);
    let base = server.url();
    let _w = server
        .mock("GET", "/.well-known/openid-configuration")
        .with_body(json!({"issuer": base, "jwks_uri": format!("{base}/jwks.json")}).to_string())
        .create();
    let _j = server
        .mock("GET", "/jwks.json")
        .match_header("accept", "application/json")
        .with_body(json!({"keys": [jwk(&k, "k")]}).to_string())
        .expect(1)
        .create();
    let clock = Clock::new();
    let v = new_oidc(&base, Remote::default(), clock.opts()).unwrap();
    // The same verifier from a plain thread and from inside a runtime.
    v.verify(&token(&k, "k", &base, &clock)).unwrap();
    tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .unwrap()
        .block_on(async {
            v.verify(&token(&k, "k", &base, &clock)).unwrap();
        });
    _j.assert();
}

#[test]
fn http_fetch_never_follows_a_redirect() {
    let mut server = mockito::Server::new();
    let target = server
        .mock("GET", "/elsewhere")
        .with_body("{}")
        .expect(0)
        .create();
    let _r = server
        .mock("GET", "/jwks.json")
        .with_status(302)
        .with_header("location", &format!("{}/elsewhere", server.url()))
        .create();
    let e = fetcher()
        .get(&format!("{}/jwks.json", server.url()))
        .unwrap_err();
    assert!(e.contains("status 302"), "{e}");
    target.assert();
}

#[test]
fn http_fetch_status_and_size_cap() {
    let mut server = mockito::Server::new();
    let _a = server.mock("GET", "/down").with_status(503).create();
    let _b = server
        .mock("GET", "/big")
        .with_body(vec![b' '; (1 << 20) + 1])
        .create();
    let _c = server
        .mock("GET", "/edge")
        .with_body(vec![b' '; 1 << 20])
        .create();
    let f = fetcher();
    let e = f.get(&format!("{}/down", server.url())).unwrap_err();
    assert!(e.contains("status 503"), "{e}");
    let e = f.get(&format!("{}/big", server.url())).unwrap_err();
    assert!(e.contains("larger than 1048576 bytes"), "{e}");
    assert_eq!(
        f.get(&format!("{}/edge", server.url())).unwrap().len(),
        1 << 20,
        "exactly the cap is accepted"
    );
}

#[test]
fn http_fetch_times_out() {
    let l = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("http://{}/jwks.json", l.local_addr().unwrap());
    // Accept and never answer.
    let held = std::thread::spawn(move || l.accept().map(|(s, _)| s));
    let f = HttpFetch::new(NetPolicy::online(), Duration::from_millis(300)).unwrap();
    let t = std::time::Instant::now();
    assert!(f.get(&url).is_err());
    assert!(t.elapsed() < Duration::from_secs(5));
    drop(held);
}

#[test]
fn http_fetch_honours_the_offline_policy() {
    let f = HttpFetch::new(NetPolicy::offline(), Duration::from_secs(5)).unwrap();
    let e = f.get("https://idp.example/jwks").unwrap_err();
    assert!(e.contains("network disabled by --offline"), "{e}");
    // Loopback stays reachable offline.
    let mut server = mockito::Server::new();
    let _m = server.mock("GET", "/j").with_body("{}").create();
    assert!(f.get(&format!("{}/j", server.url())).is_ok());
}

#[test]
fn fetches_are_counted_through_a_shared_fetcher() {
    // A Remote built once serves several verifiers' fetches.
    let n = Arc::new(AtomicUsize::new(0));
    struct Count(Arc<AtomicUsize>);
    impl Fetch for Count {
        fn get(&self, _: &str) -> Reply {
            self.0.fetch_add(1, Ordering::SeqCst);
            Err("down".into())
        }
    }
    let r = Remote {
        fetch: Some(Arc::new(Count(n.clone()))),
        ..Remote::default()
    };
    let clock = Clock::new();
    let a = new_jwks(JWKS, r.clone(), clock.opts()).unwrap();
    let b = new_jwks(JWKS, r, clock.opts()).unwrap();
    let k = signer(1);
    let _ = a.verify(&token(&k, "k", "x", &clock));
    let _ = b.verify(&token(&k, "k", "x", &clock));
    assert_eq!(n.load(Ordering::SeqCst), 2);
}
