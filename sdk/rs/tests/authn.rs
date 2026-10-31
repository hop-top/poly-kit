//! Conformance for `authn`: the Go `transport/authn` suite ported case
//! for case, plus the shared corpus in `contracts/authn-v1`. The corpus
//! is the source of truth: a failure means this port drifted from Go.
#![cfg(feature = "authn")]

use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use ed25519_dalek::{Signer, SigningKey};
use hop_top_kit::authn::{
    bearer_token, identity_of, keys_from_jwks, scopes_of, Alg, AuthnError, Claims, Credentials,
    Identity, Jwk, Key, Kind, Options, Verifier, VerifyKey,
};
use serde::Deserialize;
use serde_json::{json, Value};

const NOW: u64 = 1_790_000_000;

fn at(secs: u64) -> Arc<dyn Fn() -> SystemTime + Send + Sync> {
    Arc::new(move || UNIX_EPOCH + Duration::from_secs(secs))
}

fn opts() -> Options {
    Options {
        now: Some(at(NOW)),
        ..Options::default()
    }
}

fn b64(v: &[u8]) -> String {
    URL_SAFE_NO_PAD.encode(v)
}

/// A key pair from a fixed seed, so the suite is deterministic.
fn keypair(seed: u8) -> (SigningKey, Key) {
    let sk = SigningKey::from_bytes(&[seed; 32]);
    let key = Key::from_identity(sk.verifying_key().to_bytes()).unwrap();
    (sk, key)
}

/// Signs `claims` under EdDSA, naming `kid` in the header (none when
/// empty), as the Go suite's `sign` does.
fn sign(sk: &SigningKey, kid: &str, claims: &Value) -> String {
    let mut header = json!({"alg": "EdDSA", "typ": "JWT"});
    if !kid.is_empty() {
        header["kid"] = json!(kid);
    }
    sign_with_header(sk, &header, claims)
}

fn sign_with_header(sk: &SigningKey, header: &Value, claims: &Value) -> String {
    let input = format!(
        "{}.{}",
        b64(header.to_string().as_bytes()),
        b64(claims.to_string().as_bytes())
    );
    let sig = sk.sign(input.as_bytes());
    format!("{input}.{}", b64(&sig.to_bytes()))
}

/// A claim set every verifier below accepts, at `NOW`.
fn valid() -> Value {
    json!({
        "sub": "alice", "iss": "https://issuer.test", "aud": "kit-api",
        "tenant": "acme", "scopes": ["items:read", "items:write"],
        "iat": NOW, "exp": NOW + 3600,
    })
}

/// `base` with `kv` pairs set; a null deletes the claim.
fn with(base: Value, kv: &[(&str, Value)]) -> Value {
    let mut m = base;
    for (k, v) in kv {
        if v.is_null() {
            m.as_object_mut().unwrap().remove(*k);
        } else {
            m[*k] = v.clone();
        }
    }
    m
}

fn verifier(key: Key, o: Options) -> Verifier {
    Verifier::new(vec![key], o).unwrap()
}

fn refused(v: &Verifier, token: &str) -> String {
    let err = v.verify(token).unwrap_err();
    assert_eq!(err.kind(), Kind::InvalidToken, "{err}");
    err.to_string()
}

// --- Go: TestJWTVerifiesTheToolsOwnIdentityTokens -----------------------

#[test]
fn verifies_identity_tokens_with_the_identity_kid() {
    let (sk, key) = keypair(7);
    let kid = key.id.clone();
    assert_eq!(kid.len(), 16, "first 8 bytes of the SHA-256, hex");
    let raw = sign(
        &sk,
        &kid,
        &json!({"sub": "alice", "tenant": "acme", "scopes": ["items:read"],
                "iat": NOW, "exp": NOW + 3600}),
    );
    let tok = verifier(key, opts()).verify(&raw).unwrap();
    assert_eq!(
        tok.claims(),
        Claims {
            subject: "alice".into(),
            tenant: "acme".into(),
            scopes: vec!["items:read".into()]
        }
    );
    assert_eq!(tok.key_id, kid);
}

// --- Go: TestTimeChecksTolerateClockSkew ---------------------------------

