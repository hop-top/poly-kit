# blob

## What it answers

Where do opaque objects (files, artifacts, backups) go, behind a backend trait
with atomic writes? Structured records belong to `hop_top_kit::sqlstore`;
small keyed values to `hop_top_kit::kv`.

## Use it when

- a command stores or reads whole files under slash-separated keys → `LocalStore::new(dir)` then `put` / `get`
- a listing must never show a half-written object → `Store::list(prefix)` filters in-flight temp files
- code should not care which backend is behind it → take `impl blob::Store`
- one object's size and content type without listing → `Stater::stat(key)` (`LocalStore` implements it); a `Store` without it → `blob::stat_by_list(&store, key)`
- a sqlstore database needs an off-box copy → `sqlstore::backup::backup_to_blob` (feature `sqlstore-blob`)

## Quick start

```rust
use std::io::Read;

use hop_top_kit::blob::local::LocalStore;
use hop_top_kit::blob::Store;

let dir = tempfile::tempdir().unwrap();
let store = LocalStore::new(dir.path()).unwrap();

store.put("reports/2026.txt", "hello".as_bytes(), "text/plain").unwrap();

let mut body = String::new();
store.get("reports/2026.txt").unwrap().read_to_string(&mut body).unwrap();
assert_eq!(body, "hello");

let keys: Vec<String> = store.list("reports/").unwrap().into_iter().map(|o| o.key).collect();
assert_eq!(keys, vec!["reports/2026.txt".to_string()]);
```

## Contract

- Feature `blob` pulls in `thiserror` and `serde_json` (sidecar JSON). Authority: the crate [feature table](../../README.md#features).
- Only the local filesystem backend is ported; the Go S3 backend is out of scope for this crate.
- `put` stages in a sibling temp file, syncs, then renames over the destination: a concurrent `get`
  never sees a partial object and a failed write leaves the previous value intact.
- `put`'s content type is persisted in a sidecar `<root>/.blob-meta/<key>.json` holding
  `{"content_type":"..."}`, written (atomically) after the content; an empty type removes the old
  sidecar and `delete` removes both. `list` and `stat` report it as `Object::content_type`. A missing or
  corrupt sidecar reads as an empty type, never an error. The layout matches Go's local backend, so a
  store written by either is readable by the other (Go escapes `<`, `>`, `&` as `\u003c` etc.;
  Rust does not; both parse either).
- Keys whose first segment is `.blob-meta` (including via `..`) are refused with `BlobError::ReservedKey`;
  `list` never surfaces the directory.
- `Stater` is a separate, optional trait (not part of `Store`): `stat(key)` returns the same `Object`
  shape as `list`, with the exact content type of the latest `put`. A missing key (or a directory) is
  `BlobError::NotFound`. Rust cannot probe a trait at runtime, so unlike Go's `blob.Stat` there is no
  helper that prefers `Stater`: bound on `S: Stater`, or call `stat_by_list` (list-by-prefix plus
  exact-key match, content type only as good as `list`'s).
- A key that resolves outside the store root is refused with `BlobError::KeyEscapesRoot`.
- A missing key is `BlobError::NotFound`, not an empty reader.
- `list` orders per directory by name, as Go's walk does: `a/b` precedes `a.txt`.
- Parity: Go `go/storage/blob` is the reference; `tests/blob_conformance.rs` runs the shared corpus in
  `contracts/blob-v1`, and `tests/blob.rs` pins the atomic-write semantics described in its README.
  Keys that reduce to the root or contain `\` are refused here and not by Go; not in the corpus.
- Parity: `Delete` of a missing key is `NotFound` here and idempotent in Go; otherwise none recorded; Go `go/storage/blob` is the reference, and `tests/blob.rs` pins the

## Neighbours

- `hop_top_kit::sqlstore` (src/sqlstore/): typed JSON records, and the `backup_to_blob` caller
- `hop_top_kit::kv` (src/kv.rs): byte-keyed values with TTL, for anything small enough to query by key

## See also

- Crate README, [blob writes are atomic](../../../../../docs/adopters/reference/rs-sdk.md#blob-writes-are-atomic)
- [`go/storage/blob`](../../../../../go/storage/blob/README.md), the Go reference
