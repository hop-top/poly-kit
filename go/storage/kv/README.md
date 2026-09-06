# kv

## What it answers

Where do raw bytes go when the key is the only index you need? `kv.Store`
is `Put`/`Get`/`Delete`/`List`/`Close` over `[]byte`; `kv.TTLStore` adds
`PutWithTTL`. Wrong package when you need SQL (`go/storage/sqldb`), large
streamed objects (`go/storage/blob`) or credentials (`go/storage/secret`).

## Use it when

- a local cache or session table, one file, no server: `sqlite`
- write-heavy local state with native expiry: `badger`
- state shared by several processes across hosts: `etcd` or `tidb`
- you hold a context: call `kv.OpenContext`, which honours `--offline`

| Backend | Import path | Config fields | TTL | Pick it when |
|---------|-------------|---------------|-----|--------------|
| [`sqlite`](sqlite/README.md) | `hop.top/kit/go/storage/kv/sqlite` | `Path` (file) | yes | default; one file, cross-language readable |
| [`badger`](badger/README.md) | `hop.top/kit/go/storage/kv/badger` | `Path` (dir) | yes | high write throughput, Go-only readers |
| [`etcd`](etcd/README.md) | `hop.top/kit/go/storage/kv/etcd` | `Endpoints`, `Prefix` | no | coordination data on an existing cluster |
| [`tidb`](tidb/README.md) | `hop.top/kit/go/storage/kv/tidb` | `DSN`, `Table` (default `kv`) | no | MySQL-compatible server already provisioned |

[`registry/`](registry/README.md) holds the test proving what `Open` says
when no driver is imported.

## Quick start

```go
dir, _ := os.MkdirTemp("", "kv")
defer os.RemoveAll(dir)

store, err := kv.OpenContext(context.Background(), kv.Config{
	Backend: "sqlite",
	Path:    filepath.Join(dir, "cache.db"),
})
if err != nil {
	panic(err)
}
defer store.Close()

ctx := context.Background()
_ = store.Put(ctx, "greeting", []byte("hello"))
v, ok, _ := store.Get(ctx, "greeting")
fmt.Println(string(v), ok)
// Output: hello true
```

Needs `_ "hop.top/kit/go/storage/kv/sqlite"` in the imports; see
[`example_test.go`](example_test.go).

## Contract

- Registration is by blank import, from each driver's `init`. Naming a
  backend whose driver is absent returns the import path to add;
  `kv.Backends()` lists what the binary carries. No build tags.
- `Config.Backend` is required. Each driver rejects a Config missing its
  own fields (`Path`, `Endpoints`, `DSN`).
- `OpenContext` refuses a remote `tidb` or `etcd` under an offline context;
  loopback, unix sockets and file backends stay reachable. `Open` supplies
  a background context and connects unpoliced.
- Keys bind as TEXT in SQLite, never BLOB; a BLOB-bound key silently misses
  a TEXT-bound one. Corpus:
  [`contracts/kv-v1/keys.json`](../../../contracts/kv-v1/keys.json). The
  Go/Rust cross-process gate is `make test-parity-kv` (`KV_CROSSLANG=1`).
- `List` returns a set; no `ORDER BY`.
- Tests: `sqlite` and `badger` run embedded; `etcd` and `tidb` start
  testcontainers and skip under `-short` or without Docker.

## Neighbours

- `hop.top/kit/go/storage/sqldb`: the SQLite connection `kv/sqlite` opens.
- `hop.top/kit/go/core/netpolicy`: the `--offline` guard the network drivers call.
- `hop.top/kit/go/storage/blob`, `hop.top/kit/go/storage/secret`: objects and credentials.

## See also

- [Storage abstractions](../../../docs/adopters/concepts/storage-abstractions.md), including "Keys bind as TEXT"
- [`doc.go`](doc.go): driver table and network-policy seams
