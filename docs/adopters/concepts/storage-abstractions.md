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
- Backend selection via factory.

```go
store, err := kv.Open(kv.Config{
    Backend: "sqlite",   // sqlite | badger | etcd | tidb
    DSN:     "path.db",  // backend-specific connection string
    Table:   "kv",       // table/bucket name (where applicable)
})
```

Use when: caching, session state, config persistence, sync queue.

### 2. `blob.Store` — object/blob

Interface: `Put` / `Get` / `Delete` / `List` / `Exists`.

- Streaming via `io.Reader` / `io.ReadCloser`; no full-buffer
  requirement.
- Adapters: `blob/local` (filesystem), `blob/s3` (AWS S3).
- Serves as destination for automated backups via the backup
  scheduler.

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

## Reference: package list

| Package           | Type   | Backend                          |
|-------------------|--------|----------------------------------|
| `kv/sqlite`       | kv     | Embedded SQLite (default)        |
| `kv/badger`       | kv     | Embedded Badger (high-throughput)|
| `kv/etcd`         | kv     | Distributed etcd cluster         |
| `kv/tidb`         | kv     | TiDB / MySQL-compatible          |
| `blob/local`      | blob   | Local filesystem                 |
| `blob/s3`         | blob   | AWS S3                           |
| `secret/env`      | secret | Environment variables            |
| `secret/file`     | secret | Encrypted files on disk          |
| `secret/keyring`  | secret | OS keychain                      |
| `secret/openbao`  | secret | OpenBao / Vault                  |
| `secret/infisical`| secret | Infisical cloud/self-hosted      |
| `secret/memory`   | secret | In-memory (testing)              |

## Related pages

- [`secret-management-guide.md`](../guides/secret-management-guide.md) — secret backend recipes
- [`architecture.md`](../../contributors/architecture/architecture.md) — full package map
- `docs/choose-storage-abstraction.md` *(planned decision page)*
