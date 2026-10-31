//! Ports of the Go `core/identity` tests. Keys come from fixed seeds or
//! the OS RNG; no key material is written as a literal.
#![cfg(feature = "identity")]

use hop_top_kit::identity::*;

const NOW: i64 = 1_700_000_000;

fn kp(n: u8) -> Keypair {
    Keypair::from_seed(&[n; 32])
}

fn claims(sub: &str) -> Claims {
    Claims {
        subject: sub.into(),
        issued_at: NOW,
        ..Claims::default()
    }
}

fn err_has<T: std::fmt::Debug>(r: Result<T, IdentityError>, want: &str) {
    let e = r.expect_err("expected error").to_string();
    assert!(e.contains(want), "{e:?} lacks {want:?}");
}

fn b64(data: &[u8]) -> String {
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine;
    URL_SAFE_NO_PAD.encode(data)
}

/// Signs `payload` verbatim, like a foreign issuer would.
fn raw_token(k: &Keypair, payload: &str) -> String {
    let header = format!(
        r#"{{"alg":"EdDSA","kid":"{}","typ":"JWT"}}"#,
        k.public_key_id()
    );
    let input = format!("{}.{}", b64(header.as_bytes()), b64(payload.as_bytes()));
    let sig = k.sign(input.as_bytes());
    format!("{input}.{}", b64(&sig))
}

// keypair

#[test]
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn generate_makes_distinct_keys() {
    let a = Keypair::generate().unwrap();
    let b = Keypair::generate().unwrap();
    assert_eq!(a.public_key().len(), 32);
    assert_ne!(a.public_key(), b.public_key());
}

#[test]
fn public_key_id_is_deterministic_16_hex() {
    let k = kp(1);
    let id = k.public_key_id();
    assert_eq!(id, k.public_key_id());
    assert_eq!(id.len(), 16);
    assert!(id
        .bytes()
        .all(|c| c.is_ascii_hexdigit() && !c.is_ascii_uppercase()));
    assert_ne!(id, kp(2).public_key_id());
}

#[test]
fn public_key_id_is_sha256_prefix_of_the_key() {
    // sha256 of the all-zero seed's public key, first 8 bytes.
    use sha2::{Digest, Sha256};
    let k = kp(0);
    let h = Sha256::digest(k.public_key());
    let want: String = h[..8].iter().map(|b| format!("{b:02x}")).collect();
    assert_eq!(k.public_key_id(), want);
}

#[test]
fn marshal_parse_public_key_roundtrip() {
    let k = kp(3);
    let pem = k.marshal_public_key().unwrap();
    assert!(pem.starts_with("-----BEGIN PUBLIC KEY-----\n"));
    assert!(pem.ends_with("-----END PUBLIC KEY-----\n"));
    assert_eq!(parse_public_key(pem.as_bytes()).unwrap(), k.public_key());
}

#[test]
fn marshal_parse_private_key_roundtrip() {
    let k = kp(4);
    let pem = k.marshal_private_key().unwrap();
    assert!(pem.starts_with("-----BEGIN PRIVATE KEY-----\n"));
    let loaded = parse_private_key(pem.as_bytes()).unwrap();
    assert_eq!(loaded.public_key(), k.public_key());
    assert_eq!(loaded.seed(), k.seed());
}

#[test]
fn pem_matches_the_pkcs8_and_spki_layout_go_writes() {
    // Go's x509 emits a fixed 16-byte PKCS#8 prefix before the seed and a
    // 12-byte SPKI prefix before the key.
    let k = kp(5);
    let der = |pem: &str| -> Vec<u8> {
        use base64::engine::general_purpose::STANDARD;
        use base64::Engine;
        let body: String = pem.lines().filter(|l| !l.starts_with("-----")).collect();
        STANDARD.decode(body).unwrap()
    };
    let mut priv_der = vec![
        0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04,
        0x20,
    ];
    priv_der.extend_from_slice(&k.seed());
    assert_eq!(der(&k.marshal_private_key().unwrap()), priv_der);
    let mut pub_der = vec![
        0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00,
    ];
    pub_der.extend_from_slice(&k.public_key());
    assert_eq!(der(&k.marshal_public_key().unwrap()), pub_der);
}

