# badger

## What it answers

A `kv` driver over BadgerDB: an LSM store in a directory, native TTL, Go
readers only. Wrong package when another language must open the data
(`go/storage/kv/sqlite`) or hosts share it (`go/storage/kv/etcd`, `tidb`).

## Use it when

- `kv.Config{Backend: "badger", Path: "state/"}` after a blank import of this package; `Path` is a directory and is required. `PutWithTTL` maps to Badger's own TTL.

## Quick start

```go
dir, _ := os.MkdirTemp("", "kv-badger")
defer os.RemoveAll(dir)

store, err := badger.New(dir)
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

## Contract

- Registered as `"badger"` via `kv.RegisterBackendContext`; opened with `DefaultOptions(dir)` and a nil logger. No dial, so an offline context never refuses it; a cancelled context is checked before any file is created. Tests: embedded.
- One process holds the directory lock; a second `New` on the same path fails. Not part of the kv-v1 corpus; no other port reads Badger files.

## Neighbours

- `hop.top/kit/go/storage/kv`: the interface and `Open`; `kv/sqlite` is the file-backed alternative with the same TTL surface.
