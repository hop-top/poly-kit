//! `services.<name>.*` resolved through the config env lookup:
//! `<TOOL>_SERVICES_<NAME>_ENABLED` and friends, without touching the
//! process environment.

#![cfg(all(feature = "serve", feature = "config", not(target_arch = "wasm32")))]

use std::collections::HashMap;
use std::sync::Arc;
use std::time::Duration;

use hop_top_kit::config::{bind_env, EnvLookup};
use hop_top_kit::serve::{
    configs_from_lookup, ServiceConfig, DEFAULT_READY_TIMEOUT, DEFAULT_STOP_TIMEOUT,
};

fn lookup(vars: &[(&str, &str)]) -> EnvLookup {
    let m: HashMap<String, String> = vars
        .iter()
        .map(|(k, v)| (k.to_string(), v.to_string()))
        .collect();
    bind_env("mytool", &[])
        .unwrap()
        .with_env(Arc::new(move |k: &str| m.get(k).cloned()))
}

#[test]
fn enabled_env_var_enables_service() {
    let l = lookup(&[("MYTOOL_SERVICES_API_ENABLED", "true")]);
    let c = configs_from_lookup(&l, &["api", "socket"]);
    assert_eq!(c.get("api"), Some(&ServiceConfig::enabled()));
    assert!(!c.contains_key("socket"), "no keys set: not configured");
}

#[test]
fn explicit_false_is_configured_but_disabled() {
    let l = lookup(&[("MYTOOL_SERVICES_API_ENABLED", "false")]);
    let c = configs_from_lookup(&l, &["api"]);
    assert_eq!(c.get("api"), Some(&ServiceConfig::disabled()));
}

#[test]
fn unparseable_enabled_is_false() {
    let l = lookup(&[("MYTOOL_SERVICES_API_ENABLED", "yes please")]);
    let c = configs_from_lookup(&l, &["api"]);
    assert!(!c["api"].enabled);
}

#[test]
fn bool_spellings_follow_parse_bool() {
    for (raw, want) in [
        ("1", true),
        ("T", true),
        ("TRUE", true),
        ("0", false),
        ("f", false),
    ] {
        let l = lookup(&[("MYTOOL_SERVICES_API_ENABLED", raw)]);
        assert_eq!(
            configs_from_lookup(&l, &["api"])["api"].enabled,
            want,
            "{raw}"
        );
    }
}

#[test]
fn timeouts_resolve_and_default() {
    let l = lookup(&[
        ("MYTOOL_SERVICES_API_READY_TIMEOUT", "5s"),
        ("MYTOOL_SERVICES_API_STOP_TIMEOUT", "bogus"),
    ]);
    let c = configs_from_lookup(&l, &["api"]);
    assert!(!c["api"].enabled, "enabled defaults to false");
    assert_eq!(c["api"].ready_timeout, Duration::from_secs(5));
    assert_eq!(c["api"].stop_timeout, DEFAULT_STOP_TIMEOUT);
    let _ = DEFAULT_READY_TIMEOUT;
}

#[test]
fn dotted_set_beats_env() {
    let mut l = lookup(&[("MYTOOL_SERVICES_API_ENABLED", "false")]);
    l.set(
        "services.api.enabled",
        hop_top_kit::config::Value::Bool(true),
    );
    assert!(configs_from_lookup(&l, &["api"])["api"].enabled);
}
