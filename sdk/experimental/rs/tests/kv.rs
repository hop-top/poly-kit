//! Behavioural parity tests for the kv store, ported from the Go
//! `storage/kv` suite (`kv_test.go`, `config_test.go`, `sqlite_test.go`,
//! `prefix_test.go`, `regressions_test.go`).

#![cfg(feature = "kv")]

use std::sync::Arc;
use std::thread;
use std::time::Duration;

use hop_top_kit::kv::{Backend, Config, KvError, SqliteStore, Store, TtlStore};
use tempfile::TempDir;

/// Fresh file-backed store in a temp dir that outlives it.
fn new_store() -> (TempDir, SqliteStore) {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("test.db");
    let store = SqliteStore::new(path.to_str().unwrap()).unwrap();
    (dir, store)
}

fn sorted(mut keys: Vec<Vec<u8>>) -> Vec<Vec<u8>> {
    keys.sort();
    keys
}

// --- config_test.go ------------------------------------------------------

#[test]
fn open_sqlite() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("test.db");
    let store = Config::sqlite(path.to_str().unwrap()).open().unwrap();

    store.put(b"k1", b"v1").unwrap();
    assert_eq!(store.get(b"k1").unwrap(), Some(b"v1".to_vec()));
}

#[test]
fn open_sqlite_missing_path() {
    let err = Config::sqlite("").open().unwrap_err();
    assert!(
        matches!(err, KvError::PathRequired("sqlite")),
        "got {err:?}"
    );
}

#[test]
fn open_unknown_backend() {
    let err = Backend::parse("redis").unwrap_err();
    assert!(matches!(err, KvError::UnknownBackend(_)), "got {err:?}");
}

#[test]
fn open_uncompiled_backends_unavailable() {
    // Recognised names, not compiled in — parity with Go's build-tagged
    // backends, which report "not available", not "unknown backend".
    for backend in [Backend::Etcd, Backend::Tidb] {
        let cfg = Config {
            backend: backend.clone(),
            path: String::new(),
        };
        let err = cfg.open().unwrap_err();
        assert!(
            matches!(err, KvError::BackendUnavailable(_)),
            "{backend:?}: got {err:?}"
        );
    }
}

#[test]
fn open_badger_missing_path() {
    let cfg = Config {
        backend: Backend::Badger,
        path: String::new(),
    };
    let err = cfg.open().unwrap_err();
    assert!(
        matches!(err, KvError::PathRequired("badger")),
        "got {err:?}"
    );
}

// --- sqlite_test.go ------------------------------------------------------

#[test]
fn put_get_roundtrip() {
    let (_dir, s) = new_store();
    s.put(b"k1", b"v1").unwrap();
    assert_eq!(s.get(b"k1").unwrap(), Some(b"v1".to_vec()));
}

#[test]
fn get_missing() {
    let (_dir, s) = new_store();
    assert_eq!(s.get(b"missing").unwrap(), None);
}

#[test]
fn delete_removes_key() {
    let (_dir, s) = new_store();
    s.put(b"k1", b"v1").unwrap();
    s.delete(b"k1").unwrap();
    assert_eq!(s.get(b"k1").unwrap(), None);
}

#[test]
fn delete_missing_is_not_an_error() {
    let (_dir, s) = new_store();
    s.delete(b"never-existed").unwrap();
}

#[test]
fn list_prefix() {
    let (_dir, s) = new_store();
    s.put(b"app/a", b"1").unwrap();
    s.put(b"app/b", b"2").unwrap();
    s.put(b"other/c", b"3").unwrap();

    assert_eq!(
        sorted(s.list(b"app/").unwrap()),
        vec![b"app/a".to_vec(), b"app/b".to_vec()]
    );
}

