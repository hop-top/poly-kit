// Integration tests for telemetry::mode. Cargo compiles every file
// under tests/ unconditionally, so the entire body is gated on the
// `telemetry` feature to keep default-feature builds happy.
#![cfg(feature = "telemetry")]

use hop_top_kit::telemetry::{parse_mode, resolve_mode, Mode};
use std::sync::Mutex;

// resolve_mode reads three env vars. std::env::set_var is unsafe
// under threading (since Rust 1.71's safety lint, and always on some
// libc impls), and cargo runs tests in the same process by default.
// Serialise every test that touches the env via this mutex.
//
// Note: this guards within ONE integration binary (`tests/mode.rs`).
// Other test binaries run in separate processes, so they don't
// contend.
static ENV_LOCK: Mutex<()> = Mutex::new(());

fn clear_env() {
    std::env::remove_var("KIT_APP_PREFIX");
    std::env::remove_var("KIT_TELEMETRY_MODE");
    // Cover the prefixes the tests below set.
    for app in ["SPACED", "APP", "MYTOOL"] {
        std::env::remove_var(format!("{}_TELEMETRY_MODE", app));
    }
}

#[test]
fn resolve_mode_defaults_off_when_unset() {
    let _g = ENV_LOCK.lock().unwrap();
    clear_env();
    assert_eq!(resolve_mode(), Mode::Off);
}

#[test]
fn resolve_mode_reads_kit_var() {
    let _g = ENV_LOCK.lock().unwrap();
    clear_env();
    std::env::set_var("KIT_TELEMETRY_MODE", "anon");
    assert_eq!(resolve_mode(), Mode::Anon);

    std::env::set_var("KIT_TELEMETRY_MODE", "full");
    assert_eq!(resolve_mode(), Mode::Full);

    std::env::set_var("KIT_TELEMETRY_MODE", "off");
    assert_eq!(resolve_mode(), Mode::Off);

    clear_env();
}

#[test]
fn resolve_mode_invalid_kit_var_falls_to_off() {
    let _g = ENV_LOCK.lock().unwrap();
    clear_env();
    std::env::set_var("KIT_TELEMETRY_MODE", "garbage");
    // Unknown token at the only level => default Off.
    assert_eq!(resolve_mode(), Mode::Off);
    clear_env();
}

#[test]
fn resolve_mode_app_prefix_wins_over_kit() {
    let _g = ENV_LOCK.lock().unwrap();
    clear_env();
    std::env::set_var("KIT_APP_PREFIX", "spaced");
    std::env::set_var("SPACED_TELEMETRY_MODE", "full");
    std::env::set_var("KIT_TELEMETRY_MODE", "anon");
    assert_eq!(resolve_mode(), Mode::Full);
    clear_env();
}

#[test]
fn resolve_mode_app_prefix_invalid_falls_back_to_kit() {
    let _g = ENV_LOCK.lock().unwrap();
    clear_env();
    std::env::set_var("KIT_APP_PREFIX", "spaced");
    std::env::set_var("SPACED_TELEMETRY_MODE", "garbage");
    std::env::set_var("KIT_TELEMETRY_MODE", "anon");
    // App-prefix var parses as invalid; precedence walks to KIT.
    assert_eq!(resolve_mode(), Mode::Anon);
    clear_env();
}

#[test]
fn resolve_mode_app_prefix_case_insensitive() {
    let _g = ENV_LOCK.lock().unwrap();
    clear_env();
    std::env::set_var("KIT_APP_PREFIX", "mytool");
    std::env::set_var("MYTOOL_TELEMETRY_MODE", "Full");
    assert_eq!(resolve_mode(), Mode::Full);
    clear_env();
}

#[test]
fn parse_mode_serde_round_trip() {
    // Ensures the Serialize/Deserialize lowercase-rename works for
    // adopters that surface Mode through a config file.
    let m = Mode::Anon;
    let s = serde_json::to_string(&m).unwrap();
    assert_eq!(s, "\"anon\"");
    let back: Mode = serde_json::from_str(&s).unwrap();
    assert_eq!(back, Mode::Anon);
}

#[test]
fn parse_mode_unit_function_exposed() {
    // Smoke check that parse_mode is reachable through the public
    // re-export, not just the inner module.
    assert_eq!(parse_mode(Some("anon")), (Mode::Anon, true));
    assert_eq!(parse_mode(Some("nope")), (Mode::Off, false));
    assert_eq!(parse_mode(None), (Mode::Off, true));
}
