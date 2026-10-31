//! Ports of the Go `core/identity` store tests and the end-to-end
//! round trip. Native only: the store touches the filesystem.
#![cfg(all(feature = "identity", not(target_arch = "wasm32")))]

use hop_top_kit::identity::*;

#[cfg(unix)]
use std::os::unix::fs::PermissionsExt;

#[test]
fn save_load_roundtrip() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    let k = Keypair::generate().unwrap();
    store.save(&k).unwrap();
    let loaded = store.load().unwrap();
    assert_eq!(loaded.public_key(), k.public_key());
    assert_eq!(loaded.seed(), k.seed());
}

#[cfg(unix)]
#[test]
fn permissions() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    store.save(&Keypair::generate().unwrap()).unwrap();
    let mode = |n: &str| {
        std::fs::metadata(dir.path().join(n))
            .unwrap()
            .permissions()
            .mode()
            & 0o777
    };
    assert_eq!(mode("id_ed25519"), 0o600);
    assert_eq!(mode("id_ed25519.pub"), 0o644);
}

#[test]
fn load_not_found() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    assert!(matches!(store.load(), Err(IdentityError::NotFound)));
    assert_eq!(
        IdentityError::NotFound.to_string(),
        "identity: keypair not found"
    );
}

#[test]
fn load_corrupt_is_not_not_found() {
    let dir = tempfile::tempdir().unwrap();
    std::fs::write(dir.path().join("id_ed25519"), b"junk").unwrap();
    let store = Store::new(dir.path()).unwrap();
    let e = store.load().unwrap_err();
    assert!(!matches!(e, IdentityError::NotFound), "{e}");
    // LoadOrGenerate must not overwrite a key it cannot parse.
    assert!(store.load_or_generate().is_err());
    assert_eq!(
        std::fs::read(dir.path().join("id_ed25519")).unwrap(),
        b"junk"
    );
}

#[test]
fn load_or_generate() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    assert!(!store.exists());
    let a = store.load_or_generate().unwrap();
    assert!(store.exists());
    let b = store.load_or_generate().unwrap();
    assert_eq!(a.public_key(), b.public_key());
    assert!(!dir.path().join("id_ed25519.tmp").exists());
}

#[test]
fn directory_creation() {
    let dir = tempfile::tempdir().unwrap();
    let nested = dir.path().join("nested").join("dir");
    Store::new(&nested).unwrap();
    assert!(nested.is_dir());
    #[cfg(unix)]
    assert_eq!(
        std::fs::metadata(&nested).unwrap().permissions().mode() & 0o777,
        0o700
    );
}

#[test]
fn exists() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    assert!(!store.exists());
    store.save(&Keypair::generate().unwrap()).unwrap();
    assert!(store.exists());
}

#[test]
fn default_store_lives_under_data_home_kit_identity() {
    // XDG_DATA_HOME is process-global; this is the only test touching it.
    let dir = tempfile::tempdir().unwrap();
    std::env::set_var("XDG_DATA_HOME", dir.path());
    let store = default_store().unwrap();
    assert!(dir.path().join("kit").join("identity").is_dir());
    drop(store);
}

#[test]
fn e2e_full_roundtrip() {
    let dir = tempfile::tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    let k = Keypair::generate().unwrap();
    store.save(&k).unwrap();
    let loaded = store.load().unwrap();
    assert_eq!(k.public_key_id(), loaded.public_key_id());

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;
    let token = loaded.sign_jwt(Claims {
        subject: "test-user".into(),
        issuer: "kit-test".into(),
        scopes: vec!["read".into(), "write".into()],
        issued_at: now,
        expires_at: now + 3600,
        ..Claims::default()
    });
    let got = verify_jwt(&token, &loaded.public_key()).unwrap();
    assert_eq!(got.subject, "test-user");
    assert_eq!(got.scopes, ["read", "write"]);

    let key = loaded.derive_key();
    let ct = encrypt(&key, b"sensitive data").unwrap();
    assert_eq!(decrypt(&key, &ct).unwrap(), b"sensitive data");
}
