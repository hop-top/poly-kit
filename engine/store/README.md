# store

Server-side storage for the kit sidecar. Internal to `kit serve` —
external clients reach this through the engine's HTTP/WS API, not by
importing the package.

## What it stores

| Type | Where | Persistence | Purpose |
|---|---|---|---|
| `Document` | SQLite table `documents` | Durable | Type-tagged JSON blobs keyed by `(type, id)` |
| `Version` | SQLite tables `versions`, `version_parents`, `snapshot_blobs`, `version_snapshots` (default) — or in-memory map (`--versions=memory`) | Durable by default; in-memory opt-in | Point-in-time snapshots for history/revert |

## Document

A single SQLite table holds every document type — `(type, id, data,
created_at, updated_at)`. `data` is opaque JSON; the engine doesn't
parse it. `id` is taken from the JSON's `"id"` field if present, else
generated via `util.Short`.

`DocumentStore` exposes `Create`, `Get`, `List`, `Update`, `Delete`,
plus a basic `Query{Limit, Offset, Sort, Search}`. Search is `LIKE`
on the JSON blob with backslash escaping — coarse but enough for
small local datasets.

## VersionedDocument

`VersionedDocumentStore` wraps `DocumentStore` and records a version
on every mutation, building a DAG keyed by `type:id`. Used by
`engine.collection(...).history(id)` and revert flows.

The version backend is pluggable via the `VersionStore` seam.
`kit serve` defaults to the **SQLite-backed** implementation:
versioning rows live in the same database file `DocumentStore`
already owns, so a document write and its version row commit in a
single transaction (see [ADR-0011][adr-0011]). History survives
restart, no migration needed for upgrading installs (in-memory
state was already lost on every restart).

For tests and ephemeral / dev uses, pass `--versions=memory` to
`kit serve` (or call `NewInMemoryVersionedDocumentStore` directly):
no on-disk state, history clears on restart. Conformance tests
exercise both backends through identical scenarios.

### Schema

Additive — these tables are present in every engine DB regardless
of backend selection. The in-memory backend simply ignores them.

