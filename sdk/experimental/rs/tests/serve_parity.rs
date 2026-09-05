//! Pins this port's serve constants against `contracts/parity/serve.json`.
//!
//! The Go suite (`contracts/parity/parity_test.go`) already checks the
//! fixture against the reference implementation. This does the same from
//! the Rust side, so a value drifting in either the fixture or this port
//! fails a build rather than surfacing later as a subscriber that never
//! fires or a supervising process that reads the wrong exit code.
//!
//! It also asserts the fixture's `ports.rs` row matches what this port
//! actually ships — an honest conformance record is the whole point of
//! the file, and a row claiming more than the code does is worse than a
//! PENDING.

#![cfg(feature = "serve")]

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use hop_top_kit::serve::{
    default_topics, is_reserved_name, validate_name, FailurePolicy, LifecycleOutcome,
    ServiceConfig, SupervisorConfig, DEFAULT_READY_TIMEOUT, DEFAULT_SHUTDOWN_TIMEOUT,
    DEFAULT_STOP_TIMEOUT, DEFAULT_TOPIC_PREFIX, RESERVED_NAMES, SHUTDOWN_SIGNALS,
};
use serde_json::Value;

fn locate_contract() -> PathBuf {
    // Same anchor the typeid contract loader uses: CARGO_MANIFEST_DIR is
    // stable under `cargo test` and under editor invocations alike.
    let manifest = env!("CARGO_MANIFEST_DIR");
    let mut dir: &Path = Path::new(manifest);
    for _ in 0..10 {
        let candidate = dir.join("contracts").join("parity").join("serve.json");
        if candidate.exists() {
            return candidate;
        }
        match dir.parent() {
            Some(parent) => dir = parent,
            None => break,
        }
    }
    panic!("contracts/parity/serve.json: not found walking up from {manifest}");
}

fn load() -> Value {
    let path = locate_contract();
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {}: {e}", path.display()))
}

fn constants() -> Value {
    load()["constants"].clone()
}

#[test]
fn the_name_pattern_matches_the_fixture() {
    let want = constants()["name_pattern"].as_str().unwrap().to_string();
    assert_eq!(want, hop_top_kit::serve::NAME_PATTERN);

    // And the predicate actually implements it, rather than the constant
    // being a decorative string next to a divergent check.
    for ok in ["api", "web-ui", "a", "x9"] {
        assert_eq!(validate_name(ok), None, "{ok}");
    }
    for bad in ["API", "1api", "-api", "api_x", ""] {
        assert!(validate_name(bad).is_some(), "{bad:?}");
    }
}

#[test]
fn the_reserved_names_match_the_fixture() {
    let want: Vec<String> = constants()["reserved_names"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap().to_string())
        .collect();
    let got: Vec<String> = RESERVED_NAMES.iter().map(|s| (*s).to_string()).collect();
    assert_eq!(got, want);
    for name in &want {
        assert!(is_reserved_name(name), "{name}");
    }
}

#[test]
fn the_six_topic_strings_match_the_fixture() {
    let c = constants();
    let fixture = c["topics"].as_object().unwrap();
    assert_eq!(
        fixture["prefix"].as_str().unwrap(),
        DEFAULT_TOPIC_PREFIX,
        "topic prefix"
    );

    let mine = default_topics(DEFAULT_TOPIC_PREFIX);
    let mut want: BTreeMap<String, String> = BTreeMap::new();
    for (key, value) in fixture {
        if key == "prefix" {
            continue;
        }
        want.insert(key.clone(), value.as_str().unwrap().to_string());
    }
    assert_eq!(want.len(), 6, "the fixture must declare six transitions");
    assert_eq!(mine, want);
}

#[test]
fn the_payload_keys_match_the_fixture() {
    use hop_top_kit::serve::{PAYLOAD_KEY_ADDRESS, PAYLOAD_KEY_ERROR, PAYLOAD_KEY_SERVICE};
    let c = constants();
    let keys = c["payload_keys"].as_object().unwrap();
    for k in [PAYLOAD_KEY_SERVICE, PAYLOAD_KEY_ERROR, PAYLOAD_KEY_ADDRESS] {
        assert!(keys.contains_key(k), "fixture is missing payload key {k}");
    }
    assert_eq!(keys.len(), 3, "no payload key beyond the three is contract");
}

