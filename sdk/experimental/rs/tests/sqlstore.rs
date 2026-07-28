//! Integration tests for the sqlstore port, mirroring the Go
//! `storage/sqlstore` suite plus the shared identity key-derivation
//! contract.

#![cfg(feature = "sqlstore")]

use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;
use std::time::Duration;

use hop_top_kit::sqlstore::backup::{backup_before_migrate, BackupOptions};
use hop_top_kit::sqlstore::{Options, Store, USER_MIGRATION_VERSION};

fn tmpdir() -> tempfile::TempDir {
    tempfile::tempdir().expect("create temp dir")
}

fn db_path(dir: &tempfile::TempDir, name: &str) -> String {
    dir.path().join(name).to_string_lossy().into_owned()
}

// --- store ---------------------------------------------------------------

#[test]
fn put_get_roundtrips() {
    let dir = tmpdir();
    let store = Store::open(db_path(&dir, "test.db"), Options::new()).unwrap();

    let mut value = HashMap::new();
    value.insert("hello".to_string(), "world".to_string());
    store.put("key1", &value).unwrap();

    let got: HashMap<String, String> = store.get("key1").unwrap().expect("key present");
    assert_eq!(got.get("hello").map(String::as_str), Some("world"));
}

#[test]
fn get_missing_key_returns_none() {
    let dir = tmpdir();
    let store = Store::open(db_path(&dir, "test.db"), Options::new()).unwrap();

    let got: Option<HashMap<String, String>> = store.get("missing").unwrap();
    assert!(got.is_none());
}

#[test]
fn ttl_expires_entries() {
    let dir = tmpdir();
    let store = Store::open(
        db_path(&dir, "test.db"),
        // The stored_at column has second precision (as in Go), so an
        // expiry test needs a window measured in seconds, not millis.
        Options::new().with_ttl(Duration::from_secs(1)),
    )
    .unwrap();

    store.put("ttl-key", "value").unwrap();
    assert_eq!(
        store.get::<String>("ttl-key").unwrap().as_deref(),
        Some("value"),
        "fresh entry must be readable"
    );

    std::thread::sleep(Duration::from_millis(2100));
    assert!(
        store.get::<String>("ttl-key").unwrap().is_none(),
        "entry past its TTL must read as absent"
    );
}

#[test]
fn zero_ttl_never_expires() {
    let dir = tmpdir();
    let store = Store::open(db_path(&dir, "test.db"), Options::new()).unwrap();
    store.put("k", "v").unwrap();
    std::thread::sleep(Duration::from_millis(50));
    assert_eq!(store.get::<String>("k").unwrap().as_deref(), Some("v"));
}

#[test]
fn put_overwrites_existing_key() {
    let dir = tmpdir();
    let store = Store::open(db_path(&dir, "test.db"), Options::new()).unwrap();

    store.put("k", "first").unwrap();
    store.put("k", "second").unwrap();

    assert_eq!(store.get::<String>("k").unwrap().as_deref(), Some("second"));
}

#[test]
fn delete_removes_entry() {
    let dir = tmpdir();
    let store = Store::open(db_path(&dir, "test.db"), Options::new()).unwrap();

    store.put("k", "v").unwrap();
    assert!(store.delete("k").unwrap());
    assert!(store.get::<String>("k").unwrap().is_none());
    assert!(!store.delete("k").unwrap(), "second delete is a no-op");
}

#[test]
fn store_persists_across_reopen() {
    let dir = tmpdir();
    let path = db_path(&dir, "persist.db");

    {
        let store = Store::open(&path, Options::new()).unwrap();
        store.put("k", "v").unwrap();
    }

    let store = Store::open(&path, Options::new()).unwrap();
    assert_eq!(store.get::<String>("k").unwrap().as_deref(), Some("v"));
}

#[test]
fn in_memory_path_creates_no_file() {
    // Guards the documented sqldb hazard: a DSN-shaped in-memory path must
    // not land a real file on disk.
    let before: Vec<PathBuf> = fs::read_dir(".")
        .unwrap()
        .filter_map(|e| e.ok().map(|e| e.path()))
        .collect();

    let store = Store::open(":memory:?cache=shared", Options::new()).unwrap();
    store.put("k", "v").unwrap();
    assert_eq!(store.get::<String>("k").unwrap().as_deref(), Some("v"));

    assert!(
        !PathBuf::from(":memory:?cache=shared").exists(),
        "DSN-shaped in-memory path must not materialise a file"
    );
    let after: Vec<PathBuf> = fs::read_dir(".")
        .unwrap()
        .filter_map(|e| e.ok().map(|e| e.path()))
        .collect();
    assert_eq!(before.len(), after.len(), "cwd gained a stray file");
}

