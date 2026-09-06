# blob

## What it answers

Where do opaque objects (files, artifacts, backups) go, behind a backend trait
with atomic writes? Structured records belong to `hop_top_kit::sqlstore`;
small keyed values to `hop_top_kit::kv`.

## Use it when

- a command stores or reads whole files under slash-separated keys → `LocalStore::new(dir)` then `put` / `get`
- a listing must never show a half-written object → `Store::list(prefix)` filters in-flight temp files
- code should not care which backend is behind it → take `impl blob::Store`
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

- Feature `blob` pulls in `thiserror` only. Authority: the crate [feature table](../../README.md#features).
- Only the local filesystem backend is ported; the Go S3 backend is out of scope for this crate.
- `put` stages in a sibling temp file, syncs, then renames over the destination: a concurrent `get`
  never sees a partial object and a failed write leaves the previous value intact.
- A key that resolves outside the store root is refused with `BlobError::KeyEscapesRoot`.
- A missing key is `BlobError::NotFound`, not an empty reader.
- Parity: none recorded; Go `go/storage/blob` is the reference, and `tests/blob.rs` pins the
  atomic-write semantics described in its README.

## Neighbours

- `hop_top_kit::sqlstore` (src/sqlstore/): typed JSON records, and the `backup_to_blob` caller
- `hop_top_kit::kv` (src/kv.rs): byte-keyed values with TTL, for anything small enough to query by key

## See also

- Crate README, [blob writes are atomic](../../../../../docs/adopters/reference/rs-sdk.md#blob-writes-are-atomic)
- [`go/storage/blob`](../../../../../go/storage/blob/README.md), the Go reference
