# composite

## What it answers

One `MutableStore` that routes each key to the member that owns it: CI
secrets in `agefile`, developer credentials in `keyring`, `env` as a
read-only fallback. Not a backend: predicates are Go funcs, so it is
assembled with `New` after `secret.Open` opens each member.

## Use it when

- `composite.New(members...)` in priority order; `Owns` is the predicate (nil: catch-all), `RO` marks read fallbacks. Predicates: `HasPrefix`, `HasSuffix`, `AnyOf`, `MatchRegexp`, `Or`, `And`, `Not`.

## Quick start

```go
ci, dev := memory.New(), memory.New()
store := composite.New(
	composite.Member{Name: "ci", Store: ci, Owns: composite.HasPrefix("ci/")},
	composite.Member{Name: "dev", Store: dev},
)

ctx := context.Background()
_ = store.Set(ctx, "ci/token", []byte("a"))
_ = store.Set(ctx, "db/password", []byte("b"))
ciKeys, _ := ci.List(ctx, "")
devKeys, _ := dev.List(ctx, "")
fmt.Println(ciKeys, devKeys)
// Output: [ci/token] [db/password]
```

## Contract

- Reads: owners in declaration order, then non-owners. Writes: first non-`RO` owner, else `ErrNoWriter`; `Delete` also needs the owner to have the key.
- `List` is the deduped, sorted union. `Metadata` skips members returning `ErrNotSupported`. `New` panics on an empty `Name` or nil `Store`. Tests: embedded, memory members only.

## Neighbours

- `hop.top/kit/go/storage/secret`: the interfaces and `Open` for each member.