| Table               | Keyed by                     | Purpose |
|---------------------|------------------------------|---------|
| `versions`          | `(type, id, seq)` PK         | One row per version; monotonic `seq` per `(type, id)` |
| `version_parents`   | `(version_id, parent_id)` PK | Parent edges; many-to-one supports branching |
| `snapshot_blobs`    | `hash` PK                    | Content-addressed payload + `refcount` (see [Snapshot deduplication](#snapshot-deduplication)) |
| `version_snapshots` | `version_id` PK              | Join from version to its blob hash |

`ON DELETE CASCADE` on `version_parents.version_id` and
`version_snapshots.version_id` keeps deletes tidy. Refcount on
`snapshot_blobs` is decremented through `VersionStore.DeleteHistory`
(callers MUST go through that path; never `DELETE FROM versions`
directly).

## Branching (Fork / Merge)

`VersionedDocumentStore` exposes a public branching API on top of the
existing version DAG. The schema didn't change — the `version_parents`
table from the engine-versioned-sqlite track already supports
many-to-one edges (locked in ADR-0011 decision 3). The new
methods surface that capability:

- `Fork(ctx, type, id, fromSeq) (Version, error)` — appends a new
  version with `parents=[fromSeq's version_id]` and the same data as
  `fromSeq`. The new version becomes the latest seq, so subsequent
  `Update` extends this branch by default.
- `Merge(ctx, type, id, sourceSeq, targetSeq, data) (Version, error)` —
  appends a version with `parents=[sourceVersion, targetVersion]` in
  that order. The merged data is whatever the caller supplies;
  conflict detection is the caller's job in MVP.
- `Branches(ctx, type, id) ([]Version, error)` — returns the heads
  (DAG tips), ordered by ascending seq. A linear history has exactly
  one head; forked history has more.

> **Sibling-materialization semantics.** `Fork` is not idempotent —
> calling it twice with the same `fromSeq` produces two distinct
> sibling versions. That's how MVP expresses divergence without a
> separate `UpdateAt` API. See ADR-0013 for the rationale.

The conformance suite (`versionstore_test.go` —
`TestVersionedDocumentStoreBranchingConformance`) and a 1000-iteration
property test (`versioned_branching_property_test.go`) run identical
scenarios against both backends. SQLite-specific note: parent
insertion order is preserved via `ORDER BY rowid` in the DAG-load
query (see ADR-0011 amendment in ADR-0013).

## Snapshot deduplication

Snapshots are content-addressed. Identical-payload writes share a
single `snapshot_blobs(hash, data, refcount)` row; the join table
`version_snapshots(version_id, hash)` tracks which versions
reference which blob. `AppendVersion` uses `INSERT OR IGNORE` +
refcount bump; `DeleteHistory` decrements refcounts and drops
blobs at refcount=0. Public API on `VersionedDocumentStore` is
unchanged byte-for-byte — only on-disk shape and storage size
differ.

The hash is `util.Short(data, 16)` — the same function
`Version.Hash` already uses, so storage and DAG addressing share
one keyspace. A real collision is birthday-bound near 2^64; if
one ever surfaces, `AppendVersion` verifies the existing blob's
bytes against the incoming payload and returns `ErrHashCollision`
rather than corrupting the DAG. Refcount overflow at int64 max
returns `ErrRefcountOverflow`; an attempt to drive refcount below
zero returns `ErrRefcountUnderflow` (a SQL `CHECK (refcount >= 0)`
backs the same invariant at the storage layer).

Branching's `Fork` produces a sibling version with byte-identical
data; that sibling lands as a refcount bump on the source blob —
the dedup track's headline win in cooperation with the branching
track. `Merge` likewise reuses an existing blob if its
caller-supplied payload happens to match.

Storage savings on 1000-version workloads
(`BenchmarkDedup_StorageSavings`):

| Workload          | Blobs | Versions | Savings |
|-------------------|-------|----------|---------|
| Worst (no dups)   | 1000  | 1000     | 1.00×   |
| Best (all same)   | 1     | 1000     | 1000×   |
| Realistic middle  | 100   | 1000     | 10.0×   |

The conformance suite (`versionstore_test.go` —
`DedupReusesIdenticalSnapshots`, `DedupCrossDocumentSharing`,
`RefcountedDeleteCascadesCleanly`, plus the synthetic
`ErrHashCollision` / `ErrRefcountOverflow` /
`ErrRefcountUnderflow` fault-injection cases) and the property
test (`versioned_dedup_property_test.go`, 1000 iterations, seed
`dedupPropertySeed`) run identical scenarios against both
backends. Refcount integrity is asserted at every state:
`SUM(refcount) == COUNT(version_snapshots)`; every join hash
exists in `snapshot_blobs`; every blob refcount > 0.

Migration on existing installs is automatic at first boot:
`NewDocumentStore` runs `migrateToDedup`, which hashes every row
in the legacy `snapshots(version_id, data)` table, folds rows
into `snapshot_blobs` (aggregating refcount on collision), inserts
into `version_snapshots`, and drops the legacy table. Idempotent
on re-boot — a post-migration DB has no `snapshots` table so the
walk skips. The migration runs inside a single transaction; a
crash mid-walk leaves the pre-migration state recoverable.

ADR: ADR-0014.

## Pruning + liveness

`VersionedDocumentStore` exposes opt-in retention via `Prune`,
`Abandon`, and a liveness bit on every version. Pruning uses the
existing dedup primitives (refcount-decrement, delete-at-zero) so
no new storage shape is introduced for blobs.

### Liveness model

Every version row carries a `live` bit (`versions.live`, default
`TRUE`). Operations that retire a head:

- `Abandon(ctx, type, id, seq)` — operator-explicit retire of a
  current head. Returns `ErrNotAHead` if `seq` has children;
  `ErrCannotAbandonLastLiveHead` if it would leave zero live
  heads.
- `Merge(source, target, data)` — marks both source and target
  dead (consumed by the merge tip).
- `Revert(seq)` — marks the pre-revert head dead.

Operations that don't mark heads dead: `Fork` (source stays live;
new fork tip is also live); `Update` (other live heads on a
branched doc stay live until explicitly retired).

### Prune algorithm

`Prune(ctx, type, id, policy)` walks the DAG bottom-up. A version
V is prunable iff (a) V exceeds `RetentionPolicy` bounds (count
or age, AND-rule when both set); (b) V is not a *live* head;
(c) all of V's descendants are themselves prunable. Without the
"live" qualifier in (b), Prune is provably a no-op — every leaf
in `version.DAG` is a graph-topology head, and the descendant-
orphan rule in (c) would retain every ancestor transitively.

The use cases this serves:

- Abandoned fork tails (operator called `Abandon`)
- Merged branches (`Merge` automatically retired source/target)
- Revert orphans (`Revert` automatically retired the pre-revert
  head; in linear-revert topologies the pre-revert head remains
  an ancestor of the live revert tip and is therefore in the
  retain floor — see ADR-0015 consequences)

The escape hatch for "trim deep ancestry of an active live head"
is a follow-up (`shallow-snapshots` / `parent-edge-rewriting`).

### Branches with liveness filter

Default `Branches(ctx, type, id)` returns ALL heads (live + dead)
for backward compatibility. New callers wanting only the
operationally-meaningful tip set call
`Branches(ctx, type, id, store.WithLiveOnly())`.

### HTTP routes

```
POST /:type/:id/prune         {max_versions, max_age_seconds}
POST /:type/:id/abandon       {seq}
GET  /:type/:id/branches?live=1
```

The conformance suite (`versionstore_test.go` —
`TestVersionedDocumentStorePruningConformance`, 15 scenarios) and
the property test (`versioned_pruning_property_test.go`, 1000
iterations) run identical scenarios against both backends. The
restart integration test
(`cmd/kit/serve_pruning_integration_test.go`) verifies live bits
and post-prune state survive `kit serve` close + reopen.

ADR: ADR-0015.

## Wire format

The SDKs (`engine/sdk/ts-kit-engine`, `engine/sdk/py-kit-engine`)
don't import this package. They speak HTTP to `kit serve`, which
delegates to `DocumentStore` / `VersionedDocumentStore`.

## See also

- [`go/runtime/domain/version`](../../go/runtime/domain/version) —
  the version DAG primitive used here
