//! Shared conformance suite for [`Store`] backends, a port of Go's
//! `go/storage/blob/blobtest`. Every backend's tests call [`run`] (and the
//! optional [`run_content_type`] / [`run_stat`]) with a factory for a fresh,
//! empty store; the suite pins what callers may rely on across backends.
//!
//! Pinned contract:
//! - `delete` of a missing key succeeds (idempotent, S3 semantics).
//! - `exists` is `false`, without error, for a missing key.
//! - `get` of a missing key is `BlobError::NotFound`.
//! - `put` overwrites; `get` returns the latest bytes.
//! - `list` returns every key with the prefix (a string-prefix match, so
//!   `"logs/"` excludes `"logs-old/x"`), with its size, as the keys the
//!   caller wrote.
//! - `Object::content_type` from `list` is never wrong: empty (the backend
//!   cannot report it, e.g. S3 `ListObjectsV2`) or exactly the type of the
//!   latest `put`. A stale type from an overwritten or deleted key never
//!   leaks. Backends that can report it also run [`run_content_type`].
//!
//! Rust has no `t.Run`, so a failed case panics with its name prefixed.

#![allow(dead_code)] // each test crate runs a subset

use std::collections::BTreeMap;
use std::io::Read;

use hop_top_kit::blob::{BlobError, Object, Stater, Store};

/// A fresh store plus whatever must outlive it (a temp dir, say).
pub struct Fixture<S> {
    pub store: S,
    pub _hold: Box<dyn std::any::Any>,
}

impl<S> Fixture<S> {
    pub fn new(store: S) -> Self {
        Fixture {
            store,
            _hold: Box::new(()),
        }
    }

    pub fn holding(store: S, hold: impl std::any::Any) -> Self {
        Fixture {
            store,
            _hold: Box::new(hold),
        }
    }
}

/// Builds an empty store isolated from every other call. The argument names
/// the case, so a backend can derive a stable, unique key prefix from it.
pub trait Factory<S>: Fn(&str) -> Fixture<S> {}
impl<S, F: Fn(&str) -> Fixture<S>> Factory<S> for F {}

fn case<S, F: Factory<S>>(new: &F, name: &str, body: impl FnOnce(&S)) {
    let fx = new(name);
    let r = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| body(&fx.store)));
    if let Err(p) = r {
        eprintln!("blobtest case failed: {name}");
        std::panic::resume_unwind(p);
    }
}

fn put<S: Store>(s: &S, key: &str, body: &str) {
    put_ct(s, key, body, "text/plain");
}

fn put_ct<S: Store>(s: &S, key: &str, body: &str, ct: &str) {
    s.put(key, body.as_bytes(), ct)
        .unwrap_or_else(|e| panic!("put {key}: {e}"));
}

fn read<S: Store>(s: &S, key: &str) -> String {
    let mut out = String::new();
    s.get(key)
        .unwrap_or_else(|e| panic!("get {key}: {e}"))
        .read_to_string(&mut out)
        .unwrap();
    out
}

fn list_types<S: Store>(s: &S) -> BTreeMap<String, String> {
    s.list("ct/")
        .expect("list ct/")
        .into_iter()
        .map(|o| (o.key, o.content_type))
        .collect()
}

const WANT_TYPES: [(&str, &str); 4] = [
    ("ct/a.json", "application/json"),
    ("ct/b.bin", ""),
    ("ct/over", "text/csv"),
    ("ct/gone", ""),
];

/// Writes the keys [`WANT_TYPES`] describes, in the order that exercises
/// overwrite, delete and re-create.
fn write_typed_keys<S: Store>(s: &S) {
    put_ct(s, "ct/a.json", "{}", "application/json");
    put_ct(s, "ct/b.bin", "x", "");
    put_ct(s, "ct/over", "1", "text/plain");
    put_ct(s, "ct/over", "2", "text/csv");
    put_ct(s, "ct/gone", "1", "image/png");
    s.delete("ct/gone").expect("delete ct/gone");
    put_ct(s, "ct/gone", "1", "");
}

