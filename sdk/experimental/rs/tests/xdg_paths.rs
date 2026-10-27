//! Telemetry paths resolve through `xdg`, as Go's adrg/xdg does, and an
//! old Rust location on macOS (`~/.config`, `~/.local/state`) is still
//! honoured. Env-mutating: one lock, XDG vars unset, HOME a temp dir.
#![cfg(feature = "telemetry")]

#[cfg(target_os = "macos")]
use std::path::Path;
use std::sync::Mutex;

use hop_top_kit::telemetry::{consent_path, install_id_path, legacy_consent_path};
#[cfg(target_os = "macos")]
use hop_top_kit::telemetry::{get_install_id, load_consent};
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

#[cfg(target_os = "macos")]
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
    assert!(!old.exists(), "migrated by move");
    assert_eq!(get_install_id().unwrap(), id);
}

#[cfg(target_os = "macos")]
const YES: &[u8] = b"kit:\n  telemetry:\n    consent:\n      state: granted\n";

#[cfg(target_os = "macos")]
#[test]
fn macos_canonical_reset_and_old_yes_is_not_granted() {
    let (_g, tmp) = bare_home();
    write(&consent_path(), b"other: 1\nkit:\n  telemetry: {}\n");
    let old = tmp.path().join(".config/kit/config.yaml");
    write(&old, YES);
    assert!(!load_consent().allowed, "an old-location yes never grants");
    assert!(!old.exists(), "an ignored old-location allow is removed");
    assert!(!load_consent().allowed);
}

#[cfg(target_os = "macos")]
#[test]
fn macos_canonical_unset_and_old_yes_migrates_once() {
    let (_g, tmp) = bare_home();
    let old = tmp.path().join(".config/kit/config.yaml");
    write(&old, YES);
    assert!(load_consent().allowed);
    assert!(!old.exists(), "migrated, not copied");
    assert_eq!(std::fs::read(consent_path()).unwrap(), YES);
    use std::os::unix::fs::PermissionsExt;
    let mode = std::fs::metadata(consent_path())
        .unwrap()
        .permissions()
        .mode()
        & 0o777;
    assert_eq!(mode, 0o600);
    assert!(load_consent().allowed);
}

#[cfg(target_os = "macos")]
#[test]
fn macos_old_no_and_canonical_yes_is_deny_and_kept() {
    let (_g, tmp) = bare_home();
    write(&consent_path(), YES);
    let old = tmp.path().join(".config/kit/telemetry.yaml");
    write(&old, b"telemetry:\n  consent:\n    state: denied\n");
    assert!(!load_consent().allowed);
    assert!(old.exists(), "an old-location deny keeps applying");
}

#[cfg(target_os = "macos")]
#[test]
fn macos_old_file_with_other_keys_is_not_deleted() {
    let (_g, tmp) = bare_home();
    write(&consent_path(), b"kit:\n  telemetry: {}\n");
    let old = tmp.path().join(".config/kit/config.yaml");
    write(
        &old,
        b"mine: keep\nkit:\n  telemetry:\n    consent:\n      state: granted\n",
    );
    assert!(!load_consent().allowed);
    assert!(
        old.exists(),
        "a file holding more than the record is left alone"
    );
}

#[cfg(target_os = "macos")]
#[test]
fn macos_rotate_never_resurrects_the_old_install_id() {
    let (_g, tmp) = bare_home();
    let old = tmp
        .path()
        .join(".local/state/kit/telemetry/installation_id");
    write(&old, &[7u8; 32]);
    let first = get_install_id().unwrap();
    assert!(!old.exists(), "migrated by move");
    let rotated = hop_top_kit::telemetry::rotate().unwrap();
    assert_ne!(rotated, first);
    assert!(!old.exists());
    std::fs::remove_file(install_id_path()).unwrap();
    let fresh = get_install_id().unwrap();
    assert_ne!(fresh, first, "the old id never comes back");
}

#[cfg(target_os = "macos")]
#[test]
fn macos_rotate_removes_a_stray_old_install_id() {
    let (_g, tmp) = bare_home();
    get_install_id().unwrap();
    let old = tmp
        .path()
        .join(".local/state/kit/telemetry/installation_id");
    write(&old, &[9u8; 32]);
    hop_top_kit::telemetry::rotate().unwrap();
    assert!(!old.exists());
    write(&old, &[9u8; 32]);
    get_install_id().unwrap();
    assert!(
        !old.exists(),
        "a read with a canonical id drops the stale one"
    );
}
