//! Rust runner for the cross-language compliance conformance harness.
//!
//! Runs the in-tree SDK's compliance check against the shared opt-in
//! fixture and writes the observed score, denominator and per-factor
//! status as one stable JSON object to `KIT_CROSS_LANG_COMPLIANCE_OUT`.
//!
//! Only the static pass runs; Rust ships no runtime pass and F13 is a
//! static check in every port. Keys are sorted and `factors` is an
//! object keyed by factor number, so result order never matters: the
//! score, the denominator and the per-factor verdicts are the contract.

use std::collections::BTreeMap;
use std::path::PathBuf;

use hop_top_kit::compliance::run_static;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let fixture = std::env::var("KIT_CROSS_LANG_COMPLIANCE_FIXTURE")
        .map(PathBuf::from)
        .map_err(|_| "KIT_CROSS_LANG_COMPLIANCE_FIXTURE must be set")?;
    let out = std::env::var("KIT_CROSS_LANG_COMPLIANCE_OUT")
        .map_err(|_| "KIT_CROSS_LANG_COMPLIANCE_OUT must be set")?;

    let report = run_static(&fixture)?;

    let mut factors = BTreeMap::new();
    let mut names = BTreeMap::new();
    for r in &report.results {
        let key = r.factor.number().to_string();
        factors.insert(key.clone(), r.status.as_str());
        names.insert(key, r.name.clone());
    }
    let obs = serde_json::json!({
        "lang": "rs",
        "score": report.score,
        "total": report.total,
        "factors": factors,
        "names": names,
    });
    // Sort every object's keys: serde_json built with preserve_order
    // keeps insertion order, so go through a BTreeMap tree.
    let sorted: BTreeMap<String, serde_json::Value> = obs
        .as_object()
        .expect("object")
        .iter()
        .map(|(k, v)| (k.clone(), v.clone()))
        .collect();
    std::fs::write(out, serde_json::to_string_pretty(&sorted)? + "\n")?;
    Ok(())
}
