//! API keys over a kv store: the Go `apikey_test.go` suite ported, the
//! record-shape additions (Get, branded prefix and hash domain, metadata),
//! and the cross-language corpus in `contracts/authn-v1`.
#![cfg(feature = "authn-apikeys")]

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use hop_top_kit::authn::{
    ApiKeys, AuthnError, Claims, Credentials, Kind, NewApiKey, API_KEY_HASH_DOMAIN,
};
use hop_top_kit::kv::{KvError, Store};
use serde_json::Value;

/// A kv store several `ApiKeys` can share, as two processes share a
/// file.
#[derive(Clone, Default)]
struct Mem {
    rows: Arc<Mutex<BTreeMap<Vec<u8>, Vec<u8>>>>,
    fail_reads: bool,
}

impl Store for Mem {
    fn put(&self, key: &[u8], value: &[u8]) -> Result<(), KvError> {
        self.rows
            .lock()
            .unwrap()
            .insert(key.to_vec(), value.to_vec());
        Ok(())
    }
    fn get(&self, key: &[u8]) -> Result<Option<Vec<u8>>, KvError> {
        if self.fail_reads {
            return Err(KvError::BackendUnavailable("disk on fire"));
        }
        Ok(self.rows.lock().unwrap().get(key).cloned())
    }
    fn delete(&self, key: &[u8]) -> Result<(), KvError> {
        self.rows.lock().unwrap().remove(key);
        Ok(())
    }
    fn list(&self, prefix: &[u8]) -> Result<Vec<Vec<u8>>, KvError> {
        Ok(self
            .rows
            .lock()
            .unwrap()
            .keys()
            .filter(|k| k.starts_with(prefix))
            .cloned()
            .collect())
    }
}

/// A settable clock.
#[derive(Clone)]
struct Clock(Arc<Mutex<SystemTime>>);

impl Clock {
    fn new(secs: u64) -> Self {
        Self(Arc::new(Mutex::new(UNIX_EPOCH + Duration::from_secs(secs))))
    }
    fn advance(&self, d: Duration) {
        *self.0.lock().unwrap() += d;
    }
    fn now_fn(&self) -> Arc<dyn Fn() -> SystemTime + Send + Sync> {
        let c = self.0.clone();
        Arc::new(move || *c.lock().unwrap())
    }
}

fn keys(store: &Mem, clock: &Clock) -> ApiKeys {
    ApiKeys::new(store.clone()).with_clock(clock.now_fn())
}

fn spec(principal: &str) -> NewApiKey {
    NewApiKey {
        principal: principal.into(),
        ..NewApiKey::default()
    }
}

fn creds<'a>(authorization: Option<&'a str>, api_key: Option<&'a str>) -> Credentials<'a> {
    Credentials {
        authorization,
        api_key,
    }
}

// --- Go: TestAPIKeyLifecycle ---------------------------------------------

#[test]
fn lifecycle_issue_verify_list_revoke() {
    let store = Mem::default();
    let clock = Clock::new(1_790_000_000);
    let k = keys(&store, &clock);

    let (raw, rec) = k
        .create(NewApiKey {
            tenant: "acme".into(),
            scopes: vec!["items:read".into()],
            ..spec("ci-bot")
        })
        .unwrap();
    let parts: Vec<&str> = raw.split('_').collect();
    assert_eq!(parts.len(), 3);
    assert_eq!(parts[0], "kit");
    assert_eq!(parts[1], rec.id);
    assert_eq!(parts[2].len(), 64, "a 256-bit secret");

    let stored = store
        .get(format!("apikey/{}", rec.id).as_bytes())
        .unwrap()
        .expect("stored");
    assert!(
        !String::from_utf8_lossy(&stored).contains(parts[2]),
        "the secret is never stored"
    );

    let got = k.verify(&raw).unwrap();
    assert_eq!(
        got.claims(),
        Claims {
            subject: "ci-bot".into(),
            tenant: "acme".into(),
            scopes: vec!["items:read".into()]
        }
    );

    clock.advance(Duration::from_secs(1));
    let (raw2, _) = k.create(spec("alice")).unwrap();
    let list = k.list().unwrap();
    assert_eq!(list.len(), 2);
    assert_eq!(list[0].principal, "alice", "newest first");

    let revoked = k.revoke(&rec.id).unwrap();
    assert!(revoked.revoked_at.is_some());
    let err = k.verify(&raw).unwrap_err();
    assert_eq!(err.kind(), Kind::InvalidToken);
    assert!(err.to_string().contains("revoked"));
    k.verify(&raw2).expect("revoking one key leaves the others");
    assert_eq!(k.list().unwrap().len(), 2, "a revoked key stays listed");
    assert!(k.revoke(&rec.id).is_ok(), "revoking twice is fine");

    assert_eq!(
        k.revoke("0000000000000000").unwrap_err(),
        AuthnError::ApiKeyNotFound("0000000000000000".into())
    );
}