#[test]
fn time_checks_tolerate_clock_skew() {
    let (sk, key) = keypair(1);
    let now = NOW as i64;
    // (name, claims, skew, accepted, error substring)
    type Case = (&'static str, Value, Option<Duration>, bool, &'static str);
    let cases: Vec<Case> = vec![
        ("fresh", valid(), None, true, ""),
        (
            "expired within skew",
            with(valid(), &[("exp", json!(now - 10))]),
            None,
            true,
            "",
        ),
        (
            "expired beyond skew",
            with(valid(), &[("exp", json!(now - 120))]),
            None,
            false,
            "expired",
        ),
        (
            "expired, no tolerance",
            with(valid(), &[("exp", json!(now - 10))]),
            Some(Duration::ZERO),
            false,
            "expired",
        ),
        (
            "nbf within skew",
            with(valid(), &[("nbf", json!(now + 10))]),
            None,
            true,
            "",
        ),
        (
            "nbf beyond skew",
            with(valid(), &[("nbf", json!(now + 300))]),
            None,
            false,
            "not valid yet",
        ),
        (
            "iat in the future",
            with(valid(), &[("iat", json!(now + 300))]),
            None,
            false,
            "future",
        ),
        (
            "custom skew",
            with(valid(), &[("exp", json!(now - 120))]),
            Some(Duration::from_secs(300)),
            true,
            "",
        ),
        (
            "no exp",
            with(valid(), &[("exp", Value::Null)]),
            None,
            false,
            "no exp",
        ),
        (
            "no sub",
            with(valid(), &[("sub", Value::Null)]),
            None,
            false,
            "no sub",
        ),
    ];
    for (name, claims, skew, ok, want) in cases {
        let v = verifier(
            key.clone(),
            Options {
                clock_skew: skew,
                ..opts()
            },
        );
        let res = v.verify(&sign(&sk, &key.id, &claims));
        if ok {
            assert!(res.is_ok(), "{name}: {res:?}");
        } else {
            let msg = res.unwrap_err().to_string();
            assert!(msg.contains(want), "{name}: {msg}");
            assert!(msg.starts_with("authn: invalid token: "), "{name}: {msg}");
        }
    }
}

// --- Go: TestIssuerAndAudienceAreChecked ---------------------------------

#[test]
fn issuer_and_audience_are_checked() {
    let (sk, key) = keypair(2);
    let raw = sign(
        &sk,
        &key.id,
        &with(valid(), &[("aud", json!(["other", "kit-api"]))]),
    );
    let cases: Vec<(&str, Options, &str)> = vec![
        (
            "both match",
            Options {
                issuer: Some("https://issuer.test".into()),
                audience: vec!["kit-api".into()],
                ..opts()
            },
            "",
        ),
        ("unset", opts(), ""),
        (
            "any audience",
            Options {
                audience: vec!["nope".into(), "other".into()],
                ..opts()
            },
            "",
        ),
        (
            "wrong issuer",
            Options {
                issuer: Some("https://elsewhere.test".into()),
                ..opts()
            },
            r#"issuer "https://issuer.test" is not "https://elsewhere.test""#,
        ),
        (
            "wrong audience",
            Options {
                audience: vec!["billing".into()],
                ..opts()
            },
            "does not include billing",
        ),
        (
            "audience trailing slash",
            Options {
                audience: vec!["kit-api/".into()],
                ..opts()
            },
            "",
        ),
        (
            "audience path is strict",
            Options {
                audience: vec!["kit-api/mcp".into()],
                ..opts()
            },
            "does not include",
        ),
    ];
    for (name, o, want) in cases {
        let res = verifier(key.clone(), o).verify(&raw);
        if want.is_empty() {
            assert!(res.is_ok(), "{name}: {res:?}");
        } else {
            let msg = res.unwrap_err().to_string();
            assert!(msg.contains(want), "{name}: {msg}");
        }
    }
}

// --- Go: TestKidRotation -------------------------------------------------

#[test]
fn each_key_verifies_the_tokens_naming_its_kid() {
    let (old_sk, old_key) = keypair(3);
    let (new_sk, new_key) = keypair(4);
    let (stranger_sk, stranger_key) = keypair(5);
    let v = Verifier::new(vec![new_key.clone(), old_key.clone()], opts()).unwrap();
    let claims = json!({"sub": "alice", "iat": NOW, "exp": NOW + 3600});

    for (sk, key) in [(&old_sk, &old_key), (&new_sk, &new_key)] {
        let tok = v.verify(&sign(sk, &key.id, &claims)).unwrap();
        assert_eq!(tok.key_id, key.id);
    }

    // The old key's signature under the new key's kid.
    let mismatched = sign(&old_sk, &new_key.id, &claims);
    // kid selects only the new key: signature fails with no fallback.
    assert!(refused(&v, &mismatched).contains("signature"));

    let stranger = sign(&stranger_sk, &stranger_key.id, &claims);
    refused(&v, &stranger);
}