#[test]
fn the_config_keys_and_defaults_match_the_fixture() {
    let c = constants();
    let keys = c["config_keys"].as_object().unwrap();

    let enabled = &keys["services.<name>.enabled"];
    assert_eq!(enabled["type"], "bool");
    assert_eq!(enabled["default"], Value::Bool(false));
    assert!(!ServiceConfig::default().enabled);

    let ready = &keys["services.<name>.ready_timeout"];
    assert_eq!(ready["type"], "duration");
    assert_eq!(ready["default"], "30s");
    assert_eq!(DEFAULT_READY_TIMEOUT.as_secs(), 30);
    assert_eq!(
        ServiceConfig::default().ready_timeout,
        DEFAULT_READY_TIMEOUT
    );

    let stop = &keys["services.<name>.stop_timeout"];
    assert_eq!(stop["type"], "duration");
    assert_eq!(stop["default"], "30s");
    assert_eq!(DEFAULT_STOP_TIMEOUT.as_secs(), 30);
    assert_eq!(ServiceConfig::default().stop_timeout, DEFAULT_STOP_TIMEOUT);

    let policy = &keys["services.failure_policy"];
    assert_eq!(policy["type"], "enum");
    assert_eq!(policy["default"], "fail-fast");
    assert_eq!(
        SupervisorConfig::default().failure_policy.as_str(),
        "fail-fast"
    );

    let shutdown = &keys["services.shutdown_timeout"];
    assert_eq!(shutdown["type"], "duration");
    assert_eq!(shutdown["default"], "60s");
    assert_eq!(DEFAULT_SHUTDOWN_TIMEOUT.as_secs(), 60);
    assert_eq!(
        SupervisorConfig::default().shutdown_timeout,
        DEFAULT_SHUTDOWN_TIMEOUT
    );
}

#[test]
fn the_failure_policies_match_the_fixture() {
    let c = constants();
    let want: Vec<String> = c["failure_policies"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap().to_string())
        .collect();
    for p in &want {
        assert!(
            FailurePolicy::parse(p).is_some(),
            "the fixture declares {p:?} and this port rejects it"
        );
    }
    let mine = [FailurePolicy::FailFast, FailurePolicy::Isolate];
    for p in mine {
        assert!(
            want.iter().any(|w| w == p.as_str()),
            "this port accepts {:?} and the fixture omits it",
            p.as_str()
        );
    }
    assert_eq!(want.len(), mine.len());
}

#[test]
fn the_signals_match_the_fixture() {
    let c = constants();
    let want: Vec<String> = c["signals"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap().to_string())
        .collect();
    let got: Vec<String> = SHUTDOWN_SIGNALS.iter().map(|s| (*s).to_string()).collect();
    assert_eq!(got, want);
}

#[test]
fn every_exit_code_row_matches_the_fixture() {
    use LifecycleOutcome::*;
    let c = constants();
    let rows = c["exit_codes"].as_object().unwrap();

    let mine: &[(&str, LifecycleOutcome)] = &[
        ("clean-stop", CleanStop),
        ("invalid-selection", InvalidSelection),
        ("config-invalid", ConfigInvalid),
        ("no-services", NoServices),
        ("unknown-service", UnknownService),
        ("policy-denied", PolicyDenied),
        ("start-failed", StartFailed),
        ("runtime-crash", RuntimeCrash),
        ("shutdown-timeout", ShutdownTimeout),
    ];
    assert_eq!(
        rows.len(),
        mine.len(),
        "the fixture and this port must model the same outcome set"
    );
    for (key, outcome) in mine {
        let row = rows
            .get(*key)
            .unwrap_or_else(|| panic!("fixture is missing exit-code row {key}"));
        assert_eq!(row["code"], outcome.code(), "{key} code");
        assert_eq!(
            row["exit"].as_i64().unwrap() as i32,
            outcome.exit_code(),
            "{key} exit"
        );
        // The outcome's own identifier is the fixture key, so the
        // supervisor `stopped` reason a subscriber reads matches the
        // conformance record.
        assert_eq!(outcome.as_str(), *key);
    }
}

/// The fixture's `ports.rs` row must describe what this port actually
/// ships, not what it aspires to. The two PENDING entries are the ones
/// that need an argument parser, which `sdk/experimental/rs/src/cli.rs`
/// does not yet provide.
#[test]
fn the_rust_port_row_is_an_honest_record() {
    let doc = load();
    let rs = doc["ports"]["rs"].as_object().unwrap();

    // Every behavior key the fixture declares must appear on every port
    // row, so a status can never go missing rather than going PENDING.
    let behaviors = doc["behaviors"].as_object().unwrap();
    for key in behaviors.keys() {
        assert!(rs.contains_key(key), "ports.rs is missing status for {key}");
    }

    // Shipped: everything exercised by tests/serve.rs.
    for shipped in [
        "override_rule",
        "registration_seam",
        "readiness",
        "lifecycle_events",
        "ordered_shutdown",
        "failure_policy",
        "exit_taxonomy",
        "config_keys",
    ] {
        assert_eq!(rs[shipped], "SHIPPED", "{shipped}");
    }

    // Deferred: the two that need a command layer to parse operands and
    // flags. Claiming these without a parser would be the dishonest
    // half of the record.
    for pending in ["hierarchy", "list_flag"] {
        assert_eq!(
            rs[pending], "PENDING",
            "{pending} needs a command layer this SDK does not have"
        );
    }

    assert!(
        rs["implementation"].is_string(),
        "a shipped port names where its implementation lives"
    );
    assert!(
        rs["note"].as_str().unwrap().contains("cli.rs"),
        "the note must say why the two PENDING entries are pending"
    );
}