// --- Go: TestAPIKeyRefusals ----------------------------------------------

#[test]
fn refusals() {
    let clock = Clock::new(1_790_000_000);
    let store = Mem::default();
    let k = keys(&store, &clock);
    let (raw, rec) = k
        .create(NewApiKey {
            ttl: Duration::from_secs(3600),
            ..spec("p")
        })
        .unwrap();
    k.verify(&raw).unwrap();

    let wrong_secret = format!("kit_{}_{}", rec.id, "0".repeat(64));
    assert!(k
        .verify(&wrong_secret)
        .unwrap_err()
        .to_string()
        .contains("unknown api key"));
    let unknown_id = format!("kit_{}_{}", "1".repeat(16), "0".repeat(64));
    assert!(k
        .verify(&unknown_id)
        .unwrap_err()
        .to_string()
        .contains("unknown api key"));
    for bad in [
        "kit_x_y",
        "nope",
        "kit_a_b_c",
        &raw.to_uppercase(),
        &format!("other_{}_{}", rec.id, "0".repeat(64)),
    ] {
        let e = k.verify(bad).unwrap_err();
        assert_eq!(e.kind(), Kind::InvalidToken, "{bad}");
        assert!(e.to_string().contains("not a kit api key"), "{bad}: {e}");
    }
    assert_eq!(k.verify("  ").unwrap_err().kind(), Kind::NoToken);

    clock.advance(Duration::from_secs(3600));
    let e = k.verify(&raw).unwrap_err();
    assert_eq!(e.kind(), Kind::InvalidToken);
    assert!(e.to_string().contains("expired"));

    assert!(
        k.create(NewApiKey::default()).is_err(),
        "a key names a principal"
    );
    assert!(k
        .create(NewApiKey {
            scopes: vec!["a b".into()],
            ..spec("p")
        })
        .is_err());
}

// --- Go: TestAPIKeyAuthFunc ----------------------------------------------

#[test]
fn auth_func_reads_x_api_key_else_bearer() {
    let k = ApiKeys::new(Mem::default());
    let (raw, _) = k
        .create(NewApiKey {
            scopes: vec!["items:export".into(), "other".into()],
            ..spec("svc")
        })
        .unwrap();
    let f = k.auth_func();
    let bearer = format!("Bearer {raw}");

    for (name, c, ok, text) in [
        ("x-api-key", creds(None, Some(&raw)), true, ""),
        ("bearer", creds(Some(&bearer), None), true, ""),
        (
            "none",
            creds(None, None),
            false,
            "X-API-Key or Authorization: Bearer",
        ),
        (
            "wrong",
            creds(None, Some("kit_x_y")),
            false,
            "not a kit api key",
        ),
        (
            "x-api-key wins",
            creds(Some(&bearer), Some("nope")),
            false,
            "not a kit api key",
        ),
        (
            "basic",
            creds(Some("Basic abc"), None),
            false,
            "not a bearer token: send it as X-API-Key",
        ),
    ] {
        let res = f(&c);
        if ok {
            let claims = res.unwrap_or_else(|e| panic!("{name}: {e}"));
            assert_eq!(claims.subject, "svc");
            assert_eq!(claims.scopes, ["items:export", "other"]);
        } else {
            let msg = res.unwrap_err().to_string();
            assert!(msg.contains(text), "{name}: {msg}");
        }
    }
}

// --- Go: TestAPIKeyStoreFailureIsNotAVerdict -----------------------------

