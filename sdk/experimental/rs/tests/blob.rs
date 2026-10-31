#![cfg(feature = "blob")]

use std::fs;
use std::io::{self, Read};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use hop_top_kit::blob::local::LocalStore;
use hop_top_kit::blob::{stat_by_list, BlobError, Object, Stater, Store};
use tempfile::TempDir;

fn setup() -> (TempDir, LocalStore) {
    let dir = TempDir::new().expect("tempdir");
    let store = LocalStore::new(dir.path()).expect("new store");
    (dir, store)
}

fn read_key(s: &LocalStore, key: &str) -> String {
    let mut rc = s.get(key).expect("get");
    let mut got = String::new();
    rc.read_to_string(&mut got).expect("read");
    got
}

/// Yields `n` bytes then fails, simulating an interrupted write.
struct FailingReader {
    byte: u8,
    remaining: usize,
}

impl Read for FailingReader {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        if self.remaining == 0 {
            return Err(io::Error::other("boom"));
        }
        let n = buf.len().min(self.remaining);
        buf[..n].fill(self.byte);
        self.remaining -= n;
        Ok(n)
    }
}

#[test]
fn put_get_roundtrip() {
    let (_dir, s) = setup();
    let data = "hello world";
    s.put("greet.txt", data.as_bytes(), "text/plain")
        .expect("put");
    assert_eq!(read_key(&s, "greet.txt"), data);
}

#[test]
fn nested_dirs() {
    let (_dir, s) = setup();
    s.put("a/b/c.txt", "nested".as_bytes(), "").expect("put");
    assert_eq!(read_key(&s, "a/b/c.txt"), "nested");
}

#[test]
fn get_missing_key() {
    let (_dir, s) = setup();
    assert!(matches!(s.get("nope"), Err(BlobError::NotFound(_))));
}

#[test]
fn delete_removes_key() {
    let (_dir, s) = setup();
    s.put("del.txt", "x".as_bytes(), "").expect("put");
    s.delete("del.txt").expect("delete");
    assert!(!s.exists("del.txt").expect("exists"));
}

#[test]
fn list_prefix() {
    let (_dir, s) = setup();
    s.put("logs/a.log", "1".as_bytes(), "").expect("put");
    s.put("logs/b.log", "2".as_bytes(), "").expect("put");
    s.put("data/x.bin", "3".as_bytes(), "").expect("put");

    let objs = s.list("logs/").expect("list");
    assert_eq!(objs.len(), 2);
    assert_eq!(objs[0].key, "logs/a.log");
    assert_eq!(objs[1].key, "logs/b.log");
    assert_eq!(objs[0].size, 1);
}

#[test]
fn exists_reports_presence() {
    let (_dir, s) = setup();
    assert!(!s.exists("nope").expect("exists"));
    s.put("yes.txt", "y".as_bytes(), "").expect("put");
    assert!(s.exists("yes.txt").expect("exists"));
}

#[test]
fn streaming_1mb() {
    let (_dir, s) = setup();
    let data = vec![b'A'; 1 << 20];
    s.put("big.bin", data.as_slice(), "application/octet-stream")
        .expect("put");

    let mut rc = s.get("big.bin").expect("get");
    let mut got = Vec::new();
    rc.read_to_end(&mut got).expect("read");
    assert_eq!(got.len(), data.len());
}

#[test]
fn key_escaping_root_is_rejected() {
    let (_dir, s) = setup();
    assert!(matches!(
        s.put("../escape.txt", "x".as_bytes(), ""),
        Err(BlobError::KeyEscapesRoot(_))
    ));
}

#[test]
fn put_failure_leaves_previous_value() {
    let (_dir, s) = setup();
    s.put("k.txt", "original".as_bytes(), "").expect("put");

    let err = s
        .put(
            "k.txt",
            FailingReader {
                byte: b'B',
                remaining: 32,
            },
            "",
        )
        .expect_err("expected write error");
    assert!(matches!(err, BlobError::Io { op: "write", .. }));

    assert_eq!(read_key(&s, "k.txt"), "original");
}