// --- migrations ----------------------------------------------------------

#[test]
fn migrate_sql_creates_caller_tables() {
    let dir = tmpdir();
    let store = Store::open(
        db_path(&dir, "mig.db"),
        Options::new().with_migrate_sql("CREATE TABLE notes (id TEXT PRIMARY KEY)"),
    )
    .unwrap();

    store
        .conn()
        .execute("INSERT INTO notes (id) VALUES ('a')", [])
        .expect("caller table exists");
}

#[test]
fn migrations_are_recorded_and_idempotent() {
    let dir = tmpdir();
    let path = db_path(&dir, "mig.db");
    let sql = "CREATE TABLE notes (id TEXT PRIMARY KEY); INSERT INTO notes (id) VALUES ('seed')";

    {
        let store = Store::open(&path, Options::new().with_migrate_sql(sql)).unwrap();
        let versions: Vec<i64> = store
            .conn()
            .prepare("SELECT version FROM schema_versions ORDER BY version")
            .unwrap()
            .query_map([], |r| r.get(0))
            .unwrap()
            .map(Result::unwrap)
            .collect();
        assert_eq!(versions, vec![1, USER_MIGRATION_VERSION]);
    }

    // Reopening must not re-run the caller SQL: unifying on the sqldb
    // runner is precisely what makes non-idempotent seed inserts safe,
    // where Go's run-on-every-open MigrateSQL would fail here.
    let store = Store::open(&path, Options::new().with_migrate_sql(sql)).unwrap();
    let count: i64 = store
        .conn()
        .query_row("SELECT COUNT(*) FROM notes", [], |r| r.get(0))
        .unwrap();
    assert_eq!(count, 1, "caller migration must run exactly once");
}

#[test]
fn failed_migration_rolls_back() {
    let dir = tmpdir();
    let err = Store::open(
        db_path(&dir, "bad.db"),
        Options::new().with_migrate_sql("CREATE TABLE ok (id TEXT); THIS IS NOT SQL"),
    )
    .expect_err("invalid migration must fail");
    assert!(err.to_string().contains("migrate"), "got: {err}");
}

// --- backup --------------------------------------------------------------

#[test]
fn backup_before_migrate_is_noop_when_file_missing() {
    let dir = tmpdir();
    let got = backup_before_migrate(dir.path().join("test.db"), 3, &BackupOptions::new()).unwrap();
    assert!(got.is_none());
}

#[test]
fn backup_before_migrate_writes_timestamped_copy_to_dbs_dir() {
    let dir = tmpdir();
    let path = dir.path().join("test.db");
    let content = b"sqlite-data-here";
    fs::write(&path, content).unwrap();

    let got = backup_before_migrate(&path, 7, &BackupOptions::new())
        .unwrap()
        .expect("backup path");

    assert_eq!(got.parent().unwrap(), dir.path().join(".dbs"));
    let name = got.file_name().unwrap().to_string_lossy().into_owned();
    assert!(name.starts_with("test.pre-v7."), "got {name}");
    assert!(name.ends_with(".bak"), "got {name}");

    assert_eq!(fs::read(&got).unwrap(), content, "backup content mismatch");
    assert_eq!(fs::read(&path).unwrap(), content, "source was modified");
}

#[test]
fn backup_dir_option_overrides_destination() {
    let dir = tmpdir();
    let path = dir.path().join("test.db");
    fs::write(&path, b"data").unwrap();

    let override_dir = dir.path().join("custom").join("nested");
    let got = backup_before_migrate(&path, 9, &BackupOptions::new().with_dir(&override_dir))
        .unwrap()
        .expect("backup path");

    assert_eq!(got.parent().unwrap(), override_dir);
    assert!(override_dir.is_dir(), "override dir must be created");
}

