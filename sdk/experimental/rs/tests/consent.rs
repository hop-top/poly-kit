// Integration tests for telemetry::consent. Same XDG-redirect idiom
// as install_id.rs — every test points XDG_CONFIG_HOME at a tempdir.
#![cfg(feature = "telemetry")]

use hop_top_kit::telemetry::{consent_path, load_consent};
use std::sync::Mutex;
use tempfile::TempDir;

static ENV_LOCK: Mutex<()> = Mutex::new(());

fn isolated_config_dir() -> TempDir {
    let tmp = tempfile::tempdir().expect("tempdir");
    std::env::set_var("XDG_CONFIG_HOME", tmp.path());
    std::env::set_var("HOME", tmp.path());
    tmp
}

fn write_consent(content: &str) {
    let p = consent_path();
    std::fs::create_dir_all(p.parent().unwrap()).unwrap();
    std::fs::write(&p, content).unwrap();
}

#[test]
fn path_lives_under_xdg_config_home() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_config_dir();
    let p = consent_path();
    let s = p.to_string_lossy();
    assert!(s.ends_with("kit/telemetry.yaml"), "unexpected path: {s}");
}

#[test]
fn missing_file_returns_denied() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_config_dir();
    let c = load_consent();
    assert!(!c.allowed);
    assert_eq!(c.prompt_version, 0);
    assert_eq!(c.decision_source, "config");
    assert!(c.decided_at.is_none());
}

#[test]
fn granted_state_is_allowed() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_config_dir();
    write_consent(
        r#"
telemetry:
  consent:
    state: granted
    prompt_version: 2
    decision_source: cli
    decided_at: "2026-05-19T12:00:00Z"
"#,
    );
    let c = load_consent();
    assert!(c.allowed);
    assert_eq!(c.prompt_version, 2);
    assert_eq!(c.decision_source, "cli");
    assert_eq!(c.decided_at.as_deref(), Some("2026-05-19T12:00:00Z"));
}

#[test]
fn denied_state_is_not_allowed_but_metadata_preserved() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_config_dir();
    write_consent(
        r#"
telemetry:
  consent:
    state: denied
    prompt_version: 1
"#,
    );
    let c = load_consent();
    assert!(!c.allowed);
    assert_eq!(c.prompt_version, 1);
    assert_eq!(c.decision_source, "config");
}

#[test]
fn unknown_state_defaults_to_denied() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_config_dir();
    write_consent(
        r#"
telemetry:
  consent:
    state: maybe
    prompt_version: 9
"#,
    );
    let c = load_consent();
    assert!(!c.allowed);
    // Unknown state collapses to default-denied shape.
    assert_eq!(c.prompt_version, 0);
    assert_eq!(c.decision_source, "config");
}

#[test]
fn missing_consent_block_defaults_to_denied() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_config_dir();
    write_consent(
        r#"
telemetry:
  some_other_key: 42
"#,
    );
    let c = load_consent();
    assert!(!c.allowed);
}

#[test]
fn malformed_yaml_defaults_to_denied() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_config_dir();
    write_consent("telemetry:\n  consent: [this is not a map\n");
    let c = load_consent();
    assert!(!c.allowed);
}