#[test]
fn put_failure_leaves_no_key_and_no_temp() {
    let (dir, s) = setup();

    s.put(
        "new.txt",
        FailingReader {
            byte: b'x',
            remaining: 2,
        },
        "",
    )
    .expect_err("expected write error");

    assert!(!s.exists("new.txt").expect("exists"));

    let entries: Vec<_> = fs::read_dir(dir.path())
        .expect("read_dir")
        .map(|e| e.expect("entry").file_name())
        .collect();
    assert!(entries.is_empty(), "temp file left behind: {entries:?}");
}

#[test]
fn put_overwrite_never_observed_partial() {
    let (_dir, s) = setup();
    let s = Arc::new(s);

    let small = "v1".to_string();
    let large = "Z".repeat(1 << 20);

    s.put("atomic.bin", small.as_bytes(), "").expect("put");

    let done = Arc::new(AtomicBool::new(false));

    let writer = {
        let s = Arc::clone(&s);
        let done = Arc::clone(&done);
        let small = small.clone();
        let large = large.clone();
        std::thread::spawn(move || {
            for _ in 0..20 {
                s.put("atomic.bin", large.as_bytes(), "")
                    .expect("put large");
                s.put("atomic.bin", small.as_bytes(), "")
                    .expect("put small");
            }
            done.store(true, Ordering::SeqCst);
        })
    };

    while !done.load(Ordering::SeqCst) {
        let mut rc = s.get("atomic.bin").expect("get during concurrent put");
        let mut got = Vec::new();
        rc.read_to_end(&mut got)
            .expect("read during concurrent put");
        assert!(
            got == small.as_bytes() || got == large.as_bytes(),
            "observed partial blob of {} bytes",
            got.len()
        );
    }

    writer.join().expect("writer thread");
}

#[test]
fn list_skips_temp_files() {
    let (dir, s) = setup();
    s.put("real.txt", "r".as_bytes(), "").expect("put");
    fs::write(dir.path().join(".real.txt.123.0.tmp"), b"junk").expect("write orphan");

    let objs = s.list("").expect("list");
    assert_eq!(objs.len(), 1);
    assert_eq!(objs[0].key, "real.txt");
}

/// Stored blobs must not be world-readable. Rust creates files at
/// `0666 & ~umask` (typically 0644), so `put` narrows to 0640 before the
/// rename, matching the Go backend.
#[cfg(unix)]
#[test]
fn put_stores_blob_not_world_readable() {
    use std::os::unix::fs::PermissionsExt;

    let (dir, s) = setup();
    s.put("secret.bin", "sensitive".as_bytes(), "")
        .expect("put");

    let mode = fs::metadata(dir.path().join("secret.bin"))
        .expect("stat")
        .permissions()
        .mode()
        & 0o777;
    assert_eq!(
        mode, 0o640,
        "got {mode:o}, want 640 to match the Go backend"
    );
}

// ---- content type (sidecar) + Stater ------------------------------------

fn types(s: &LocalStore, prefix: &str) -> std::collections::BTreeMap<String, String> {
    s.list(prefix)
        .expect("list")
        .into_iter()
        .map(|o| (o.key, o.content_type))
        .collect()
}

fn files_under(dir: &std::path::Path) -> Vec<std::path::PathBuf> {
    fn walk(d: &std::path::Path, out: &mut Vec<std::path::PathBuf>) {
        for e in fs::read_dir(d).expect("read_dir") {
            let p = e.expect("entry").path();
            if p.is_dir() {
                walk(&p, out);
            } else {
                out.push(p);
            }
        }
    }
    let mut v = Vec::new();
    walk(dir, &mut v);
    v
}

#[test]
fn content_type_round_trip() {
    let (_dir, s) = setup();
    s.put("ct/a.json", "{}".as_bytes(), "application/json")
        .expect("put");
    s.put("ct/b.bin", "x".as_bytes(), "").expect("put");
    s.put("ct/over", "1".as_bytes(), "text/plain").expect("put");
    s.put("ct/over", "2".as_bytes(), "text/csv").expect("put");
    s.put("ct/gone", "1".as_bytes(), "image/png").expect("put");
    s.delete("ct/gone").expect("delete");
    s.put("ct/gone", "1".as_bytes(), "").expect("put");

    let got = types(&s, "ct/");
    assert_eq!(got["ct/a.json"], "application/json");
    assert_eq!(got["ct/b.bin"], "");
    assert_eq!(got["ct/over"], "text/csv");
    assert_eq!(got["ct/gone"], "");
    assert_eq!(got.len(), 4);
}

