//! Runs the shared `contracts/celpermission-v1` vectors against the
//! Rust permission gate. Go is canonical; a failure here means this
//! port drifted, not that the vector should change.

#![cfg(feature = "cli-permissions")]

use std::collections::BTreeMap;

use hop_top_kit::cli::policy::PermissionRule;
use hop_top_kit::cli::{CallerSource, PermissionGate, PermissionRequest};
use serde_json::Value;

fn vectors() -> Value {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../../contracts/celpermission-v1/vectors.json"
    );
    serde_json::from_str(&std::fs::read_to_string(path).expect("vectors")).expect("json")
}

fn s(v: &Value, k: &str) -> String {
    v.get(k).and_then(Value::as_str).unwrap_or("").to_string()
}

fn strings(v: &Value, k: &str) -> Vec<String> {
    v.get(k)
        .and_then(Value::as_array)
        .map(|a| a.iter().map(|x| x.as_str().unwrap().to_string()).collect())
        .unwrap_or_default()
}

fn rules(v: &Value) -> Vec<PermissionRule> {
    v.get("rules")
        .and_then(Value::as_array)
        .map(|a| {
            a.iter()
                .map(|r| PermissionRule {
                    name: s(r, "name"),
                    when: s(r, "when"),
                    effect: s(r, "effect"),
                    otherwise: s(r, "otherwise"),
                    message: s(r, "message"),
                })
                .collect()
        })
        .unwrap_or_default()
}

fn request(tree: &Value, r: &Value) -> PermissionRequest {
    let path = strings(r, "path");
    let tier = tree[path.join(" ")].as_str().expect("tier in tree");
    PermissionRequest {
        caller: s(r, "caller"),
        tenant: s(r, "tenant"),
        scopes: strings(r, "scopes"),
        source: match s(r, "source").as_str() {
            "verified" => CallerSource::Verified,
            "transport" => CallerSource::Transport,
            _ => CallerSource::None,
        },
        surface: s(r, "surface"),
        remote_addr: s(r, "remote_addr"),
        path,
        tier: tier.to_string(),
        args: strings(r, "args"),
        flags: r
            .get("flags")
            .and_then(Value::as_object)
            .map(|m| m.iter().map(|(k, v)| (k.clone(), v.clone())).collect())
            .unwrap_or_else(BTreeMap::new),
    }
}

#[test]
fn suites() {
    let doc = vectors();
    let mut n = 0;
    for suite in doc["suites"].as_array().unwrap() {
        for c in suite["cases"].as_array().unwrap() {
            let label = format!("{} / {}", suite["name"], c["name"]);
            // A case may carry its own rules; else the suite's apply.
            let set = if c.get("rules").is_some() { c } else { suite };
            let gate = PermissionGate::new(&rules(set)).unwrap_or_else(|e| panic!("{label}: {e}"));
            let got = gate.decide(&request(&doc["tree"], &c["request"]));
            assert_eq!(
                got.allowed,
                c["allowed"].as_bool().unwrap(),
                "{label}: {got:?}"
            );
            if let Some(want) = c.get("reason").and_then(Value::as_str) {
                assert_eq!(got.reason, want, "{label}");
            }
            if let Some(p) = c.get("reason_prefix").and_then(Value::as_str) {
                assert!(got.reason.starts_with(p), "{label}: {}", got.reason);
            }
            if got.allowed {
                assert!(got.reason.is_empty(), "{label}");
            }
            n += 1;
        }
    }
    assert!(n >= 40, "only {n} cases ran");
}

#[test]
fn compile_errors() {
    let doc = vectors();
    for c in doc["compile_errors"].as_array().unwrap() {
        let err = PermissionGate::new(&rules(c)).expect_err(&format!("{}", c["name"]));
        if let Some(p) = c.get("error_prefix").and_then(Value::as_str) {
            assert!(err.starts_with(p), "{}: {err}", c["name"]);
        }
        if let Some(p) = c.get("error_contains").and_then(Value::as_str) {
            assert!(err.contains(p), "{}: {err}", c["name"]);
        }
    }
}