#[test]
fn backup_checkpoints_a_live_wal_database() {
    // A WAL-mode database with uncheckpointed writes must still produce a
    // backup containing those rows.
    let dir = tmpdir();
    let path = db_path(&dir, "live.db");
    let store = Store::open(&path, Options::new()).unwrap();
    store.put("k", "v").unwrap();

    let backup = backup_before_migrate(&path, 1, &BackupOptions::new())
        .unwrap()
        .expect("backup path");

    let restored = Store::open(backup.to_string_lossy().into_owned(), Options::new()).unwrap();
    assert_eq!(restored.get::<String>("k").unwrap().as_deref(), Some("v"));
}

#[cfg(feature = "sqlstore-blob")]
mod blob_backup {
    use super::*;
    use hop_top_kit::blob::local::LocalStore;
    use hop_top_kit::blob::Store as BlobStore;
    use hop_top_kit::sqlstore::backup::{backup_to_blob, restore_from_blob};
    use std::io::Read;

    #[test]
    fn backup_restore_roundtrip_through_blob_store() {
        let db_dir = tmpdir();
        let blob_dir = tmpdir();

        let path = db_dir.path().join("app.db");
        let content = b"sqlite3-database-content";
        fs::write(&path, content).unwrap();

        let blobs = LocalStore::new(blob_dir.path()).unwrap();
        let key = "backups/app.db";

        backup_to_blob(&path, &blobs, key).unwrap();
        assert!(blobs.exists(key).unwrap(), "backup blob not found");

        let mut buf = Vec::new();
        blobs.get(key).unwrap().read_to_end(&mut buf).unwrap();
        assert_eq!(buf, content, "blob content mismatch");

        fs::remove_file(&path).unwrap();
        restore_from_blob(&path, &blobs, key).unwrap();
        assert_eq!(fs::read(&path).unwrap(), content, "restored content");
    }

    #[test]
    fn restore_creates_missing_parent_directories() {
        let blob_dir = tmpdir();
        let dest_root = tmpdir();
        let blobs = LocalStore::new(blob_dir.path()).unwrap();
        blobs
            .put("k", &b"payload"[..], "application/x-sqlite3")
            .unwrap();

        let path = dest_root.path().join("deep").join("nested").join("app.db");
        restore_from_blob(&path, &blobs, "k").unwrap();
        assert_eq!(fs::read(&path).unwrap(), b"payload");
    }

    #[test]
    fn restore_leaves_no_temp_file_behind() {
        let db_dir = tmpdir();
        let blob_dir = tmpdir();
        let blobs = LocalStore::new(blob_dir.path()).unwrap();
        blobs
            .put("k", &b"new"[..], "application/x-sqlite3")
            .unwrap();

        let path = db_dir.path().join("app.db");
        fs::write(&path, b"old").unwrap();
        restore_from_blob(&path, &blobs, "k").unwrap();

        assert_eq!(fs::read(&path).unwrap(), b"new");
        let leftovers: Vec<_> = fs::read_dir(db_dir.path())
            .unwrap()
            .filter_map(|e| e.ok())
            .map(|e| e.file_name().to_string_lossy().into_owned())
            .filter(|n| n.contains(".restore.tmp"))
            .collect();
        assert!(leftovers.is_empty(), "stray temp files: {leftovers:?}");
    }

    #[test]
    fn failed_restore_preserves_existing_database() {
        let db_dir = tmpdir();
        let blob_dir = tmpdir();
        let blobs = LocalStore::new(blob_dir.path()).unwrap();

        let path = db_dir.path().join("app.db");
        fs::write(&path, b"original").unwrap();

        // Missing key: the atomic swap must never begin.
        restore_from_blob(&path, &blobs, "absent").expect_err("missing key must fail");
        assert_eq!(
            fs::read(&path).unwrap(),
            b"original",
            "failed restore must not clobber the existing database"
        );
    }

    #[test]
    fn backup_to_blob_roundtrips_a_live_database() {
        let db_dir = tmpdir();
        let blob_dir = tmpdir();
        let path = db_path(&db_dir, "live.db");

        {
            let store = Store::open(&path, Options::new()).unwrap();
            store.put("k", "v").unwrap();
        }

        let blobs = LocalStore::new(blob_dir.path()).unwrap();
        backup_to_blob(&path, &blobs, "snap").unwrap();

        let restored_path = db_dir.path().join("restored.db");
        restore_from_blob(&restored_path, &blobs, "snap").unwrap();

        let store =
            Store::open(restored_path.to_string_lossy().into_owned(), Options::new()).unwrap();
        assert_eq!(store.get::<String>("k").unwrap().as_deref(), Some("v"));
    }
}