#[test]
fn content_type_bytes_unchanged_and_not_listed_as_key() {
    let (_dir, s) = setup();
    s.put("k", "payload".as_bytes(), "text/plain").expect("put");
    assert_eq!(read_key(&s, "k"), "payload");
    let objs = s.list("").expect("list");
    assert_eq!(objs.len(), 1);
    assert_eq!(objs[0].key, "k");
}

#[test]
fn content_type_survives_reopen() {
    let (dir, s) = setup();
    s.put("a/b.json", "{}".as_bytes(), "application/json")
        .expect("put");
    let s2 = LocalStore::new(dir.path()).expect("reopen");
    assert_eq!(types(&s2, "")["a/b.json"], "application/json");
}

#[test]
fn legacy_store_without_sidecar_lists_empty_type() {
    let (dir, s) = setup();
    fs::create_dir_all(dir.path().join("old")).expect("mkdir");
    fs::write(dir.path().join("old/f.txt"), b"hi").expect("write");
    let got = types(&s, "");
    assert_eq!(got.get("old/f.txt").map(String::as_str), Some(""));
}

#[test]
fn delete_removes_sidecar() {
    let (dir, s) = setup();
    s.put("a/b.txt", "x".as_bytes(), "text/plain").expect("put");
    assert!(dir.path().join(".blob-meta/a/b.txt.json").exists());
    s.delete("a/b.txt").expect("delete");
    let left = files_under(dir.path());
    assert!(left.is_empty(), "files left after delete: {left:?}");
}

#[test]
fn put_without_type_removes_stale_sidecar() {
    let (dir, s) = setup();
    s.put("k", "1".as_bytes(), "text/plain").expect("put");
    s.put("k", "2".as_bytes(), "").expect("put");
    assert_eq!(types(&s, "")["k"], "");
    assert!(!dir.path().join(".blob-meta/k.json").exists());
}

#[test]
fn corrupt_sidecar_is_empty_type_not_error() {
    let (dir, s) = setup();
    s.put("k", "1".as_bytes(), "text/plain").expect("put");
    fs::write(dir.path().join(".blob-meta/k.json"), b"{not json").expect("corrupt");
    assert_eq!(types(&s, "")["k"], "");
    assert_eq!(s.stat("k").expect("stat").content_type, "");
}

#[test]
fn sidecar_namespace_cannot_collide_with_keys() {
    let (_dir, s) = setup();
    for k in ["k.json", "k.json.json", "k"] {
        s.put(k, k.as_bytes(), &format!("text/{k}")).expect("put");
    }
    let got = types(&s, "");
    for k in ["k.json", "k.json.json", "k"] {
        assert_eq!(got[k], format!("text/{k}"));
    }
    assert_eq!(got.len(), 3);

    for key in [".blob-meta/x", ".blob-meta", "a/../.blob-meta/x"] {
        assert!(
            matches!(
                s.put(key, "x".as_bytes(), ""),
                Err(BlobError::ReservedKey(_))
            ),
            "put {key:?} must be rejected"
        );
        assert!(matches!(s.get(key), Err(BlobError::ReservedKey(_))));
        assert!(matches!(s.stat(key), Err(BlobError::ReservedKey(_))));
    }
    // Only the top-level segment is reserved.
    s.put("a/.blob-meta/x", "x".as_bytes(), "")
        .expect("nested ok");
}

