# How kit storage is layered

Concept page. Explains the five storage layers kit provides, how
they compose, and which one fits which access pattern.

## Who this is for

Authors choosing where to persist data inside a kit-based tool.

> **Looking for a decision table you can act on?** Use
> [Choose the right abstraction](#choose-the-right-abstraction)
> below. A standalone decision page (`docs/choose-storage-abstraction.md`)
> is tracked as a follow-up.

## Layers at a glance

| Layer | Interface                          | Use when                          |
|-------|-------------------------------------|-----------------------------------|
| 1     | `kv.Store`                          | Raw bytes by key                  |
| 2     | `blob.Store`                        | Files / large objects / backups   |
| 3     | `sqldb.Open()`                      | Direct SQL against local DB       |
| 4     | `secret.Store` / `MutableStore`     | Credentials, tokens, API keys     |
| 5     | `domain.Repository[T]`              | Typed CRUD on domain objects      |

## Layer detail

### 1. `kv.Store` — key-value

Interface: `Put` / `Get` / `Delete` / `List` / `Close`.

- Values are `[]byte`; caller serialises.
- Optional `TTLStore` extension for expiration.
- Backend selection via factory; importing a driver registers its name,
  so a binary only links the backends it opens.

```go
import (
    "hop.top/kit/go/storage/kv"
    _ "hop.top/kit/go/storage/kv/sqlite"
)

store, err := kv.Open(kv.Config{
    Backend: "sqlite",     // sqlite | badger | etcd | tidb
    Path:    "cache.db",   // sqlite file / badger directory
})
```

Config carries only the fields a given backend reads: `Path` for
sqlite and badger, `Endpoints` plus `Prefix` for etcd, `DSN` plus
`Table` for tidb. Opening a backend whose driver was not imported
reports the package to import.

Use when: caching, session state, config persistence, sync queue.

#### Keys bind as TEXT

The SQLite driver declares `key TEXT PRIMARY KEY`, and every implementation
must bind keys as TEXT rather than BLOB. This is a correctness requirement,
not a style preference.

SQLite treats TEXT and BLOB as distinct storage classes and compares
storage class before value, so a key written as a BLOB never equals the
same bytes written as TEXT. Nothing raises an error when this goes wrong.
Instead, reads become silent misses, `INSERT OR REPLACE` writes a shadow
row beside the one it should have replaced, and prefix scans return
disjoint sets.

Two consequences are easy to miss:

- **The column declaration is not what carries the contract; the bind type
  is.** The table is created with `CREATE TABLE IF NOT EXISTS`, so whichever
  process opens the file first wins and any other implementation's
  declaration is inert. Declaring `TEXT` proves nothing about what a peer
  actually binds.
- **Keys are arbitrary byte sequences.** Go models them as `string`, which
  admits bytes that are not valid UTF-8. A port whose string type cannot
  hold those bytes must take a byte slice and bind it as TEXT without UTF-8
  validation rather than reaching for BLOB.

TEXT also gives the ordering callers rely on: the default `BINARY`
collation is `memcmp` over stored bytes, which matches Go string
comparison, so ordered scans agree across languages even for non-UTF-8
keys. Note that `List` itself issues no `ORDER BY`; its result is a set.

A test suite that round-trips within a single language cannot catch a
binding mismatch, because both sides agree with themselves. The gate that
crosses the boundary is driven from the shared corpus in
[`contracts/kv-v1/keys.json`](../../../contracts/kv-v1/keys.json):
`go/storage/kv/sqlite/crosslang_test.go` writes the corpus and has the Rust
implementation read it back, and vice versa. It needs both toolchains, so
it runs in the parity job (`make test-parity-kv`, which sets
`KV_CROSSLANG=1`) rather than in `go test ./...`. The remaining cases in
that file, including the one pinning the key column's storage class,
always run.

### 2. `blob.Store` — object/blob

Interface: `Put` / `Get` / `Delete` / `List` / `Exists`.

- Streaming via `io.Reader` / `io.ReadCloser`; no full-buffer
  requirement.
- Adapters: `blob/local` (filesystem), `blob/s3` (AWS S3).
- Serves as destination for automated backups via the backup
  scheduler.
- `blob/local` writes atomically: contents are staged in a temp file and
  renamed into place, so a concurrent `Get` never sees a partial blob and
  a failed write leaves the previous value intact.

Use when: file storage, backups, large payloads, media.

### 3. `sqldb` — shared SQLite connection

`sqldb.Open()` — shared connection management (not an interface).

- Opens with standard pragmas (WAL, busy_timeout, foreign_keys).
- Migration helper included.
- Used by `domain/sqlite`, `store` (kit serve), `core/upgrade`,
  `runtime/bus/sqlite`.

Use when: any package needs raw SQL against the local database.

### 4. `secret.Store` — secrets

Read-only interface: `Get` / `List` / `Exists`. Extended
`MutableStore` adds `Set` / `Delete`.

- Values are `*Secret` with `Key`, `Value []byte`, `Metadata`.
- Optional `Keeper` interface for encryption at rest.
- Backend selection via factory.

```go
store, err := secret.Open(secret.Config{
    Backend: "env",      // env | file | keyring | openbao
    Prefix:  "MYAPP_",   //   | infisical | memory
})
```

Use when: credentials, API keys, tokens, any sensitive value.
See [Secret Management Guide](../guides/secret-management-guide.md).

### 5. `domain.Repository[T]` — typed entities

Generic CRUD: `Create` / `Get` / `List` / `Update` / `Delete`.

- Typed entity operations with validation/auditing.
- Backed by `sqldb` under the hood (via `domain/sqlite`).

Use when: CRUD on domain objects with schema enforcement.

## How they compose

```
App code
  │ uses
domain.Repository[T]  ◄─ typed CRUD (highest level)
  │ backed by
sqldb.Open()          ◄─ shared SQLite connection

kv.Open(Config)       ◄─ raw key-value (mid level)
  │ dispatches to
kv/sqlite, kv/badger, kv/etcd, kv/tidb

blob.Store            ◄─ object storage (files/backups)
  │ adapters
blob/local, blob/s3

secret.Open(Config)   ◄─ credentials / sensitive values
  │ dispatches to
secret/env, secret/file, secret/keyring,
secret/openbao, secret/infisical, secret/memory

store.DocumentStore   ◄─ kit serve's generic JSON store
  │ backed by
sqldb.Open()
```

## Choose the right abstraction

| Need                          | Use                    |
|-------------------------------|------------------------|
| Typed CRUD with validation    | `domain.Repository[T]` |
| Raw bytes by key              | `kv.Open(Config)`      |
| Files / large objects         | `blob.Store`           |
| Automated backups             | `blob.Store` as dest   |
| Credentials / API keys        | `secret.Open(Config)`  |
| Generic JSON documents        | `store.DocumentStore`  |
| Raw SQL (local)               | `sqldb.Open()` direct  |

## Network policy in the kv backends

`kv.OpenContext` consults the offline marker before a network driver
dials; `kv.Open` keeps its signature and supplies a background context,
so it connects without consulting the policy. Prefer `OpenContext`
wherever a context is at hand.

A third-party driver registered through the older `kv.Opener` still
works, because `OpenContext` falls back to it, but it connects
unpoliced. Register through `kv.RegisterBackendContext` to be covered;
all four shipped drivers do.

The two network drivers reach the policy by different seams:

- `tidb` routes the MySQL driver's `Config.DialFunc` through
  `netpolicy.GuardDial`.
- `etcd` cannot use that seam, because gRPC dials on its own background
  context so the marker never arrives, and `clientv3.New` returns before
  connecting at all. Its endpoints are checked with `netpolicy.CheckDial`
  at open time.

Registration is by import rather than by build tag, so a binary carries
only the dependencies of the backends it opens.

## Reference: package list

| Package           | Type   | Backend                          |
|-------------------|--------|----------------------------------|
| `blob/local`      | blob   | Local filesystem                 |
| `blob/s3`         | blob   | AWS S3                           |
| `kv/badger`       | kv     | Embedded Badger (high-throughput)|
| `kv/etcd`         | kv     | Distributed etcd cluster         |
| `kv/sqlite`       | kv     | Embedded SQLite (default)        |
| `kv/tidb`         | kv     | TiDB / MySQL-compatible          |
| `secret/env`      | secret | Environment variables            |
| `secret/file`     | secret | Encrypted files on disk          |
| `secret/infisical`| secret | Infisical cloud/self-hosted      |
| `secret/keyring`  | secret | OS keychain                      |
| `secret/memory`   | secret | In-memory (testing)              |
| `secret/openbao`  | secret | OpenBao / Vault                  |

## Related pages

- [`secret-management-guide.md`](../guides/secret-management-guide.md) — secret backend recipes
- [`architecture.md`](../../contributors/architecture/architecture.md) — full package map
- `docs/choose-storage-abstraction.md` *(planned decision page)*