// --- encryption ----------------------------------------------------------

#[cfg(feature = "sqlstore-encrypt")]
mod encryption {
    use super::*;
    use hop_top_kit::sqlstore::crypto::derive_key;
    use hop_top_kit::sqlstore::EncryptedStore;

    fn seed(byte: u8) -> [u8; 32] {
        [byte; 32]
    }

    #[test]
    fn encrypted_put_get_roundtrips() {
        let dir = tmpdir();
        let inner = Store::open(db_path(&dir, "enc.db"), Options::new()).unwrap();
        let store = EncryptedStore::from_seed(inner, &seed(1));

        let mut value = HashMap::new();
        value.insert("msg".to_string(), "hello".to_string());
        store.put("secret", &value).unwrap();

        let got: HashMap<String, String> = store.get("secret").unwrap().expect("key present");
        assert_eq!(got.get("msg").map(String::as_str), Some("hello"));
    }

    #[test]
    fn raw_value_is_not_plaintext() {
        let dir = tmpdir();
        let inner = Store::open(db_path(&dir, "enc.db"), Options::new()).unwrap();
        let store = EncryptedStore::from_seed(inner, &seed(1));

        store.put("secret", "plaintext-value").unwrap();

        let raw = store
            .inner()
            .get_raw("secret")
            .unwrap()
            .expect("row present");
        assert!(
            !raw.contains("plaintext-value"),
            "stored bytes leaked the plaintext"
        );
    }

    #[test]
    fn different_seed_cannot_decrypt() {
        let dir = tmpdir();
        let inner = Store::open(db_path(&dir, "shared.db"), Options::new()).unwrap();

        let writer = EncryptedStore::from_seed(inner, &seed(1));
        writer.put("data", "secret-payload").unwrap();

        let reader = EncryptedStore::from_seed(writer.into_inner(), &seed(2));
        let err = reader
            .get::<String>("data")
            .expect_err("wrong key must fail");
        assert!(err.to_string().contains("decryption failed"), "got: {err}");
    }

    #[test]
    fn encrypted_missing_key_returns_none() {
        let dir = tmpdir();
        let inner = Store::open(db_path(&dir, "enc.db"), Options::new()).unwrap();
        let store = EncryptedStore::from_seed(inner, &seed(1));

        assert!(store.get::<String>("nonexistent").unwrap().is_none());
    }

    #[test]
    fn encrypted_store_persists_across_reopen() {
        let dir = tmpdir();
        let path = db_path(&dir, "enc.db");

        {
            let store =
                EncryptedStore::from_seed(Store::open(&path, Options::new()).unwrap(), &seed(9));
            store.put("k", "v").unwrap();
        }

        let store =
            EncryptedStore::from_seed(Store::open(&path, Options::new()).unwrap(), &seed(9));
        assert_eq!(store.get::<String>("k").unwrap().as_deref(), Some("v"));
    }

    /// Asserts the Rust key derivation against the shared cross-language
    /// vectors. Drift here means an encrypted store written by another
    /// language becomes unreadable, so the fixture is the authority.
    #[test]
    fn derive_key_matches_shared_contract() {
        let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("../../../contracts/identity-v1/derive-key.json");
        let raw =
            fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
        let fixture: serde_json::Value = serde_json::from_str(&raw).unwrap();

        assert_eq!(fixture["version"], 1, "unexpected fixture version");
        let cases = fixture["derive_key"].as_array().expect("derive_key array");
        assert!(!cases.is_empty(), "fixture has no cases");

        for case in cases {
            let name = case["name"].as_str().unwrap();
            let seed = hex_decode(case["seed"].as_str().unwrap());
            let want = case["derived_key"].as_str().unwrap();

            let seed: [u8; 32] = seed.try_into().expect("32-byte seed");
            assert_eq!(hex_encode(&derive_key(&seed)), want, "case {name}");
        }
    }

    fn hex_decode(s: &str) -> Vec<u8> {
        (0..s.len())
            .step_by(2)
            .map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("hex digit"))
            .collect()
    }

    fn hex_encode(bytes: &[u8]) -> String {
        bytes.iter().map(|b| format!("{b:02x}")).collect()
    }
}
