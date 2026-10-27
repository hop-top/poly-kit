//! Telemetry paths resolve through `xdg`, as Go's adrg/xdg does, and an
//! old Rust location on macOS (`~/.config`, `~/.local/state`) is still
//! honoured. Env-mutating: one lock, XDG vars unset, HOME a temp dir.
#![cfg(feature = "telemetry")]

use std::path::Path;
use std::sync::Mutex;

use hop_top_kit::telemetry::{
    consent_path, get_install_id, install_id_path, legacy_consent_path, load_consent,
};
use hop_top_kit::xdg;

static ENV: Mutex<()> = Mutex::new(());

fn bare_home() -> (std::sync::MutexGuard<'static, ()>, tempfile::TempDir) {
    let g = ENV.lock().unwrap_or_else(|e| e.into_inner());
    let tmp = tempfile::tempdir().unwrap();
    std::env::remove_var("XDG_CONFIG_HOME");
    std::env::remove_var("XDG_STATE_HOME");
    std::env::set_var("HOME", tmp.path());
    (g, tmp)
}

fn write(p: &Path, body: &[u8]) {
    std::fs::create_dir_all(p.parent().unwrap()).unwrap();
    std::fs::write(p, body).unwrap();
}

#[test]
fn telemetry_paths_follow_the_xdg_homes() {
    let (_g, _tmp) = bare_home();
    assert_eq!(
        consent_path(),
        xdg::config_home().join("kit").join("config.yaml")
    );
    assert_eq!(
        legacy_consent_path(),
        xdg::config_home().join("kit").join("telemetry.yaml")
    );
    assert_eq!(
        install_id_path(),
        xdg::state_home().join("kit/telemetry/installation_id")
    );
}

#[test]
fn a_relative_xdg_config_home_is_ignored() {
    let (_g, _tmp) = bare_home();
    std::env::set_var("XDG_CONFIG_HOME", "relative/dir");
    assert_eq!(
        consent_path(),
        xdg::config_home().join("kit").join("config.yaml")
    );
    assert!(consent_path().is_absolute());
    std::env::remove_var("XDG_CONFIG_HOME");
}

#[cfg(target_os = "macos")]
#[test]
fn macos_resolves_application_support_like_go() {
    let (_g, tmp) = bare_home();
    let app = tmp.path().join("Library/Application Support");
    assert_eq!(consent_path(), app.join("kit/config.yaml"));
    assert_eq!(install_id_path(), app.join("kit/telemetry/installation_id"));
}

#[cfg(target_os = "macos")]
#[test]
fn macos_old_dot_config_deny_still_wins() {
    let (_g, tmp) = bare_home();
    write(
        &consent_path(),
        b"kit:\n  telemetry:\n    consent:\n      state: granted\n",
    );
    write(
        &tmp.path().join(".config/kit/config.yaml"),
        b"kit:\n  telemetry:\n    consent:\n      state: denied\n",
    );
    assert!(!load_consent().allowed);
    std::fs::remove_file(tmp.path().join(".config/kit/config.yaml")).unwrap();
    assert!(load_consent().allowed);
    write(
        &tmp.path().join(".config/kit/telemetry.yaml"),
        b"telemetry:\n  consent:\n    state: granted\n",
    );
    std::fs::remove_file(consent_path()).unwrap();
    assert!(
        load_consent().allowed,
        "an old-location grant alone still reads"
    );
}

#[cfg(target_os = "macos")]
#[test]
fn macos_old_install_id_is_moved_not_regenerated() {
    let (_g, tmp) = bare_home();
    let old = tmp
        .path()
        .join(".local/state/kit/telemetry/installation_id");
    write(&old, &[7u8; 32]);
    let id = get_install_id().unwrap();
    assert_eq!(std::fs::read(install_id_path()).unwrap(), vec![7u8; 32]);
    assert_eq!(get_install_id().unwrap(), id);
}
