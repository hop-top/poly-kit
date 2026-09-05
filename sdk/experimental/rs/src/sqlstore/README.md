# sqlstore

## What it answers

Where does a CLI keep typed JSON records locally, with numbered migrations,
file or blob backup, and optional at-rest encryption? Byte-keyed values with
TTL belong to `hop_top_kit::kv`; raw connections and pragmas to
`hop_top_kit::sqldb`.

## Use it when

- a command needs to persist a serde type by string key → `Store::open` then `put` / `get`
- records must expire after a window → `Options::new().with_ttl(..)`
- the app owns extra tables or seed rows → `Options::with_migrate_sql(..)`, applied once at version 1000
- a migration must be reversible → `backup::backup_before_migrate` writes a timestamped copy to `.dbs/`
- the backup must leave the host → `backup::backup_to_blob` / `restore_from_blob` (feature `sqlstore-blob`)
- values must be unreadable at rest → `EncryptedStore::from_seed` (feature `sqlstore-encrypt`)

## Quick start

```rust
use hop_top_kit::sqlstore::{Options, Store};

let dir = tempfile::tempdir().unwrap();
let path = dir.path().join("app.db").to_string_lossy().into_owned();
let store = Store::open(path, Options::new()).unwrap();

store.put("last-run", &vec!["build", "test"]).unwrap();
let got: Option<Vec<String>> = store.get("last-run").unwrap();
assert_eq!(got.as_deref(), Some(&["build".to_string(), "test".to_string()][..]));
```

## Contract

- Feature `sqlstore` pulls in `sqldb`, `serde`, `serde_json`; `sqlstore-blob` adds `blob`;
  `sqlstore-encrypt` adds `crypto_secretbox`, `hkdf`, `sha2`. Authority: the crate
  [feature table](../../README.md#features).
- Keys are plain strings, no namespacing; the caller owns uniqueness.
- The `kv` table is migration version 1; caller SQL lands at `USER_MIGRATION_VERSION` (1000)
  and runs exactly once per database inside a transaction. Editing it later does not re-run it,
  unlike Go's run-every-open `MigrateSQL`.
- `stored_at` has second precision, so a TTL under one second cannot be observed.
- Expired rows are hidden by `get`, not deleted.
- Encryption: HKDF-SHA256 over a 32-byte Ed25519 seed, then NaCl secretbox with a 24-byte nonce.
  Pass the seed, never the 64-byte expanded key. Info string `kit-identity-encryption-v1`.
- Parity: [`contracts/identity-v1/derive-key.json`](../../../../../contracts/identity-v1/derive-key.json),
  asserted by `tests/sqlstore.rs`; Go `go/storage/sqlstore` is the reference for store behaviour.

## Neighbours

- `hop_top_kit::sqldb` (src/sqldb.rs): connection open, pragmas, the migration runner this store uses
- `hop_top_kit::kv` (src/kv.rs): byte-keyed `Store` / `TtlStore` traits, the layer `httpcache` sits on
- `hop_top_kit::blob` (src/blob/): the backend `backup_to_blob` writes through

## See also

- Crate README, [Storage](../../README.md#storage) and
  [At-rest encryption](../../README.md#at-rest-encryption)
- [`docs/adopters/guides/encrypt-engine-data.md`](../../../../../docs/adopters/guides/encrypt-engine-data.md)
- [`go/storage/sqlstore`](../../../../../go/storage/sqlstore/) and
  [`go/core/identity`](../../../../../go/core/identity/README.md), the Go references
