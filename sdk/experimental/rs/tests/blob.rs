#![cfg(feature = "blob")]

use std::fs;
use std::io::{self, Read};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use hop_top_kit::blob::local::LocalStore;
use hop_top_kit::blob::{BlobError, Store};
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
