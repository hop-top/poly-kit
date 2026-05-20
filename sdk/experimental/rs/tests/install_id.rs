// Integration tests for telemetry::install_id. The implementation
// resolves its on-disk path from XDG_STATE_HOME (with a HOME fallback),
// so every test redirects both to a tempdir to avoid touching the
// developer's real state dir.
#![cfg(feature = "telemetry")]

use hop_top_kit::telemetry::{get_install_id, install_id_path, rotate};
use std::os::unix::fs::PermissionsExt;
use std::sync::Mutex;
use tempfile::TempDir;

// XDG_STATE_HOME / HOME are process-global; serialise tests within
// this binary. Other test binaries (mode.rs, consent.rs) run in
// separate processes.
static ENV_LOCK: Mutex<()> = Mutex::new(());

/// Redirect XDG_STATE_HOME (and HOME, as a belt-and-braces fallback)
/// to a fresh tempdir. Returns the dir so its lifetime keeps the
/// directory alive for the test body.
fn isolated_state_dir() -> TempDir {
    let tmp = tempfile::tempdir().expect("tempdir");
    std::env::set_var("XDG_STATE_HOME", tmp.path());
    std::env::set_var("HOME", tmp.path());
    tmp
}

#[test]
fn path_lives_under_xdg_state_home() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_state_dir();
    let p = install_id_path();
    let s = p.to_string_lossy();
    assert!(
        s.ends_with("kit/telemetry/installation_id"),
        "unexpected path: {s}"
    );
}

#[test]
fn first_call_generates_64_char_hex() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_state_dir();
    let id = get_install_id().expect("get_install_id");
    assert_eq!(id.len(), 64, "expected 64-char hex SHA-256, got {id}");
    assert!(
        id.chars()
            .all(|c| c.is_ascii_hexdigit() && !c.is_uppercase()),
        "expected lowercase hex, got {id}"
    );
}

#[test]
fn on_disk_file_is_32_bytes_and_0600() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_state_dir();
    let _ = get_install_id().expect("first call");
    let p = install_id_path();
    let meta = std::fs::metadata(&p).expect("file metadata");
    assert_eq!(meta.len(), 32, "expected 32 raw bytes on disk");
    let mode = meta.permissions().mode() & 0o777;
    assert_eq!(mode, 0o600, "expected 0600 perms, got {:o}", mode);

    let parent_meta = std::fs::metadata(p.parent().unwrap()).expect("dir metadata");
    let parent_mode = parent_meta.permissions().mode() & 0o777;
    assert_eq!(
        parent_mode, 0o700,
        "expected 0700 parent dir perms, got {:o}",
        parent_mode
    );
}

#[test]
fn id_stable_across_calls() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_state_dir();
    let a = get_install_id().expect("first call");
    let b = get_install_id().expect("second call");
    assert_eq!(a, b, "install id should be stable across calls");
}

#[test]
fn rotate_changes_the_id() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_state_dir();
    let before = get_install_id().expect("seed");
    let after = rotate().expect("rotate");
    assert_ne!(before, after, "rotate must produce a different id");

    let again = get_install_id().expect("read after rotate");
    assert_eq!(again, after, "post-rotate reads should match rotated id");
}

#[test]
fn corrupt_file_returns_error() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = isolated_state_dir();
    // Pre-create the path with the wrong number of bytes.
    let p = install_id_path();
    std::fs::create_dir_all(p.parent().unwrap()).unwrap();
    std::fs::write(&p, b"too short").unwrap();

    let err = get_install_id().expect_err("should reject wrong-size file");
    assert_eq!(err.kind(), std::io::ErrorKind::InvalidData);
}