/// Byte-compat with the Go backend: exact path, compact JSON shape.
#[test]
fn sidecar_layout_matches_go() {
    let (dir, s) = setup();
    s.put("a/b.txt", "x".as_bytes(), "text/plain").expect("put");
    let raw = fs::read_to_string(dir.path().join(".blob-meta/a/b.txt.json")).expect("sidecar");
    assert_eq!(raw, r#"{"content_type":"text/plain"}"#);
}

/// A sidecar written by the Go backend is readable here.
#[test]
fn reads_go_written_sidecar() {
    let (dir, s) = setup();
    fs::create_dir_all(dir.path().join("d")).expect("mkdir");
    fs::write(dir.path().join("d/k"), b"abc").expect("write");
    fs::create_dir_all(dir.path().join(".blob-meta/d")).expect("mkdir");
    fs::write(
        dir.path().join(".blob-meta/d/k.json"),
        br#"{"content_type":"text/x-go"}"#,
    )
    .expect("write sidecar");
    assert_eq!(types(&s, "")["d/k"], "text/x-go");
    assert_eq!(
        s.stat("d/k").expect("stat"),
        Object {
            key: "d/k".into(),
            size: 3,
            content_type: "text/x-go".into()
        }
    );
}

#[test]
fn stat_round_trip() {
    let (_dir, s) = setup();
    s.put("st/a.json", "{}".as_bytes(), "application/json")
        .expect("put");
    s.put("st/b.bin", "xyz".as_bytes(), "").expect("put");
    assert_eq!(
        s.stat("st/a.json").expect("stat"),
        Object {
            key: "st/a.json".into(),
            size: 2,
            content_type: "application/json".into()
        }
    );
    let b = s.stat("st/b.bin").expect("stat");
    assert_eq!((b.size, b.content_type.as_str()), (3, ""));
}

#[test]
fn stat_missing_and_after_delete_are_not_found() {
    let (_dir, s) = setup();
    assert!(matches!(s.stat("nope"), Err(BlobError::NotFound(_))));
    s.put("gone", "1".as_bytes(), "image/png").expect("put");
    s.delete("gone").expect("delete");
    assert!(matches!(s.stat("gone"), Err(BlobError::NotFound(_))));
    // A directory is not an object.
    s.put("dir/x", "1".as_bytes(), "").expect("put");
    assert!(matches!(s.stat("dir"), Err(BlobError::NotFound(_))));
}

#[test]
fn stat_overwrite_updates() {
    let (_dir, s) = setup();
    s.put("o", "1".as_bytes(), "text/plain").expect("put");
    s.put("o", "22".as_bytes(), "text/csv").expect("put");
    let o = s.stat("o").expect("stat");
    assert_eq!((o.size, o.content_type.as_str()), (2, "text/csv"));
    s.put("o", "333".as_bytes(), "").expect("put");
    let o = s.stat("o").expect("stat");
    assert_eq!((o.size, o.content_type.as_str()), (3, ""));
}

#[test]
fn stat_reads_sidecar_on_disk() {
    let (dir, s) = setup();
    s.put("k", "abc".as_bytes(), "text/x-a").expect("put");
    fs::write(
        dir.path().join(".blob-meta/k.json"),
        br#"{"content_type":"text/x-b"}"#,
    )
    .expect("rewrite");
    assert_eq!(s.stat("k").expect("stat").content_type, "text/x-b");
}

/// Minimal Store with no Stater.
struct ListOnly(Vec<Object>);

impl Store for ListOnly {
    type Reader = std::io::Empty;
    fn put<R: Read>(&self, _: &str, _: R, _: &str) -> Result<(), BlobError> {
        Ok(())
    }
    fn get(&self, _: &str) -> Result<Self::Reader, BlobError> {
        Ok(io::empty())
    }
    fn delete(&self, _: &str) -> Result<(), BlobError> {
        Ok(())
    }
    fn list(&self, _: &str) -> Result<Vec<Object>, BlobError> {
        Ok(self.0.clone())
    }
    fn exists(&self, _: &str) -> Result<bool, BlobError> {
        Ok(false)
    }
}

fn obj(key: &str, size: u64) -> Object {
    Object {
        key: key.into(),
        size,
        content_type: String::new(),
    }
}

#[test]
fn stat_by_list_picks_exact_key() {
    let s = ListOnly(vec![obj("k2", 9), obj("k", 1)]);
    let got = stat_by_list(&s, "k").expect("stat");
    assert_eq!((got.key.as_str(), got.size), ("k", 1));
}

#[test]
fn stat_by_list_missing_is_not_found() {
    let s = ListOnly(vec![obj("k2", 9)]);
    assert!(matches!(
        stat_by_list(&s, "k"),
        Err(BlobError::NotFound(k)) if k == "k"
    ));
}
