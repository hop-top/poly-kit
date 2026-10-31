//! wasm32-unknown-unknown smoke test for `authn`, run under Node by
//! wasm-bindgen-test-runner:
//!
//! ```sh
//! CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
//!   cargo test --target wasm32-unknown-unknown --no-default-features \
//!   --features authn --test authn_wasm
//! ```
//!
//! Verifies the shared corpus's EdDSA and ECDSA tokens with the clock
//! injected, and that a verifier without one refuses rather than
//! reading a clock wasm32 does not have.

#![cfg(all(feature = "authn", target_arch = "wasm32", target_os = "unknown"))]

use std::sync::Arc;
use std::time::{Duration, UNIX_EPOCH};

use hop_top_kit::authn::{Jwk, Options, Verifier};
use serde_json::Value;
use wasm_bindgen_test::wasm_bindgen_test;

const CORPUS: &str = include_str!("../../../contracts/authn-v1/vectors.json");

fn token(name: &str) -> (Verifier, String, Verifier) {
    let c: Value = serde_json::from_str(CORPUS).unwrap();
    let v = c["tokens"]
        .as_array()
        .unwrap()
        .iter()
        .find(|t| t["name"] == name)
        .unwrap();
    let jwk: Jwk = serde_json::from_value(v["jwk"].clone()).unwrap();
    let now = c["now"].as_u64().unwrap();
    let opts = Options {
        now: Some(Arc::new(move || UNIX_EPOCH + Duration::from_secs(now))),
        ..Options::default()
    };
    (
        Verifier::new(vec![jwk.to_key().unwrap()], opts).unwrap(),
        v["token"].as_str().unwrap().to_string(),
        Verifier::new(vec![jwk.to_key().unwrap()], Options::default()).unwrap(),
    )
}

#[wasm_bindgen_test]
fn verifies_eddsa_and_ecdsa_tokens() {
    for name in ["eddsa", "es256", "es384", "es512"] {
        let (v, raw, _) = token(name);
        assert_eq!(v.verify(&raw).unwrap().subject, "alice", "{name}");
    }
}

#[wasm_bindgen_test]
fn no_clock_refuses() {
    let (_, raw, clockless) = token("eddsa");
    let e = clockless.verify(&raw).unwrap_err().to_string();
    assert!(e.contains("no clock"), "{e}");
}