/// The core suite every backend passes.
pub fn run<S: Store, F: Factory<S>>(new: F) {
    case(&new, "DeleteMissingKeySucceeds", |s| {
        s.delete("never/existed.txt")
            .expect("delete of missing key");
    });

    case(&new, "DeleteTwiceSucceeds", |s| {
        put(s, "d.txt", "x");
        s.delete("d.txt").expect("first delete");
        s.delete("d.txt").expect("second delete");
        assert!(!s.exists("d.txt").expect("exists after delete"));
    });

    case(&new, "ExistsMissing", |s| {
        assert!(!s.exists("nope").expect("exists(missing) must not error"));
    });

    case(&new, "GetMissingErrors", |s| {
        assert!(matches!(s.get("nope"), Err(BlobError::NotFound(k)) if k == "nope"));
    });

    case(&new, "PutGetOverwrite", |s| {
        put(s, "a/b.txt", "one");
        put(s, "a/b.txt", "two!");
        assert_eq!(read(s, "a/b.txt"), "two!", "want latest bytes");
    });

    case(&new, "ListContentTypeNeverWrong", |s| {
        write_typed_keys(s);
        let got = list_types(s);
        for (k, want) in WANT_TYPES {
            let g = got
                .get(k)
                .unwrap_or_else(|| panic!("list missing {k}: {got:?}"));
            assert!(
                g.is_empty() || g == want,
                "List ContentType[{k}] = {g:?}, want {want:?} or empty"
            );
        }
    });

    case(&new, "ListPrefixKeysAndSizes", |s| {
        put(s, "logs/a.log", "1");
        put(s, "logs/sub/b.log", "22");
        put(s, "data/x.bin", "333");
        put(s, "logs-old/c.log", "4444"); // shares "logs" but not "logs/"
        let mut objs = s.list("logs/").expect("list");
        objs.sort_by(|a, b| a.key.cmp(&b.key));
        let keys: Vec<&str> = objs.iter().map(|o| o.key.as_str()).collect();
        assert_eq!(keys, ["logs/a.log", "logs/sub/b.log"]);
        assert_eq!(objs[0].size, 1);
        assert_eq!(objs[1].size, 2);
    });
}

/// Strict contract for backends that persist the content type: `list`
/// reports exactly the type of the latest `put`, empty when none was given,
/// and the type does not outlive its key.
pub fn run_content_type<S: Store, F: Factory<S>>(new: F) {
    case(&new, "ContentTypeRoundTrip", |s| {
        write_typed_keys(s);
        let got = list_types(s);
        for (k, want) in WANT_TYPES {
            assert_eq!(
                got.get(k).map(String::as_str),
                Some(want),
                "ContentType[{k}]"
            );
        }
    });

    case(&new, "ContentTypeBytesUnchanged", |s| {
        put_ct(s, "k", "payload", "text/plain");
        assert_eq!(read(s, "k"), "payload");
    });

    case(&new, "ContentTypeNotListedAsKey", |s| {
        put_ct(s, "k", "p", "text/plain");
        let objs = s.list("").expect("list");
        assert_eq!(
            objs.iter().map(|o| o.key.as_str()).collect::<Vec<_>>(),
            ["k"],
            "list = {objs:?}, want only k"
        );
    });
}

/// "No type was given": empty, or the default a backend substitutes (S3
/// serves binary/octet-stream).
fn untyped(ct: &str) -> bool {
    matches!(ct, "" | "binary/octet-stream" | "application/octet-stream")
}

/// The optional [`Stater`] contract:
/// - `stat` returns key, size and the exact content type of the latest
///   `put` (empty, or the backend's default, when none was given).
/// - `stat` of a missing key is `NotFound`, as `get` is.
/// - Overwrite updates type and size; delete makes `stat` fail again.
pub fn run_stat<S: Store + Stater, F: Factory<S>>(new: F) {
    case(&new, "StatRoundTrip", |s| {
        put_ct(s, "st/a.json", "{}", "application/json");
        put_ct(s, "st/b.bin", "xyz", "");
        let got = s.stat("st/a.json").expect("stat a.json");
        assert_eq!(
            got,
            Object {
                key: "st/a.json".into(),
                size: 2,
                content_type: "application/json".into()
            }
        );
        let got = s.stat("st/b.bin").expect("stat b.bin");
        assert!(
            got.key == "st/b.bin" && got.size == 3 && untyped(&got.content_type),
            "Stat(no type) = {got:?}, want size 3 and no type"
        );
    });

    case(&new, "StatMissingErrors", |s| {
        assert!(matches!(s.stat("st/nope"), Err(BlobError::NotFound(k)) if k == "st/nope"));
    });

    case(&new, "StatOverwriteUpdates", |s| {
        put_ct(s, "st/over", "1", "text/plain");
        put_ct(s, "st/over", "22", "text/csv");
        let got = s.stat("st/over").expect("stat");
        assert!(
            got.content_type == "text/csv" && got.size == 2,
            "Stat after overwrite = {got:?}"
        );
        put_ct(s, "st/over", "333", "");
        let got = s.stat("st/over").expect("stat");
        assert!(
            untyped(&got.content_type) && got.size == 3,
            "Stat after typeless overwrite = {got:?}"
        );
    });

    case(&new, "StatAfterDeleteErrors", |s| {
        put_ct(s, "st/gone", "1", "image/png");
        s.delete("st/gone").expect("delete");
        assert!(matches!(s.stat("st/gone"), Err(BlobError::NotFound(_))));
    });
}
