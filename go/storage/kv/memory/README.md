# memory

## What it answers

A `kv` driver that keeps everything in the process: a bounded map with
expiry and least-recently-used eviction. Wrong package when the data must
survive a restart (`go/storage/kv/sqlite`, `badger`) or be shared with
another process (`etcd`, `tidb`).

## Use it when

- a cache whose loss costs a recomputation, never a correctness bug
- `memory.New(memory.WithMaxBytes(n))` in code, or `kv.Config{Backend: "memory"}`
  after a blank import of this package (default budget)

## Quick start

```go
store := memory.New(memory.WithMaxBytes(1 << 20))
defer store.Close()

ctx := context.Background()
_ = store.PutWithTTL(ctx, "session", []byte("abc"), time.Hour)
v, ok, _ := store.Get(ctx, "session")
fmt.Println(string(v), ok)
// Output: abc true
```

## Contract

- Registered as `"memory"` via `kv.RegisterBackendContext`. `*Store` is a
  `kv.TTLStore`; `PutWithTTL` with a ttl of zero or less never expires.
- Bounded by bytes of keys plus values, `DefaultMaxBytes` (64 MiB) unless
  `WithMaxBytes` says otherwise. A write past the bound evicts expired
  entries first, then the least recently used. An entry larger than the
  whole budget is refused with `ErrValueTooLarge` and evicts nothing.
- Values are copied in and out. After `Close` every call returns `ErrClosed`.

## Neighbours

- `hop.top/kit/go/storage/kv`: the interface and `Open`.
- `hop.top/kit/go/transport/cmdsurface`: the served result cache uses it
  as its default store.
