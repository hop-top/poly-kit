//! Runs the shared identity corpus in `contracts/identity-v1`. The
//! vectors are the source of truth: a failure means this port drifted
//! from the contract (Go is canonical), not that a vector should be
//! edited to fit.
//!
//! Strict: unknown fields fail, `version` must be 1, and the corpus
//! directory must match [`FILES`] exactly.
#![cfg(feature = "identity")]

use std::fs;
use std::path::PathBuf;

use hop_top_kit::identity::{derive_key, Keypair};
use serde::Deserialize;

const FILES: &[&str] = &["derive-key.json"];

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Algorithm {
    kdf: String,
    ikm: String,
    salt: Option<String>,
    info: String,
    output_len: usize,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Case {
    name: String,
    seed: String,
    public_key: String,
    derived_key: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Fixture {
    version: u32,
    #[allow(dead_code)]
    note: String,
    algorithm: Algorithm,
    derive_key: Vec<Case>,
}

fn dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/identity-v1")
}

fn unhex(s: &str) -> Vec<u8> {
    assert_eq!(s.len() % 2, 0, "odd hex length");
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("hex"))
        .collect()
}

fn hex(b: &[u8]) -> String {
    b.iter().map(|x| format!("{x:02x}")).collect()
}

#[test]
fn corpus_files_match() {
    let mut got: Vec<String> = fs::read_dir(dir())
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    got.sort();
    assert_eq!(got, FILES);
}

#[test]
fn derive_key_vectors() {
    let data = fs::read_to_string(dir().join("derive-key.json")).unwrap();
    let f: Fixture = serde_json::from_str(&data).unwrap();
    assert_eq!(f.version, 1);
    assert_eq!(f.algorithm.kdf, "HKDF-SHA256");
    assert!(f.algorithm.ikm.contains("seed"));
    assert!(f.algorithm.salt.is_none());
    assert_eq!(
        f.algorithm.info.as_bytes(),
        hop_top_kit::identity::KEY_DERIVATION_INFO
    );
    assert_eq!(f.algorithm.output_len, 32);
    assert!(!f.derive_key.is_empty());

    for c in &f.derive_key {
        let seed: [u8; 32] = unhex(&c.seed).try_into().expect("32-byte seed");
        let kp = Keypair::from_seed(&seed);
        assert_eq!(
            hex(&kp.public_key()),
            c.public_key,
            "{}: public key",
            c.name
        );
        let key = kp.derive_key();
        assert_eq!(hex(&key), c.derived_key, "{}: derived key", c.name);
        assert_eq!(derive_key(&seed), key, "{}: free fn", c.name);
    }
}