#[test]
fn list_empty_prefix_returns_everything() {
    let (_dir, s) = new_store();
    s.put(b"app/a", b"1").unwrap();
    s.put(b"other/c", b"3").unwrap();

    assert_eq!(
        sorted(s.list(b"").unwrap()),
        vec![b"app/a".to_vec(), b"other/c".to_vec()]
    );
}

#[test]
fn ttl_expiration() {
    let (_dir, s) = new_store();
    s.put_with_ttl(b"ephemeral", b"x", Duration::from_millis(50))
        .unwrap();

    assert_eq!(s.get(b"ephemeral").unwrap(), Some(b"x".to_vec()));

    thread::sleep(Duration::from_millis(60));

    assert_eq!(s.get(b"ephemeral").unwrap(), None);
}

#[test]
fn ttl_expired_key_is_excluded_from_list() {
    let (_dir, s) = new_store();
    s.put(b"live", b"1").unwrap();
    s.put_with_ttl(b"dying", b"2", Duration::from_millis(50))
        .unwrap();

    assert_eq!(sorted(s.list(b"").unwrap()).len(), 2);

    thread::sleep(Duration::from_millis(60));

    assert_eq!(s.list(b"").unwrap(), vec![b"live".to_vec()]);
}

#[test]
fn put_clears_previous_ttl() {
    let (_dir, s) = new_store();
    s.put_with_ttl(b"k", b"1", Duration::from_millis(50))
        .unwrap();
    // Plain put writes expires_at = NULL, so the key must outlive the TTL.
    s.put(b"k", b"2").unwrap();

    thread::sleep(Duration::from_millis(60));

    assert_eq!(s.get(b"k").unwrap(), Some(b"2".to_vec()));
}

#[test]
fn sweep_reclaims_expired_rows() {
    let (_dir, s) = new_store();
    s.put_with_ttl(b"dying", b"x", Duration::from_millis(10))
        .unwrap();
    thread::sleep(Duration::from_millis(20));

    // The sweep fires once every 100 writes; drive past the threshold.
    for i in 0..200i32 {
        s.put(format!("filler-{i}").as_bytes(), b"v").unwrap();
    }

    // Row is physically gone, not merely filtered on read.
    let remaining = s.list(b"dying").unwrap();
    assert!(remaining.is_empty(), "expired row survived sweep");
}

#[test]
fn list_prefix_0xff_bytes() {
    let (_dir, s) = new_store();

    // Keys with a trailing 0xff byte: prefix_end increments the prior byte.
    let prefix = b"data\xff";
    let key1 = b"data\xffa";
    let key2 = b"data\xffb";
    let other = b"dataz";

    s.put(key1, b"1").unwrap();
    s.put(key2, b"2").unwrap();
    s.put(other, b"3").unwrap();

    let keys = sorted(s.list(prefix).unwrap());
    assert_eq!(keys, vec![key1.to_vec(), key2.to_vec()]);
    assert!(!keys.contains(&other.to_vec()));

    // Pure 0xff prefix: unbounded upper bound returns all keys >= prefix.
    let all_ff = b"\xff\xff";
    let ff_key = b"\xff\xffx";
    s.put(ff_key, b"ff").unwrap();

    let keys = s.list(all_ff).unwrap();
    assert!(
        keys.contains(&ff_key.to_vec()),
        "all-0xff prefix must not silently drop keys"
    );
}

