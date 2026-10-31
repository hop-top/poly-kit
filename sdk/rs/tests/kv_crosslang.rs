//! Cross-language storage-binding gate for the kv SQLite backend.
//!
//! The Rust suite round-trips Rust-to-Rust and the Go suite Go-to-Go, so
//! a key-affinity mismatch passes both by construction. This file is the
//! Rust half of a test that actually crosses the boundary, driven from
//! the shared corpus in `contracts/kv-v1/keys.json`.
//!
//! Two entry points:
//!
//! * `harness_write` / `harness_read` operate on the file named by
//!   `KV_CROSSLANG_DB` and are invoked as a subprocess by the Go test in
//!   `go/storage/kv/sqlite/crosslang_test.go`. Without that variable they
//!   skip, so they are inert under a plain `cargo test`.
//! * The remaining tests are Rust-only and always run: they pin the
//!   storage class, the BINARY collation ordering and the corpus
//!   round-trip without needing a Go toolchain.
#![cfg(feature = "kv")]

use std::collections::BTreeSet;
use std::env;
use std::fs;
use std::path::{Path, PathBuf};

use hop_top_kit::kv::{SqliteStore, Store};
use rusqlite::Connection;
use serde::Deserialize;

#[derive(Debug, Deserialize)]
struct Contract {
    #[allow(dead_code)]
    version: String,
    cases: Vec<Case>,
    list_order: Vec<String>,
    prefix_scans: Vec<PrefixScan>,
}

#[derive(Debug, Deserialize)]
struct Case {
    name: String,
    key_hex: String,
    value_hex: String,
    #[allow(dead_code)]
    note: String,
}

#[derive(Debug, Deserialize)]
struct PrefixScan {
    name: String,
    prefix_hex: String,
    expect_hex: Vec<String>,
    note: String,
}

/// Decodes a hex string from the fixture.
fn unhex(s: &str) -> Vec<u8> {
    assert!(s.len().is_multiple_of(2), "odd-length hex: {s:?}");
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("hex digit"))
        .collect()
}

fn hex(b: &[u8]) -> String {
    b.iter().map(|x| format!("{x:02x}")).collect()
}

/// Walks up from the crate manifest to the tree holding `contracts/`.
fn locate_contract() -> PathBuf {
    let manifest = env!("CARGO_MANIFEST_DIR");
    let mut dir: &Path = Path::new(manifest);
    for _ in 0..10 {
        let candidate = dir.join("contracts").join("kv-v1").join("keys.json");
        if candidate.exists() {
            return candidate;
        }
        match dir.parent() {
            Some(parent) => dir = parent,
            None => break,
        }
    }
    panic!("contracts/kv-v1/keys.json: not found walking up from {manifest}");
}

fn load() -> Contract {
    let path = locate_contract();
    let raw = fs::read_to_string(&path).unwrap_or_else(|e| panic!("read {}: {e}", path.display()));
    serde_json::from_str(&raw).unwrap_or_else(|e| panic!("parse {}: {e}", path.display()))
}

/// Writes every fixture case into the store at `path`.
fn write_corpus(path: &str, c: &Contract) {
    let store = SqliteStore::new(path).expect("open store for write");
    for case in &c.cases {
        store
            .put(&unhex(&case.key_hex), &unhex(&case.value_hex))
            .unwrap_or_else(|e| panic!("put {}: {e}", case.name));
    }
}

/// Asserts the store at `path` holds exactly the fixture corpus.
fn read_corpus(path: &str, c: &Contract) {
    let store = SqliteStore::new(path).expect("open store for read");

    for case in &c.cases {
        let key = unhex(&case.key_hex);
        let want = unhex(&case.value_hex);
        let got = store
            .get(&key)
            .unwrap_or_else(|e| panic!("get {}: {e}", case.name));
        assert_eq!(
            got,
            Some(want),
            "get {} ({}): a storage-class mismatch reads as a silent miss, not an error",
            case.name,
            case.key_hex,
        );
    }

    // `list` issues no ORDER BY, so its row order is unspecified — compare
    // as a set. Collation order is pinned separately against an explicitly
    // ordered query in `binary_collation_order`.
    for scan in &c.prefix_scans {
        let got: BTreeSet<String> = store
            .list(&unhex(&scan.prefix_hex))
            .unwrap_or_else(|e| panic!("list {}: {e}", scan.name))
            .iter()
            .map(|k| hex(k))
            .collect();
        let want: BTreeSet<String> = scan.expect_hex.iter().cloned().collect();
        assert_eq!(got, want, "prefix scan {}: {}", scan.name, scan.note);
    }
}

