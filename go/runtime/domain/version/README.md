# version

## What it answers

Append-only history for domain entities: a content-addressed `DAG` of `Version` nodes and a `VersionedRepository[T]` that records every `Create`, `Update`, `Delete` as a node over any `domain.Repository[T]`. Wrong package for plain CRUD (use the repository directly) and for cross-node transport (use `runtime/sync`).

## Use it when

- an entity must be revertible → `version.NewVersionedRepository[T](inner)` then `vr.Revert(ctx, entityID, versionID)`
- you must list or fetch a past snapshot → `vr.ListVersions(id)`, `vr.GetVersion(...)`
- two nodes edited the same entity and you must detect divergence → `vr.DAG().IsBranched()`, `Heads()`, `CommonAncestor(a, b)`

## Quick start

```go
d := version.NewDAG()
must(d.Append(version.Version{ID: "v1", Hash: "a1"}))
must(d.Append(version.Version{ID: "v2", ParentIDs: []string{"v1"}, Hash: "b2"}))
must(d.Append(version.Version{ID: "v3", ParentIDs: []string{"v1"}, Hash: "c3"})) // diverges from v2

fmt.Println(d.Heads(), d.IsBranched())
anc, _ := d.CommonAncestor("v2", "v3")
fmt.Println(anc)
// [v2 v3] true
// v1
```

`must` panics on a non-nil error. Verified by `example_test.go` in this directory.

## Contract

- `Append` rejects a duplicate ID and any unknown parent ID.
- `Hash` is the SHA-256 of the payload; equal state on two peers yields equal head hashes, which is how `sync.Replicator` detects convergence.
- More than one head means diverged history; the package detects branches and leaves merge resolution to the caller.
- `Ancestors` is exclusive of the node itself and unordered; `Children` follows insertion order.

## Neighbours

- `hop.top/kit/go/runtime/domain`: `Repository[T]` and `Entity`.
- `hop.top/kit/go/runtime/domain/sqlite`: a concrete inner repository to wrap.
- `hop.top/kit/go/runtime/sync`: replication that carries versions between peers.
