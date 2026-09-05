# memory

## What it answers

An in-process `MutableStore` for tests: a mutex-guarded map, nothing
persisted. Wrong package for anything that outlives the process
(`go/storage/secret/file`).

## Use it when

- `secret.Config{Backend: "memory"}` after a blank import of this package; no other field is read
- a test needs a writable store, or a `composite` member with known contents

## Quick start

```go
store := memory.New()
ctx := context.Background()
_ = store.Set(ctx, "api-token", []byte("t0k3n"))
ok, _ := store.Exists(ctx, "api-token")
fmt.Println(ok)
// Output: true
```

## Contract

- Registered as `"memory"`. Each `Open` returns a fresh, empty store.
- `List` filters by prefix; order is not defined.
- `Metadata` always returns `ErrNotSupported`.
- Tests: embedded.

## Neighbours

- `hop.top/kit/go/storage/secret/composite`: the usual consumer in tests.

## See also

- [`secret/README.md`](../README.md)