#[test]
fn parse_rejects_non_pem() {
    assert!(parse_public_key(b"not pem").is_err());
    assert!(parse_private_key(b"not pem").is_err());
    err_has(parse_public_key(b"not pem"), "no PEM block found");
}

#[test]
fn parse_rejects_non_ed25519_keys() {
    // P-256 SPKI header with a dummy point: valid DER, wrong algorithm.
    let mut der = vec![
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01, 0x06, 0x08,
        0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00, 0x04,
    ];
    der.extend_from_slice(&[7u8; 64]);
    use base64::engine::general_purpose::STANDARD;
    use base64::Engine;
    let pem = format!(
        "-----BEGIN PUBLIC KEY-----\n{}\n-----END PUBLIC KEY-----\n",
        STANDARD.encode(der)
    );
    assert!(parse_public_key(pem.as_bytes()).is_err());
}

// jwt

#[test]
fn sign_verify_roundtrip() {
    let k = kp(1);
    let c = Claims {
        subject: "user-123".into(),
        issuer: "kit".into(),
        scopes: vec!["read".into(), "write".into()],
        issued_at: NOW,
        ..Claims::default()
    };
    let token = k.sign_jwt(c.clone());
    let got = verify_jwt_at(&token, &k.public_key(), NOW).unwrap();
    assert_eq!(got.subject, c.subject);
    assert_eq!(got.issuer, c.issuer);
    assert_eq!(got.scopes, c.scopes);
    assert_eq!(got.issued_at, c.issued_at);
}

#[test]
fn verify_expired() {
    let k = kp(1);
    let token = k.sign_jwt(Claims {
        issued_at: NOW - 7200,
        expires_at: NOW - 3600,
        ..claims("user-123")
    });
    err_has(verify_jwt_at(&token, &k.public_key(), NOW), "expired");
}