#[test]
fn a_store_that_cannot_be_read_is_not_a_verdict() {
    let mem = Mem::default();
    let (raw, _) = ApiKeys::new(mem.clone()).create(spec("p")).unwrap();
    let broken = Mem {
        fail_reads: true,
        ..mem
    };
    let e = ApiKeys::new(broken).verify(&raw).unwrap_err();
    assert_eq!(e.kind(), Kind::KeySetUnavailable);
}

#[test]
fn with_check_is_the_revocation_hook() {
    let denied = Arc::new(Mutex::new(Vec::<String>::new()));
    let d = denied.clone();
    let k = ApiKeys::new(Mem::default()).with_check(Arc::new(move |t| {
        if d.lock().unwrap().contains(&t.id) {
            Err("denied".into())
        } else {
            Ok(())
        }
    }));
    let (raw, rec) = k.create(spec("p")).unwrap();
    k.verify(&raw).unwrap();
    denied.lock().unwrap().push(rec.id);
    let e = k.verify(&raw).unwrap_err();
    assert_eq!(e.kind(), Kind::InvalidToken);
    assert!(e.to_string().contains("denied"));
}

// --- Record shape: Get, prefix, hash domain, metadata ----------------------

#[test]
fn get_returns_the_record_or_none() {
    let k = ApiKeys::new(Mem::default());
    let (_, rec) = k.create(spec("p")).unwrap();
    assert_eq!(k.get(&rec.id).unwrap().unwrap().principal, "p");
    assert!(k.get("ffffffffffffffff").unwrap().is_none());
}

#[test]
fn a_branded_prefix_and_hash_domain_round_trip() {
    let store = Mem::default();
    let k = ApiKeys::new(store.clone())
        .with_prefix("acme")
        .unwrap()
        .with_hash_domain("acme-keys-v3");
    let (raw, rec) = k.create(spec("p")).unwrap();
    assert!(raw.starts_with("acme_"));
    k.verify(&raw).unwrap();

    // The default keys neither recognize the prefix nor the hash.
    let default = ApiKeys::new(store);
    assert!(default
        .verify(&raw)
        .unwrap_err()
        .to_string()
        .contains("not a kit api key"));
    let as_kit = raw.replacen("acme", "kit", 1);
    assert!(default
        .verify(&as_kit)
        .unwrap_err()
        .to_string()
        .contains("unknown api key"));
    assert_ne!(rec.hash, {
        let (_, d) = ApiKeys::new(Mem::default()).create(spec("p")).unwrap();
        d.hash
    });

    assert!(ApiKeys::new(Mem::default()).with_prefix("").is_err());
    assert!(ApiKeys::new(Mem::default()).with_prefix("a_b").is_err());
}

#[test]
fn metadata_round_trips_and_last_used_updates_on_verify() {
    let clock = Clock::new(1_790_000_000);
    let store = Mem::default();
    let k = keys(&store, &clock).with_last_used(true);
    let mut labels = BTreeMap::new();
    labels.insert("team".to_string(), "infra".to_string());
    let (raw, rec) = k
        .create(NewApiKey {
            name: "ci".into(),
            labels: labels.clone(),
            ..spec("p")
        })
        .unwrap();
    assert!(rec.last_used_at.is_none());

    clock.advance(Duration::from_secs(90));
    let used = k.verify(&raw).unwrap();
    assert_eq!(
        used.last_used_at,
        Some(UNIX_EPOCH + Duration::from_secs(1_790_000_090))
    );
    let stored = k.get(&rec.id).unwrap().unwrap();
    assert_eq!(stored.name, "ci");
    assert_eq!(stored.labels, labels);
    assert_eq!(stored.last_used_at, used.last_used_at);

    // Tracking is off by default: a verify writes nothing.
    let quiet = keys(&store, &clock);
    clock.advance(Duration::from_secs(10));
    quiet.verify(&raw).unwrap();
    assert_eq!(
        quiet.get(&rec.id).unwrap().unwrap().last_used_at,
        stored.last_used_at
    );

    // A revoked key is never resurrected by the last-used write.
    k.revoke(&rec.id).unwrap();
    assert!(k.verify(&raw).is_err());
    assert!(k.get(&rec.id).unwrap().unwrap().revoked_at.is_some());
}

// --- cross-language record compatibility ---------------------------------

