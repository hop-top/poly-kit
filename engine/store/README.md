# store

## What it answers

Where `kit serve` keeps documents and their version history, and what the
storage layer guarantees about that history. Internal to the sidecar:
external clients (including `engine/sdk/ts-kit-engine` and
`engine/sdk/py-kit-engine`) reach it through the engine's HTTP/WS API, not
by importing the package. In-process storage adapters live in
`go/storage/*`, not here.

## Use it when

- you need type-tagged JSON blobs keyed by `(type, id)` → `DocumentStore`
  (`Create`, `Get`, `List`, `Update`, `Delete`, `Query{Limit, Offset, Sort, Search}`)
- you need a version recorded on every mutation → `VersionedDocumentStore`
  (history, revert, `Fork`, `Merge`, `Branches`)
- you need ephemeral history for tests or dev → `--versions=memory` on
  `kit serve`, or `NewInMemoryVersionedDocumentStore` directly
- you need to bound history growth → `Prune`, `Abandon`,
  `Branches(..., store.WithLiveOnly())`

## Contract

| Type | Where | Persistence |
|---|---|---|
| `Document` | SQLite table `documents` | durable |
| `Version` | SQLite tables `versions`, `version_parents`, `snapshot_blobs`, `version_snapshots` (default), or in-memory map (`--versions=memory`) | durable by default, in-memory opt-in |

- A document write and its version row commit in one transaction (SQLite
  backend, ADR-0011). Version tables are additive and present in every
  engine DB; the in-memory backend ignores them.
- `data` is opaque JSON; `id` comes from the JSON `"id"` field if present,
  else `util.Short`. Search is `LIKE` on the blob with backslash escaping.
- Snapshots are content-addressed (`util.Short(data, 16)`, refcounted in
  `snapshot_blobs`). Delete history only through
  `VersionStore.DeleteHistory`, never `DELETE FROM versions`. Errors:
  `ErrHashCollision`, `ErrRefcountOverflow`, `ErrRefcountUnderflow`.
  Legacy `snapshots` tables migrate automatically at first boot (ADR-0014).
- `Fork` is not idempotent: two calls with the same `fromSeq` yield two
  sibling versions (ADR-0013). `Merge` takes caller-supplied data;
  conflict detection is the caller's job.
- Every version carries a `live` bit. `Abandon`, `Merge` and `Revert`
  retire heads; `Fork` and `Update` do not. `Abandon` returns
  `ErrNotAHead` or `ErrCannotAbandonLastLiveHead`. `Prune` removes only
  versions outside `RetentionPolicy` that are not live heads and whose
  descendants are all prunable (ADR-0015).
- Conformance suites (`versionstore_test.go`) and 1000-iteration property
  tests run identical scenarios against both backends. The
  property tests run in parallel; `-short` or `KIT_PROPERTY_ITERATIONS=<n>`
  trims the count (pull-request CI runs 100, nightly the full 1000).

## Neighbours

- `engine/sdk/`: language SDKs that speak HTTP to `kit serve`
- `go/runtime/domain/version`: the version DAG primitive used here
- `go/storage/blob`, `kv`, `secret`, `sqlstore`: in-process adapters that
  stay in the calling process
- `cmd/kit`: `kit serve` flags, including `--versions`

## See also

- [Engine overview, Storage model](../../docs/adopters/concepts/engine-overview.md#storage-model):
  schema, branching, deduplication, pruning algorithm, benchmarks
- [Engine protocol reference](../../docs/adopters/reference/engine-protocol.md):
  wire shapes for `/history`, `/revert`, `/branches`, `/fork`, `/merge`,
  `/prune`, `/abandon`
- [Inspect with Datasette](../../docs/adopters/guides/inspect-with-datasette.md)