#[test]
fn expiry_boundary_matches_go() {
    // Go: expired only when now > exp.
    let k = kp(1);
    let token = k.sign_jwt(Claims {
        expires_at: NOW,
        ..claims("u")
    });
    assert!(verify_jwt_at(&token, &k.public_key(), NOW).is_ok());
    assert!(verify_jwt_at(&token, &k.public_key(), NOW + 1).is_err());
    // exp 0 / negative: never checked.
    let no_exp = k.sign_jwt(claims("u"));
    assert!(verify_jwt_at(&no_exp, &k.public_key(), i64::MAX).is_ok());
    let neg = raw_token(&k, r#"{"sub":"u","iat":1,"exp":-5}"#);
    assert!(verify_jwt_at(&neg, &k.public_key(), NOW).is_ok());
}

#[test]
fn verify_invalid_signature() {
    let (k1, k2) = (kp(1), kp(2));
    let token = k1.sign_jwt(claims("user-123"));
    err_has(
        verify_jwt_at(&token, &k2.public_key(), NOW),
        "invalid JWT signature",
    );
}

#[test]
fn verify_malformed() {
    let k = kp(1);
    assert!(verify_jwt_at("not.a.valid.token", &k.public_key(), NOW).is_err());
    err_has(
        verify_jwt_at("onlyonepart", &k.public_key(), NOW),
        "malformed",
    );
    err_has(verify_jwt_at("", &k.public_key(), NOW), "malformed");
    err_has(verify_jwt_at("a.b", &k.public_key(), NOW), "malformed");
}

#[test]
fn signature_checked_before_payload() {
    // Garbage payload under a bad signature reports the signature, as Go.
    let k = kp(1);
    let token = format!("{}.{}.{}", b64(b"{}"), b64(b"not json"), b64(&[0u8; 64]));
    err_has(
        verify_jwt_at(&token, &k.public_key(), NOW),
        "invalid JWT signature",
    );
}

#[test]
fn payload_errors_after_valid_signature() {
    let k = kp(1);
    err_has(
        verify_jwt_at(&raw_token(&k, "not json"), &k.public_key(), NOW),
        "unmarshal claims",
    );
    err_has(
        verify_jwt_at(&raw_token(&k, r#"{"sub":5}"#), &k.public_key(), NOW),
        "unmarshal claims",
    );
    err_has(
        verify_jwt_at(&raw_token(&k, r#"{"aud":5}"#), &k.public_key(), NOW),
        "aud must be string or []string",
    );
}

#[test]
fn undecodable_parts() {
    let k = kp(1);
    err_has(
        verify_jwt_at("a.b.@@@", &k.public_key(), NOW),
        "decode signature",
    );
    // Valid signature over a payload that is not base64.
    let input = format!("{}.@@@", b64(b"{}"));
    let token = format!("{input}.{}", b64(&k.sign(input.as_bytes())));
    err_has(
        verify_jwt_at(&token, &k.public_key(), NOW),
        "decode payload",
    );
}

#[test]
fn header_is_sorted_alg_kid_typ() {
    let k = kp(1);
    let token = k.sign_jwt(claims("u"));
    let header = token.split('.').next().unwrap();
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine;
    let raw = String::from_utf8(URL_SAFE_NO_PAD.decode(header).unwrap()).unwrap();
    assert_eq!(
        raw,
        format!(
            r#"{{"alg":"EdDSA","kid":"{}","typ":"JWT"}}"#,
            k.public_key_id()
        )
    );
}

#[test]
fn kid_is_stamped_into_claims() {
    let k = kp(1);
    let token = k.sign_jwt(Claims {
        key_id: "caller-kid".into(),
        ..claims("u")
    });
    let got = verify_jwt_at(&token, &k.public_key(), NOW).unwrap();
    assert_eq!(got.key_id, k.public_key_id());
}

#[test]
fn verify_options_issuer_audience() {
    let k = kp(1);
    let token = k.sign_jwt(Claims {
        issuer: "auth-svc".into(),
        audience: Audience(vec!["api-gateway".into()]),
        ..claims("user-1")
    });
    let pk = k.public_key();
    let opts = |i: &str, a: &str| VerifyOptions {
        issuer: i.into(),
        audience: a.into(),
    };
    err_has(
        verify_jwt_with_options_at(&token, &pk, Some(&opts("other-svc", "")), NOW),
        "issuer",
    );
    err_has(
        verify_jwt_with_options_at(&token, &pk, Some(&opts("", "web-app")), NOW),
        "audience",
    );
    // Valid issuer, bad audience, and the reverse.
    err_has(
        verify_jwt_with_options_at(&token, &pk, Some(&opts("auth-svc", "wrong")), NOW),
        "audience",
    );
    err_has(
        verify_jwt_with_options_at(&token, &pk, Some(&opts("wrong", "api-gateway")), NOW),
        "issuer",
    );
    assert!(
        verify_jwt_with_options_at(&token, &pk, Some(&opts("auth-svc", "api-gateway")), NOW)
            .is_ok()
    );
    // None and empty options accept anything signed.
    let plain = verify_jwt_at(&token, &pk, NOW).unwrap();
    assert_eq!(
        verify_jwt_with_options_at(&token, &pk, None, NOW).unwrap(),
        plain
    );
    assert_eq!(
        verify_jwt_with_options_at(&token, &pk, Some(&VerifyOptions::default()), NOW).unwrap(),
        plain
    );
}

#[test]
fn verify_options_messages_match_go() {
    let k = kp(1);
    let token = k.sign_jwt(Claims {
        issuer: "a".into(),
        audience: Audience(vec!["x".into(), "y".into()]),
        ..claims("u")
    });
    let pk = k.public_key();
    let e = verify_jwt_with_options_at(
        &token,
        &pk,
        Some(&VerifyOptions {
            issuer: "b".into(),
            audience: String::new(),
        }),
        NOW,
    )
    .unwrap_err()
    .to_string();
    assert_eq!(e, r#"identity: issuer mismatch: got "a", want "b""#);
    let e = verify_jwt_with_options_at(
        &token,
        &pk,
        Some(&VerifyOptions {
            issuer: String::new(),
            audience: "z".into(),
        }),
        NOW,
    )
    .unwrap_err()
    .to_string();
    assert_eq!(e, r#"identity: audience mismatch: got [x y], want "z""#);
}

#[test]
fn empty_claim_vs_expected() {
    let k = kp(1);
    let token = k.sign_jwt(claims("user-1"));
    let pk = k.public_key();
    err_has(
        verify_jwt_with_options_at(
            &token,
            &pk,
            Some(&VerifyOptions {
                issuer: "auth-svc".into(),
                audience: String::new(),
            }),
            NOW,
        ),
        "issuer",
    );
    err_has(
        verify_jwt_with_options_at(
            &token,
            &pk,
            Some(&VerifyOptions {
                issuer: String::new(),
                audience: "required-aud".into(),
            }),
            NOW,
        ),
        "audience",
    );
}

#[test]
fn audience_array_roundtrip_and_single_string_form() {
    let k = kp(1);
    let token = k.sign_jwt(Claims {
        audience: Audience(vec!["api-gateway".into(), "web-app".into()]),
        ..claims("user-1")
    });
    let got = verify_jwt_at(&token, &k.public_key(), NOW).unwrap();
    assert_eq!(got.audience.0, ["api-gateway", "web-app"]);
    let payload = payload_of(&token);
    assert!(
        payload.contains(r#""aud":["api-gateway","web-app"]"#),
        "{payload}"
    );

    let one = k.sign_jwt(Claims {
        audience: Audience(vec!["only".into()]),
        ..claims("u")
    });
    assert!(payload_of(&one).contains(r#""aud":"only""#));
    assert_eq!(
        verify_jwt_at(&one, &k.public_key(), NOW)
            .unwrap()
            .audience
            .0,
        ["only"]
    );
}

fn payload_of(token: &str) -> String {
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine;
    let p = token.split('.').nth(1).unwrap();
    String::from_utf8(URL_SAFE_NO_PAD.decode(p).unwrap()).unwrap()
}

#[test]
fn foreign_audience_forms() {
    let k = kp(1);
    let pk = k.public_key();
    let opts = |a: &str| VerifyOptions {
        issuer: String::new(),
        audience: a.into(),
    };

    let arr = raw_token(
        &k,
        &format!(r#"{{"sub":"ext-user","aud":["service-a","service-b"],"iat":{NOW}}}"#),
    );
    for a in ["service-a", "service-b"] {
        let got = verify_jwt_with_options_at(&arr, &pk, Some(&opts(a)), NOW).unwrap();
        assert_eq!(got.subject, "ext-user");
    }
    err_has(
        verify_jwt_with_options_at(&arr, &pk, Some(&opts("service-c")), NOW),
        "audience",
    );

    let single = raw_token(&k, r#"{"sub":"u1","aud":"single","iat":1}"#);
    let got = verify_jwt_with_options_at(&single, &pk, Some(&opts("single")), NOW).unwrap();
    assert_eq!(got.audience.0, ["single"]);
    err_has(
        verify_jwt_with_options_at(&single, &pk, Some(&opts("other")), NOW),
        "audience",
    );

    // Go quirk kept for parity: a JSON null aud decodes as [""].
    let null = raw_token(&k, r#"{"sub":"u","aud":null}"#);
    assert_eq!(verify_jwt_at(&null, &pk, NOW).unwrap().audience.0, [""]);
}

#[test]
fn claims_preserved_and_all_fields_roundtrip() {
    let k = kp(7);
    let c = Claims {
        subject: "svc-456".into(),
        issuer: "kit-auth".into(),
        key_id: String::new(),
        audience: Audience(vec!["a".into(), "b".into()]),
        tenant: "acme".into(),
        scopes: vec!["admin".into()],
        scope: "admin read".into(),
        issued_at: NOW,
        expires_at: 9_999_999_999,
        nonce: "n-1".into(),
    };
    let got = verify_jwt_at(&k.sign_jwt(c.clone()), &k.public_key(), NOW).unwrap();
    let mut want = c;
    want.key_id = k.public_key_id();
    assert_eq!(got, want);
}

#[test]
fn claims_json_field_order_and_omitempty() {
    let k = kp(1);
    let min = payload_of(&k.sign_jwt(Claims::default()));
    assert_eq!(
        min,
        format!(r#"{{"sub":"","kid":"{}","iat":0}}"#, k.public_key_id())
    );
    let full = payload_of(&k.sign_jwt(Claims {
        subject: "s".into(),
        issuer: "i".into(),
        audience: Audience(vec!["a".into()]),
        tenant: "t".into(),
        scopes: vec!["x".into()],
        scope: "x".into(),
        issued_at: 1,
        expires_at: 2,
        nonce: "n".into(),
        ..Claims::default()
    }));
    assert_eq!(
        full,
        format!(
            r#"{{"sub":"s","iss":"i","kid":"{}","aud":"a","tenant":"t","scopes":["x"],"scope":"x","iat":1,"exp":2,"nonce":"n"}}"#,
            k.public_key_id()
        )
    );
}

#[test]
fn claims_keys_match_case_insensitively_like_go() {
    let k = kp(1);
    let t = raw_token(&k, r#"{"SUB":"u","Iss":"i","IAT":9,"unknown":{"x":1}}"#);
    let got = verify_jwt_at(&t, &k.public_key(), NOW).unwrap();
    assert_eq!(
        (got.subject.as_str(), got.issuer.as_str(), got.issued_at),
        ("u", "i", 9)
    );
}

#[test]
fn adversarial_subject_survives_json() {
    let k = kp(1);
    for sub in [
        r#""injected":"true""#,
        "key\\with\\\\backslash",
        "line\nbreak",
        "normal\"quote",
        "\u{2028}é<>&",
    ] {
        let got = verify_jwt_at(&k.sign_jwt(claims(sub)), &k.public_key(), NOW).unwrap();
        assert_eq!(got.subject, sub);
    }
}

// encryption

#[test]
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn encrypt_decrypt_roundtrip_and_fresh_nonce() {
    let key = kp(1).derive_key();
    let ct = encrypt(&key, b"hello, world!").unwrap();
    assert_eq!(decrypt(&key, &ct).unwrap(), b"hello, world!");
    assert_ne!(ct, encrypt(&key, b"hello, world!").unwrap());
    assert_eq!(ct.len(), 24 + 13 + 16);
}

#[test]
fn decrypt_wrong_key() {
    let ct = encrypt_with_nonce(&kp(1).derive_key(), &[9u8; 24], b"secret");
    err_has(decrypt(&kp(2).derive_key(), &ct), "decryption failed");
}

#[test]
fn decrypt_short_ciphertext() {
    err_has(decrypt(&[0u8; 32], b"short"), "too short");
    // Exactly nonce + tag is the shortest valid frame (empty plaintext).
    err_has(decrypt(&[0u8; 32], &[0u8; 39]), "too short");
    err_has(decrypt(&[0u8; 32], &[0u8; 40]), "decryption failed");
}

#[test]
fn empty_plaintext_roundtrip() {
    let key = kp(1).derive_key();
    let ct = encrypt_with_nonce(&key, &[1u8; 24], b"");
    assert_eq!(ct.len(), 40);
    assert!(decrypt(&key, &ct).unwrap().is_empty());
}

#[test]
fn tampered_ciphertext_fails() {
    let key = kp(1).derive_key();
    let mut ct = encrypt_with_nonce(&key, &[1u8; 24], b"payload");
    let last = ct.len() - 1;
    ct[last] ^= 1;
    err_has(decrypt(&key, &ct), "decryption failed");
}

#[test]
fn secretbox_layout_is_nonce_then_tag_then_body() {
    // Frame = nonce || secretbox(tag || body); a fixed nonce makes it
    // deterministic, pinning the wire layout other ports read.
    let key = [5u8; 32];
    let a = encrypt_with_nonce(&key, &[2u8; 24], b"abc");
    let b = encrypt_with_nonce(&key, &[2u8; 24], b"abc");
    assert_eq!(a, b);
    assert_eq!(&a[..24], &[2u8; 24]);
    assert_eq!(a.len(), 24 + 16 + 3);
}

#[test]
fn derive_key_deterministic_and_separated() {
    let k = kp(1);
    assert_eq!(k.derive_key(), k.derive_key());
    assert_ne!(k.derive_key(), kp(2).derive_key());
    assert_eq!(k.derive_key(), derive_key(&k.seed()));
}

#[test]
fn debug_does_not_leak_the_seed() {
    let k = kp(0xAB);
    let s = format!("{k:?}");
    assert!(
        !s.contains("171") && !s.to_lowercase().contains("abab"),
        "{s}"
    );
}

#[cfg(feature = "sqlstore-encrypt")]
#[test]
fn derive_key_agrees_with_sqlstore() {
    let seed = [0x5a; 32];
    assert_eq!(
        derive_key(&seed),
        hop_top_kit::sqlstore::crypto::derive_key(&seed)
    );
}