#[test]
fn the_record_is_the_json_go_writes() {
    let store = Mem::default();
    let clock = Clock::new(1_790_000_000);
    let k = keys(&store, &clock);
    let (_, rec) = k
        .create(NewApiKey {
            tenant: "acme".into(),
            scopes: vec!["a".into()],
            ttl: Duration::from_secs(60),
            ..spec("p")
        })
        .unwrap();
    let v: Value = serde_json::from_slice(
        &store
            .get(format!("apikey/{}", rec.id).as_bytes())
            .unwrap()
            .unwrap(),
    )
    .unwrap();
    assert_eq!(v["created_at"], "2026-09-21T14:13:20Z");
    assert_eq!(v["expires_at"], "2026-09-21T14:14:20Z");
    for absent in ["revoked_at", "name", "labels", "last_used_at"] {
        assert!(v.get(absent).is_none(), "{absent} is omitted when empty");
    }
    assert_eq!(v["hash"].as_str().unwrap().len(), 64);

    // Records Go wrote: fractional seconds, the zero time for "unset".
    let go = br#"{"id":"0123456789abcdef","principal":"p","created_at":"2026-09-28T12:00:00.123456789Z","expires_at":"0001-01-01T00:00:00Z","hash":"00"}"#;
    store.put(b"apikey/0123456789abcdef", go).unwrap();
    let got = k.get("0123456789abcdef").unwrap().unwrap();
    assert!(got.expires_at.is_none());
    assert_eq!(
        got.created_at
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .subsec_nanos(),
        123_456_789
    );
}

#[test]
fn shared_corpus_hashes_match_go() {
    let corpus = {
        let path = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../../contracts/authn-v1/vectors.json");
        serde_json::from_slice::<Value>(&std::fs::read(path).unwrap()).unwrap()
    };
    let vectors = corpus["api_keys"].as_array().unwrap();
    assert!(!vectors.is_empty());
    for v in vectors {
        let (key, hash) = (v["key"].as_str().unwrap(), v["hash"].as_str().unwrap());
        assert_eq!(v["domain"], API_KEY_HASH_DOMAIN);
        let parts: Vec<&str> = key.split('_').collect();
        // Seed a record the way Go stored it; Rust must accept the key.
        let store = Mem::default();
        let rec = format!(
            r#"{{"id":"{}","principal":"p","created_at":"2026-09-28T12:00:00Z","hash":"{hash}"}}"#,
            parts[1]
        );
        store
            .put(format!("apikey/{}", parts[1]).as_bytes(), rec.as_bytes())
            .unwrap();
        ApiKeys::new(store)
            .verify(key)
            .unwrap_or_else(|e| panic!("{}: {e}", v["name"]));
    }
}

fn records_corpus() -> Value {
    let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../contracts/authn-v1/apikey-records.json");
    serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
}

fn seed(store: &Mem, rec: &Value) {
    store
        .put(
            format!("apikey/{}", rec["id"].as_str().unwrap()).as_bytes(),
            rec.to_string().as_bytes(),
        )
        .unwrap();
}

fn stored(store: &Mem, id: &str) -> Value {
    serde_json::from_slice(
        &store
            .get(format!("apikey/{id}").as_bytes())
            .unwrap()
            .unwrap(),
    )
    .unwrap()
}

