# sqlite

## What it answers

The default `kv` driver: one SQLite file, table `kv(key TEXT PRIMARY KEY, value BLOB, expires_at INTEGER)`,
opened through `go/storage/sqldb`. Wrong package for write-heavy Go-only state (`kv/badger`) or shared hosts (`kv/etcd`, `kv/tidb`).

## Use it when

- `kv.Config{Backend: "sqlite", Path: "cache.db"}` after a blank import of this package; `Path` is required
- `PutWithTTL`: `*Store` is a `kv.TTLStore`; expired rows are swept lazily on writes. The only cross-language `kv` backend.

## Quick start

```go
dir, _ := os.MkdirTemp("", "kv-sqlite")
defer os.RemoveAll(dir)

store, err := sqlite.New(filepath.Join(dir, "cache.db"))
if err != nil {
	panic(err)
}
defer store.Close()

ctx := context.Background()
_ = store.PutWithTTL(ctx, "session", []byte("abc"), time.Hour)
keys, _ := store.List(ctx, "sess")
fmt.Println(keys)
// Output: [session]
```

## Contract

- Registered as `"sqlite"` via `kv.RegisterBackendContext`. No dial, so an offline context never refuses it. Tests: embedded, `go test ./go/storage/kv/sqlite/`.
- Keys bind as TEXT, never BLOB. Corpus: [`contracts/kv-v1/keys.json`](../../../../contracts/kv-v1/keys.json); `crosslang_test.go` exchanges it with Rust under `KV_CROSSLANG=1` (`make test-parity-kv`, needs `cargo`). The storage-class case always runs.

## Neighbours

- `hop.top/kit/go/storage/sqldb`: the connection this driver opens. `hop.top/kit/go/storage/kv`: the interface and `Open`; see [Keys bind as TEXT](../../../../docs/adopters/concepts/storage-abstractions.md#keys-bind-as-text).