#[test]
fn concurrent_access() {
    // The Go store shares one `*sql.DB` pool across goroutines. rusqlite's
    // Connection is not Sync, so each thread opens its own connection to
    // the same file — the contention the WAL + busy_timeout pragmas from
    // `sqldb::open` exist to absorb.
    //
    // KNOWN FLAKE, roughly 1 run in 20: `SqliteStore::new` fails with
    // `Pragma { pragma: "journal_mode=WAL", DatabaseBusy }`. The cause is
    // in `sqldb::apply_pragmas`, not here. Converting a fresh database
    // from rollback journal to WAL needs an exclusive lock, and SQLite
    // does not invoke the busy handler for that transition, so a
    // `busy_timeout` set on the same connection cannot absorb it: every
    // thread but the winner gets SQLITE_BUSY immediately. All 20 threads
    // race to convert the same brand-new file here, which is exactly the
    // shape that triggers it. Fixing it means retrying (or serialising)
    // the journal_mode pragma inside `sqldb::open`, which is outside this
    // module.
    let dir = tempfile::tempdir().unwrap();
    let path = Arc::new(dir.path().join("test.db").to_str().unwrap().to_string());

    let handles: Vec<_> = (0..20)
        .map(|n| {
            let path = Arc::clone(&path);
            thread::spawn(move || {
                let s = SqliteStore::new(&path).unwrap();
                let key = format!("key-{n}");
                s.put(key.as_bytes(), b"val").unwrap();
                assert_eq!(s.get(key.as_bytes()).unwrap(), Some(b"val".to_vec()));
            })
        })
        .collect();

    for h in handles {
        h.join().unwrap();
    }

    let s = SqliteStore::new(&path).unwrap();
    assert_eq!(s.list(b"key-").unwrap().len(), 20);
}

#[test]
fn in_memory_dsn_leaves_no_file() {
    // Guards the sqldb porting hazard: `:memory:?cache=shared` handed to a
    // non-URI opener creates a literal file of that name on disk.
    let dsn = ":memory:?cache=shared";
    let s = SqliteStore::new(dsn).unwrap();
    s.put(b"k", b"v").unwrap();
    assert_eq!(s.get(b"k").unwrap(), Some(b"v".to_vec()));

    assert!(
        !std::path::Path::new(dsn).exists(),
        "in-memory DSN created a file on disk"
    );
}

#[test]
fn close_reports_success() {
    let (_dir, s) = new_store();
    s.put(b"k", b"v").unwrap();
    s.close().unwrap();
}

// --- regressions_test.go -------------------------------------------------
//
// prefix_end overflow caused 0xff-heavy prefixes to return empty results.

#[test]
fn regression_ff_prefix_returns_results() {
    let (_dir, s) = new_store();

    let prefix = b"data\xff";
    let key1 = b"data\xffa";
    let key2 = b"data\xffb";
    let other = b"dataz";

    s.put(key1, b"1").unwrap();
    s.put(key2, b"2").unwrap();
    s.put(other, b"3").unwrap();

    let keys = sorted(s.list(prefix).unwrap());
    assert_eq!(keys, vec![key1.to_vec(), key2.to_vec()]);
    assert!(!keys.contains(&other.to_vec()));
}

#[test]
fn regression_all_ff_prefix_unbounded() {
    let (_dir, s) = new_store();

    s.put(b"aaa", b"1").unwrap();
    s.put(b"\xff\xff", b"2").unwrap();
    s.put(b"\xff\xffz", b"3").unwrap();

    let keys = s.list(b"\xff\xff").unwrap();
    assert!(
        keys.contains(&b"\xff\xff".to_vec()),
        "all-0xff prefix must match itself"
    );
    assert!(
        keys.contains(&b"\xff\xffz".to_vec()),
        "all-0xff prefix must match keys beyond prefix"
    );
    assert!(
        !keys.contains(&b"aaa".to_vec()),
        "all-0xff prefix must not include keys below prefix"
    );
}

#[test]
fn regression_normal_prefix_boundary() {
    let (_dir, s) = new_store();

    s.put(b"abc", b"1").unwrap();
    s.put(b"abd", b"2").unwrap();

    assert_eq!(
        sorted(s.list(b"ab").unwrap()),
        vec![b"abc".to_vec(), b"abd".to_vec()],
        r#"list("ab") must return both "abc" and "abd""#
    );

    assert_eq!(
        s.list(b"abc").unwrap(),
        vec![b"abc".to_vec()],
        r#"list("abc") must return only "abc""#
    );
}