fn secs(rfc3339: &str) -> u64 {
    // The corpus is whole-second UTC; reuse the crate's own parser via a
    // stored record so the test shares no second implementation.
    let store = Mem::default();
    let rec = format!(r#"{{"id":"x","principal":"p","created_at":"{rfc3339}","hash":""}}"#);
    store.put(b"apikey/x", rec.as_bytes()).unwrap();
    ApiKeys::new(store)
        .get("x")
        .unwrap()
        .unwrap()
        .created_at
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_secs()
}

#[test]
fn apikey_records_corpus_hash_domains() {
    let c = records_corpus();
    for v in c["hash_domains"].as_array().unwrap() {
        let key = v["key"].as_str().unwrap();
        let id = key.split('_').nth(1).unwrap();
        let store = Mem::default();
        seed(
            &store,
            &serde_json::json!({"id": id, "principal": "p",
                "created_at": "2026-09-28T12:00:00Z", "hash": v["hash"]}),
        );
        let keys = ApiKeys::new(store)
            .with_prefix(v["prefix"].as_str().unwrap())
            .unwrap();
        keys.clone()
            .with_hash_domain(v["domain"].as_str().unwrap())
            .verify(key)
            .unwrap_or_else(|e| panic!("{}: {e}", v["name"]));
        let err = keys
            .with_hash_domain("other-domain")
            .verify(key)
            .unwrap_err();
        assert!(
            matches!(err, AuthnError::InvalidToken(_)),
            "{}: {err}",
            v["name"]
        );
    }
}

#[test]
fn apikey_records_corpus_write_back_keeps_every_field() {
    let c = records_corpus();
    for v in c["records"].as_array().unwrap() {
        let store = Mem::default();
        seed(&store, &v["record"]);
        let at = secs(v["revoke_at"].as_str().unwrap());
        let keys = ApiKeys::new(store.clone())
            .with_clock(Arc::new(move || UNIX_EPOCH + Duration::from_secs(at)));
        let id = v["record"]["id"].as_str().unwrap();
        keys.revoke(id).unwrap();
        assert_eq!(stored(&store, id), v["after_revoke"], "{}", v["name"]);
    }
}

#[test]
fn apikey_records_corpus_last_used() {
    let c = records_corpus();
    for v in c["last_used"].as_array().unwrap() {
        let store = Mem::default();
        seed(&store, &v["record"]);
        let at = secs(v["verify_at"].as_str().unwrap());
        let keys = ApiKeys::new(store.clone())
            .with_prefix(v["prefix"].as_str().unwrap())
            .unwrap()
            .with_hash_domain(v["domain"].as_str().unwrap())
            .with_clock(Arc::new(move || UNIX_EPOCH + Duration::from_secs(at)));
        let key = v["key"].as_str().unwrap();
        let id = v["record"]["id"].as_str().unwrap();
        keys.verify(key).unwrap();
        assert!(keys.get(id).unwrap().unwrap().last_used_at.is_none());
        keys.clone().with_last_used(true).verify(key).unwrap();
        assert_eq!(stored(&store, id), v["record"], "record not rewritten");
        let want = secs(v["stored_last_used_at"].as_str().unwrap());
        let got = keys.get(id).unwrap().unwrap().last_used_at.unwrap();
        assert_eq!(got.duration_since(UNIX_EPOCH).unwrap().as_secs(), want);
    }
}

type Hook = Arc<Mutex<Option<Box<dyn FnOnce() + Send>>>>;

/// A store that runs a hook once, after the first read of a key.
#[derive(Clone)]
struct AfterGet {
    inner: Mem,
    hook: Hook,
}

impl Store for AfterGet {
    fn put(&self, k: &[u8], v: &[u8]) -> Result<(), KvError> {
        self.inner.put(k, v)
    }
    fn get(&self, k: &[u8]) -> Result<Option<Vec<u8>>, KvError> {
        let out = self.inner.get(k);
        let hook = self.hook.lock().unwrap().take();
        if let Some(h) = hook {
            h();
        }
        out
    }
    fn delete(&self, k: &[u8]) -> Result<(), KvError> {
        self.inner.delete(k)
    }
    fn list(&self, p: &[u8]) -> Result<Vec<Vec<u8>>, KvError> {
        self.inner.list(p)
    }
}

#[test]
fn last_used_cannot_undo_a_concurrent_revoke() {
    let inner = Mem::default();
    let store = AfterGet {
        inner: inner.clone(),
        hook: Arc::default(),
    };
    let keys = ApiKeys::new(store.clone()).with_last_used(true);
    let (raw, rec) = keys.create(spec("p")).unwrap();
    let id = rec.id.clone();
    let other = ApiKeys::new(inner);
    *store.hook.lock().unwrap() = Some(Box::new(move || {
        other.revoke(&id).unwrap();
    }));
    keys.verify(&raw)
        .expect("the verdict was reached before the revoke landed");
    assert!(keys.verify(&raw).is_err(), "the revoke survives the stamp");
    let got = keys.get(&rec.id).unwrap().unwrap();
    assert!(got.revoked_at.is_some());
    assert!(got.last_used_at.is_some());
}
