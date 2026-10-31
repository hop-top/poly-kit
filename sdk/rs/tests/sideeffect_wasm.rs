//! wasm32-unknown-unknown smoke test for the seams, run under Node by
//! wasm-bindgen-test-runner:
//!
//! ```sh
//! CARGO_TARGET_WASM32_UNKNOWN_UNKNOWN_RUNNER=wasm-bindgen-test-runner \
//!   cargo test --target wasm32-unknown-unknown --no-default-features \
//!   --features sideeffect --test sideeffect_wasm
//! ```
//!
//! The dry-run flag, the dryrun and testfake implementations and the
//! publisher need no clock, threads or file system.

#![cfg(all(feature = "sideeffect", target_arch = "wasm32", target_os = "unknown"))]

use std::sync::Arc;

use hop_top_kit::bus::Context;
use hop_top_kit::sideeffect::dryrun::{self, SharedBuf};
use hop_top_kit::sideeffect::{
    is_dry_run, with_dry_run, Bus, DryRunPublisher, Fs, Http, Request, DRY_RUN_MECHANISM,
};
use serde_json::json;
use wasm_bindgen_test::wasm_bindgen_test;

use hop_top_kit::sideeffect::testfake;

#[wasm_bindgen_test]
fn dry_run_flag_round_trips() {
    let ctx = with_dry_run(&Context::background(), true);
    assert!(is_dry_run(&ctx));
    assert!(!is_dry_run(&with_dry_run(&ctx, false)));
}

#[wasm_bindgen_test]
fn dryrun_fs_describes() {
    let buf = SharedBuf::default();
    dryrun::Fs::new()
        .with_writer(buf.clone())
        .remove("/c".as_ref())
        .unwrap();
    assert_eq!(buf.contents(), "[dry-run] would remove /c\n");
}

#[wasm_bindgen_test]
fn publisher_tags_in_dry_run() {
    let fake = Arc::new(testfake::Bus::new());
    let p = DryRunPublisher::new(Some(fake.clone()));
    let ctx = with_dry_run(&Context::background(), true);
    p.publish(&ctx, "k.r.e.created", "src", json!({"qualifiers": {}}))
        .unwrap();
    let calls = fake.calls();
    assert!(calls[0].to_string().contains(DRY_RUN_MECHANISM));
}

#[wasm_bindgen_test]
async fn dryrun_http_intercepts_post() {
    let inner = Arc::new(testfake::Http::new());
    let h = dryrun::Http::new(inner.clone()).with_writer(SharedBuf::default());
    let r = h
        .send(Request::new("POST", "http://x.test/"))
        .await
        .unwrap();
    assert_eq!(r.status, 201);
    assert!(inner.calls().is_empty());
}