#[test]
fn a_kid_no_key_has_tries_every_key() {
    let (sk, key) = keypair(6);
    let v = verifier(key, opts());
    for kid in ["their-kid", ""] {
        assert!(v.verify(&sign(&sk, kid, &valid())).is_ok(), "kid {kid:?}");
    }
}

// --- Go: TestUnsafeAlgorithmsAreRefused ----------------------------------

#[test]
fn unsafe_algorithms_are_refused_before_any_key_is_consulted() {
    let (sk, key) = keypair(8);
    let v = verifier(key.clone(), opts());
    let payload = b64(valid().to_string().as_bytes());

    let hs = format!(
        "{}.{payload}.{}",
        b64(br#"{"alg":"HS256","typ":"JWT"}"#),
        b64(&[1u8; 32])
    );
    assert!(refused(&v, &hs).contains("algorithm"));

    let none = format!("{}.{payload}.", b64(br#"{"alg":"none"}"#));
    assert!(refused(&v, &none).contains("algorithm"));

    // Lower-case spellings are not the registered names.
    let lower = format!(
        "{}.{payload}.{}",
        b64(br#"{"alg":"eddsa"}"#),
        b64(&[1u8; 64])
    );
    refused(&v, &lower);

    refused(&v, "not-a-token");
    refused(&v, "a.b.c.d.e");
    assert_eq!(v.verify("").unwrap_err().kind(), Kind::NoToken);
    assert_eq!(v.verify("  \t").unwrap_err().kind(), Kind::NoToken);

    // Critical extensions are not understood, so the token is refused.
    let crit = sign_with_header(
        &sk,
        &json!({"alg": "EdDSA", "kid": key.id, "crit": ["exp"], "exp": 1}),
        &valid(),
    );
    assert!(refused(&v, &crit).contains("critical"));
}

// --- Go: TestCheckIsTheRevocationHook ------------------------------------

#[test]
fn check_is_the_revocation_hook() {
    let (sk, key) = keypair(9);
    let revoked = Arc::new(Mutex::new(vec!["t-2".to_string()]));
    let r = revoked.clone();
    let v = verifier(
        key.clone(),
        Options {
            check: Some(Arc::new(move |t| {
                if r.lock().unwrap().contains(&t.id) {
                    Err("revoked".into())
                } else {
                    Ok(())
                }
            })),
            ..opts()
        },
    );
    assert!(v
        .verify(&sign(
            &sk,
            &key.id,
            &with(valid(), &[("jti", json!("t-1"))])
        ))
        .is_ok());
    let msg = refused(
        &v,
        &sign(&sk, &key.id, &with(valid(), &[("jti", json!("t-2"))])),
    );
    assert!(msg.contains("revoked"), "{msg}");
}

// --- Go: TestScopesAndTenantClaims ---------------------------------------

#[test]
fn scopes_and_tenant_claims() {
    let (sk, key) = keypair(10);
    let base = with(valid(), &[("scopes", Value::Null), ("tenant", Value::Null)]);
    let claims = |s: &[&str], tenant: &str| Claims {
        subject: "alice".into(),
        tenant: tenant.into(),
        scopes: s.iter().map(|s| s.to_string()).collect(),
    };
    let cases: Vec<(&str, Value, Option<&str>, Claims)> = vec![
        (
            "scope string",
            with(base.clone(), &[("scope", json!("a b"))]),
            None,
            claims(&["a", "b"], ""),
        ),
        (
            "scp string",
            with(base.clone(), &[("scp", json!("a b"))]),
            None,
            claims(&["a", "b"], ""),
        ),
        (
            "scp list",
            with(base.clone(), &[("scp", json!(["a"]))]),
            None,
            claims(&["a"], ""),
        ),
        (
            "scopes wins",
            with(
                base.clone(),
                &[("scopes", json!(["x"])), ("scope", json!("y"))],
            ),
            None,
            claims(&["x"], ""),
        ),
        (
            "tenant claim",
            with(base.clone(), &[("tid", json!("t-9"))]),
            Some("tid"),
            claims(&[], "t-9"),
        ),
        (
            "numeric tenant",
            with(base.clone(), &[("tenant", json!(42))]),
            None,
            claims(&[], "42"),
        ),
    ];
    for (name, c, tenant_claim, want) in cases {
        let v = verifier(
            key.clone(),
            Options {
                tenant_claim: tenant_claim.map(String::from),
                ..opts()
            },
        );
        let tok = v.verify(&sign(&sk, &key.id, &c)).unwrap();
        assert_eq!(tok.claims(), want, "{name}");
    }
}

// --- Go: TestOAuthScopeReachesScopesOf / AuthFunc ------------------------

#[test]
fn oauth_scope_reaches_the_auth_func() {
    let (sk, key) = keypair(11);
    let v = verifier(key.clone(), opts());
    let raw = sign(
        &sk,
        &key.id,
        &with(
            valid(),
            &[
                ("scopes", Value::Null),
                ("scope", json!("items:export other")),
            ],
        ),
    );
    let header = format!("Bearer {raw}");
    let claims = v.auth_func()(&Credentials {
        authorization: Some(&header),
        api_key: None,
    })
    .unwrap();
    assert_eq!(claims.scopes, ["items:export", "other"]);
    assert_eq!(claims.principal(), "alice");
}

#[test]
fn auth_func_refusals_match_the_middleware_table() {
    let (sk, key) = keypair(12);
    let v = verifier(key.clone(), opts());
    let raw = sign(&sk, &key.id, &valid());
    let f = v.auth_func();
    let call = |h: Option<&str>| {
        f(&Credentials {
            authorization: h,
            api_key: None,
        })
    };

    assert_eq!(
        call(None).unwrap_err().to_string(),
        "authn: no bearer token"
    );
    let basic = call(Some("Basic abc")).unwrap_err();
    assert_eq!(basic.kind(), Kind::NoToken);
    assert!(basic.to_string().contains("not a bearer token"));
    let garbage = call(Some("Bearer garbage")).unwrap_err();
    assert!(garbage.to_string().contains("invalid token"));
    let ok = call(Some(&format!("bearer {raw}"))).unwrap();
    assert_eq!((ok.subject.as_str(), ok.tenant.as_str()), ("alice", "acme"));
}

#[test]
fn bearer_token_extraction() {
    assert_eq!(bearer_token(Some("Bearer abc")).unwrap(), "abc");
    assert_eq!(bearer_token(Some("  bEaReR   abc  ")).unwrap(), "abc");
    for bad in [None, Some(""), Some("   ")] {
        assert_eq!(bearer_token(bad).unwrap_err(), AuthnError::NoToken(None));
    }
    for bad in [
        "Bearer",
        "Bearer  ",
        "Basic abc",
        "Bearerabc",
        "Bearer\tabc",
    ] {
        let e = bearer_token(Some(bad)).unwrap_err();
        assert_eq!(e.kind(), Kind::NoToken, "{bad:?}");
    }
}

// --- Go: TestNewJWTRefusesNoKeysAndUnsupportedKeys -----------------------

#[test]
fn new_refuses_no_keys_and_bad_keys() {
    assert_eq!(
        Verifier::new(vec![], opts()).unwrap_err().kind(),
        Kind::Config
    );
    // Not on the curve, wrong size, small order: refused at Key::new.
    assert!(Key::new("x", VerifyKey::P256(vec![4; 65])).is_err());
    assert!(Key::new("x", VerifyKey::P256(vec![1, 2, 3])).is_err());
    assert!(Key::new("x", VerifyKey::Ed25519([0; 32])).is_err());
    assert!(Key::new(
        "x",
        VerifyKey::Rsa {
            n: vec![],
            e: vec![1]
        }
    )
    .is_err());
}

// --- key and claim vocabulary --------------------------------------------

#[test]
fn alg_names_round_trip_and_exclude_unsafe() {
    for a in [
        Alg::EdDsa,
        Alg::Es256,
        Alg::Es384,
        Alg::Es512,
        Alg::Rs256,
        Alg::Rs384,
        Alg::Rs512,
        Alg::Ps256,
        Alg::Ps384,
        Alg::Ps512,
    ] {
        assert_eq!(Alg::parse(a.as_str()), Some(a));
    }
    for bad in ["none", "HS256", "HS384", "HS512", "", "es256", "ED25519"] {
        assert_eq!(Alg::parse(bad), None, "{bad}");
    }
}

#[test]
fn scopes_of_reads_the_first_entry_present() {
    assert_eq!(scopes_of(&json!({"scopes": ["a", 1, "b"]})), ["a", "b"]);
    assert_eq!(scopes_of(&json!({"scopes": "one two"})), ["one two"]);
    assert_eq!(scopes_of(&json!({"scope": "a  b"})), ["a", "b"]);
    assert_eq!(scopes_of(&json!({"scp": "a b"})), ["a", "b"]);
    assert_eq!(scopes_of(&json!({"scp": ["a"]})), ["a"]);
    // `scopes` wins even when unusable.
    assert!(scopes_of(&json!({"scopes": 7, "scope": "x"})).is_empty());
    assert!(scopes_of(&json!({"scope": ""})).is_empty());
    assert!(scopes_of(&json!(null)).is_empty());
}

#[test]
fn identity_of_reads_sub_and_tenant() {
    assert_eq!(
        identity_of(&json!({"sub": "u", "tenant": "t"})),
        ("u".into(), "t".into())
    );
    assert_eq!(identity_of(&json!({"sub": 1})), ("".into(), "".into()));
    assert_eq!(identity_of(&json!(null)), ("".into(), "".into()));
}

#[test]
fn jwks_skips_unusable_keys_and_needs_one() {
    let (_, key) = keypair(13);
    let VerifyKey::Ed25519(x) = key.public.clone() else {
        unreachable!()
    };
    let doc = json!({"keys": [
        {"kty": "RSA", "use": "enc", "kid": "e", "n": "AQAB", "e": "AQAB"},
        {"kty": "oct", "kid": "s", "k": "AAAA"},
        {"kty": "EC", "crv": "secp256k1", "x": "AA", "y": "AA"},
        {"kty": "OKP", "crv": "Ed25519", "kid": "ok", "x": b64(&x), "use": "sig"},
    ]});
    let keys = keys_from_jwks(doc.to_string().as_bytes()).unwrap();
    assert_eq!(keys.len(), 1);
    assert_eq!(keys[0].id, "ok");

    let none = json!({"keys": [{"kty": "oct", "k": "AAAA"}]});
    let err = keys_from_jwks(none.to_string().as_bytes()).unwrap_err();
    assert!(err.to_string().contains("no signing key"));
    assert!(keys_from_jwks(b"{").is_err());
    assert!(keys_from_jwks(&vec![b' '; (1 << 20) + 1]).is_err());
}

#[test]
fn jwk_alg_pins_the_key_to_one_algorithm() {
    let (sk, key) = keypair(14);
    let VerifyKey::Ed25519(x) = key.public.clone() else {
        unreachable!()
    };
    let jwk: Jwk = serde_json::from_value(
        json!({"kty":"OKP","crv":"Ed25519","kid":"p","x":b64(&x),"alg":"ES256"}),
    )
    .unwrap();
    let pinned = jwk.to_key().unwrap();
    assert_eq!(pinned.algorithm, Some(Alg::Es256));
    // The token is EdDSA, the key says ES256: it is skipped.
    let v = verifier(pinned, opts());
    assert!(refused(&v, &sign(&sk, "p", &valid())).contains("signature"));
}

#[test]
fn malformed_tokens_are_refused_not_panicked_on() {
    let (_, key) = keypair(15);
    let v = verifier(key, opts());
    for raw in [
        ".",
        "..",
        "a.b.c",
        "e30.e30.AAAA",
        "e30..",
        "!!.!!.!!",
        &format!("{}.{}.{}", b64(b"[]"), b64(b"{}"), b64(b"x")),
        &format!("{}.{}.{}", b64(br#"{"alg":7}"#), b64(b"{}"), b64(b"x")),
        &format!(
            "{}.{}.{}",
            b64(br#"{"alg":"EdDSA","kid":7}"#),
            b64(b"{}"),
            b64(b"x")
        ),
        "\u{0}\u{1}.é.ü",
    ] {
        let e = v.verify(raw).unwrap_err();
        assert_eq!(e.kind(), Kind::InvalidToken, "{raw:?}");
    }
}

#[test]
fn claim_type_confusion_is_refused() {
    let (sk, key) = keypair(16);
    let v = verifier(key.clone(), opts());
    for bad in [
        with(valid(), &[("sub", json!(5))]),
        with(valid(), &[("exp", json!("soon"))]),
        with(valid(), &[("aud", json!([1]))]),
        with(valid(), &[("iss", json!({}))]),
        with(valid(), &[("exp", json!(1e300))]),
    ] {
        let msg = refused(&v, &sign(&sk, &key.id, &bad));
        assert!(msg.contains("malformed claims"), "{msg}");
    }
}

// --- shared corpus -------------------------------------------------------

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Corpus {
    version: String,
    now: u64,
    tokens: Vec<TokenVector>,
    pem: Vec<PemVector>,
    api_keys: Vec<Value>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TokenVector {
    name: String,
    alg: String,
    jwk: Jwk,
    token: String,
    #[serde(default)]
    options: VectorOptions,
    want: Option<VectorClaims>,
    #[serde(default)]
    refusal: String,
    #[serde(default)]
    key_id: String,
}

#[derive(Deserialize, Default)]
#[serde(deny_unknown_fields)]
struct VectorOptions {
    #[serde(default)]
    issuer: String,
    #[serde(default)]
    audience: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct VectorClaims {
    sub: String,
    #[serde(default)]
    tenant: String,
    #[serde(default)]
    scopes: Vec<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PemVector {
    name: String,
    pem: String,
    kid: String,
}

pub fn corpus() -> Value {
    let path =
        PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/authn-v1/vectors.json");
    serde_json::from_slice(&std::fs::read(path).expect("read corpus")).expect("corpus is JSON")
}

fn rsa_algs(a: &str) -> bool {
    a.starts_with("RS") || a.starts_with("PS")
}

#[test]
fn shared_corpus_tokens() {
    let c: Corpus = serde_json::from_value(corpus()).expect("corpus shape");
    assert_eq!(c.version, "v1");
    let mut verified = 0;
    for v in &c.tokens {
        if rsa_algs(&v.alg) && cfg!(not(feature = "authn-rsa")) {
            continue; // RSA verifies only with `authn-rsa`.
        }
        let key = v.jwk.to_key().unwrap_or_else(|e| panic!("{}: {e}", v.name));
        let ver = Verifier::new(
            vec![key],
            Options {
                issuer: (!v.options.issuer.is_empty()).then(|| v.options.issuer.clone()),
                audience: v.options.audience.clone(),
                now: Some(at(c.now)),
                ..Options::default()
            },
        )
        .unwrap();
        let res = ver.verify(&v.token);
        if let Some(want) = &v.want {
            let tok = res.unwrap_or_else(|e| panic!("{}: {e}", v.name));
            assert_eq!(tok.subject, want.sub, "{}", v.name);
            assert_eq!(tok.tenant, want.tenant, "{}", v.name);
            assert_eq!(tok.scopes, want.scopes, "{}", v.name);
            assert_eq!(tok.key_id, v.key_id, "{}", v.name);
            verified += 1;
        } else {
            let msg = res.expect_err(&v.name).to_string();
            assert!(msg.contains(&v.refusal), "{}: {msg}", v.name);
            assert!(
                msg.starts_with("authn: invalid token: "),
                "{}: {msg}",
                v.name
            );
        }
    }
    assert!(verified >= 5, "the corpus must exercise the accepted algs");
}

#[test]
fn shared_corpus_pem_key_ids() {
    let c: Corpus = serde_json::from_value(corpus()).unwrap();
    assert!(!c.pem.is_empty());
    for v in &c.pem {
        let k = Key::from_public_key_pem(v.pem.as_bytes())
            .unwrap_or_else(|e| panic!("{}: {e}", v.name));
        assert_eq!(k.id, v.kid, "{}", v.name);
    }
    assert!(Key::from_public_key_pem(b"nope").is_err());
}

#[test]
fn shared_corpus_thumbprints_match_the_jwk() {
    // The PEM kid for EC/RSA is the RFC 7638 thumbprint; the JWK route
    // must land on the same bytes.
    let c: Corpus = serde_json::from_value(corpus()).unwrap();
    for v in c.tokens.iter().filter(|v| v.want.is_some()) {
        if rsa_algs(&v.alg) && cfg!(not(feature = "authn-rsa")) {
            continue;
        }
        let key = v.jwk.to_key().unwrap();
        if matches!(key.public, VerifyKey::Ed25519(_)) {
            continue;
        }
        let by_pem = c.pem.iter().any(|p| p.kid == key.public.thumbprint());
        assert!(by_pem, "{}: thumbprint {}", v.name, key.public.thumbprint());
    }
}

#[test]
fn corpus_api_keys_are_listed_for_the_store_tests() {
    let c: Corpus = serde_json::from_value(corpus()).unwrap();
    assert_eq!(c.now, NOW);
    assert!(!c.api_keys.is_empty());
}