/// Every key in the store at `path` under an explicit `ORDER BY key`.
fn sorted_key_hex(path: &str) -> Vec<String> {
    let conn = Connection::open(path).expect("open raw");
    let mut stmt = conn.prepare("SELECT key FROM kv ORDER BY key").unwrap();
    let rows = stmt
        .query_map([], |row| {
            row.get_ref(0)
                .map(|v| hex(v.as_bytes().expect("key column must be TEXT or BLOB")))
        })
        .unwrap();
    rows.collect::<Result<Vec<_>, _>>().unwrap()
}

/// Path handed over by the Go driver, or `None` under a plain `cargo test`.
fn driver_db() -> Option<String> {
    env::var("KV_CROSSLANG_DB").ok().filter(|s| !s.is_empty())
}

/// Rust half of "Go writes, Rust reads". Driven by the Go test.
#[test]
fn harness_read() {
    let Some(path) = driver_db() else {
        eprintln!("KV_CROSSLANG_DB unset: skipping (driven by the Go cross-language test)");
        return;
    };
    let c = load();
    read_corpus(&path, &c);

    // The file was written by Go; the storage class must still be TEXT.
    let conn = Connection::open(&path).expect("open raw");
    let ty: String = conn
        .query_row("SELECT DISTINCT typeof(key) FROM kv", [], |r| r.get(0))
        .expect("typeof(key)");
    assert_eq!(ty, "text", "Go-written keys must read back as TEXT");
}

/// Rust half of "Rust writes, Go reads". Driven by the Go test.
#[test]
fn harness_write() {
    let Some(path) = driver_db() else {
        eprintln!("KV_CROSSLANG_DB unset: skipping (driven by the Go cross-language test)");
        return;
    };
    let c = load();
    write_corpus(&path, &c);

    // Fail fast here rather than leaving the Go reader to report it.
    read_corpus(&path, &c);
}

/// Control: same corpus and assertions, never leaving Rust. A failure
/// here means the fixture or the Rust store is wrong, not that the two
/// languages disagree.
#[test]
fn rust_writes_rust_reads() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("kv.db");
    let path = path.to_str().unwrap();

    let c = load();
    write_corpus(path, &c);
    read_corpus(path, &c);
}

/// Pins the storage class. The corpus round-trips within one language
/// under either affinity; only the declared type keeps the other
/// language's binding compatible.
#[test]
fn key_column_is_text() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("kv.db");
    let path = path.to_str().unwrap();

    let c = load();
    write_corpus(path, &c);

    let conn = Connection::open(path).expect("open raw");
    let mut stmt = conn.prepare("SELECT DISTINCT typeof(key) FROM kv").unwrap();
    let types: Vec<String> = stmt
        .query_map([], |r| r.get::<_, String>(0))
        .unwrap()
        .collect::<Result<_, _>>()
        .unwrap();

    assert_eq!(
        types,
        vec!["text".to_string()],
        "every kv key must be stored with SQLite storage class TEXT",
    );

    // And the declared column type, which is what `CREATE TABLE IF NOT
    // EXISTS` locks in for whichever language opens the file second.
    let decl: String = conn
        .query_row(
            "SELECT type FROM pragma_table_info('kv') WHERE name = 'key'",
            [],
            |r| r.get(0),
        )
        .expect("pragma_table_info");
    assert_eq!(decl, "TEXT", "kv.key must be declared TEXT to match Go");
}

/// Proves the assumption prefix scans rest on: under a TEXT key column,
/// the default BINARY collation is still memcmp over the stored bytes.
/// If it were UTF-8-aware or locale-sensitive, `key >= ? AND key < ?`
/// would select the wrong rows for every non-ASCII key.
#[test]
fn binary_collation_order() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("kv.db");
    let path = path.to_str().unwrap();

    let c = load();
    write_corpus(path, &c);

    assert_eq!(
        sorted_key_hex(path),
        c.list_order,
        "ORDER BY key must equal byte-wise (memcmp) order over the corpus",
    );

    // Derive the same expectation independently, so the fixture cannot
    // drift into agreeing with a wrong implementation.
    let mut want: Vec<Vec<u8>> = c.cases.iter().map(|x| unhex(&x.key_hex)).collect();
    want.sort();
    let want: Vec<String> = want.iter().map(|k| hex(k)).collect();
    assert_eq!(
        want, c.list_order,
        "fixture list_order must be the corpus sorted by byte order",
    );
}
