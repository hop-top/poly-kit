# Kit engine overview

> Audience: end-users adopting kit who need to decide whether to
> run the engine, and how it fits with their tool.

The kit engine is a sidecar process — `kit serve` — that exposes
kit's storage, sync, identity, peer discovery, and event subsystems
over localhost HTTP and WebSocket. It is **language-agnostic**: any
client that can speak HTTP can use the engine.

This is the page that answers "do I need to run the engine at all?"

## Library vs engine

Kit ships two ways to use the same primitives:

| Surface | What it is | Use when |
|---|---|---|
| **Library** (`hop.top/kit/...`) | Native Go imports — bus, identity, storage, peer mesh all in-process | Your tool is Go-only and links kit directly |
| **Engine** (`kit serve`) | Sidecar HTTP/WS server that wraps the same primitives | Your tools span multiple languages, or multiple processes need shared state |

A single Go CLI that prints tables and exits doesn't need the
engine. A Python or TypeScript tool that needs kit semantics — or
a fleet of tools that share an identity, sync state, or peer mesh —
does.

## What it owns

The engine is the cross-process owner of:

- **Documents** — typed JSON blobs persisted in SQLite (one
  database per engine, optionally encrypted at rest).
- **Identity** — an Ed25519 keypair stored in the engine's data
  dir; verifiable by other peers.
- **Sync** — bidirectional document replication with registered
  remote engines.
- **Peer mesh** — mDNS discovery + trust via TOFU.
- **Event broadcast** — a WebSocket hub at `/events` for clients
  that want real-time notifications. (This is *not* the kit bus —
  see "Bus relationship" below.)

What the engine does **not** own: the kit bus itself, in-process
storage adapters (`go/storage/blob`, `kv`, `secret`, `sqlstore`),
or anything tied to a specific tool's command tree. Those stay in
the calling process.

Documents are versioned: every mutation appends a row to a per-doc
DAG, exposed via `/history` and `/revert`. The DAG is branch-capable
— `/branches`, `/fork`, and `/merge` let callers fork at an older
version, evolve the fork in parallel, and merge it back. See
[Branching (Fork / Merge)](#branching-fork--merge)
for the public surface and [engine-protocol.md](../reference/engine-protocol.md)
for the wire shapes.

Long-running engines can bound history growth with opt-in pruning:
`/prune` and `/abandon` retire heads and reclaim storage on
`(type, id)` documents whose history has dead heads (from
`Abandon`, `Merge`, or `Revert`). See
[Pruning + liveness](#pruning--liveness)
for the live/dead head model and the spec at
[`docs/contributors/specs/engine-version-pruning.md`](../../contributors/specs/engine-version-pruning.md)
for the full algorithm.

## Bus relationship

This is the conceptual pinch point most adopters hit:

- The **bus** (`go/runtime/bus`) is in-process. Publishers and
  subscribers live in the same Go binary. See
  [bus-overview.md](bus-overview.md).
- The **engine** runs a simpler `/events` WebSocket hub for
  document-lifecycle notifications across processes. It is not a
  bus bridge; topic semantics and 4-segment validation do not
  apply.

If you need cross-process pub/sub with kit's topic discipline, use
the bus's `NetworkAdapter`, not the engine's `/events` socket.

## When to run it

You need the engine when at least one of these is true:

- Your tools include non-Go languages (TS, Python).
- Multiple processes on one machine need shared identity or
  document state.
- You want a mDNS peer mesh between machines.
- You're using one of the language SDKs that spawns the engine as
  a subprocess.

You don't need it for: in-process Go tooling, single-machine
read-only utilities, one-off scripts.

## Persistence

State lives under `XDG_DATA_HOME/kit-engine/` (default
`~/.local/share/kit-engine/`) or wherever `--data` points. SQLite
holds documents; the identity keypair sits next to it. State
survives engine restarts. There are no external storage backends
(S3, etc.) in core today.

## Storage model

Server-side storage lives in `engine/store`, internal to `kit serve`:
external clients, including the SDKs (`engine/sdk/ts-kit-engine`,
`engine/sdk/py-kit-engine`), reach it through the engine's HTTP/WS API
and never import the package. `kit serve` delegates to `DocumentStore` /
`VersionedDocumentStore`. The version DAG primitive is
[`go/runtime/domain/version`](../../../go/runtime/domain/version).

### What it stores

| Type | Where | Persistence | Purpose |
|---|---|---|---|
| `Document` | SQLite table `documents` | Durable | Type-tagged JSON blobs keyed by `(type, id)` |
| `Version` | SQLite tables `versions`, `version_parents`, `snapshot_blobs`, `version_snapshots` (default) — or in-memory map (`--versions=memory`) | Durable by default; in-memory opt-in | Point-in-time snapshots for history/revert |

### Document

A single SQLite table holds every document type — `(type, id, data,
created_at, updated_at)`. `data` is opaque JSON; the engine doesn't
parse it. `id` is taken from the JSON's `"id"` field if present, else
generated via `util.Short`.

`DocumentStore` exposes `Create`, `Get`, `List`, `Update`, `Delete`,
plus a basic `Query{Limit, Offset, Sort, Search}`. Search is `LIKE`
on the JSON blob with backslash escaping — coarse but enough for
small local datasets.

### VersionedDocument

`VersionedDocumentStore` wraps `DocumentStore` and records a version
on every mutation, building a DAG keyed by `type:id`. Used by
`engine.collection(...).history(id)` and revert flows.

The version backend is pluggable via the `VersionStore` seam.
`kit serve` defaults to the **SQLite-backed** implementation:
versioning rows live in the same database file `DocumentStore`
already owns, so a document write and its version row commit in a
single transaction (see ADR-0011). History survives
restart, no migration needed for upgrading installs (in-memory
state was already lost on every restart).

For tests and ephemeral / dev uses, pass `--versions=memory` to
`kit serve` (or call `NewInMemoryVersionedDocumentStore` directly):
no on-disk state, history clears on restart. Conformance tests
exercise both backends through identical scenarios.

#### Schema

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

### Branching (Fork / Merge)

`VersionedDocumentStore` exposes a public branching API on top of the
existing version DAG. The schema didn't change — the `version_parents`
table already supports
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

### Snapshot deduplication

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
the headline win of deduplication combined with branching. `Merge` likewise reuses an existing blob if its
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

### Pruning + liveness

`VersionedDocumentStore` exposes opt-in retention via `Prune`,
`Abandon`, and a liveness bit on every version. Pruning uses the
existing dedup primitives (refcount-decrement, delete-at-zero) so
no new storage shape is introduced for blobs.

#### Liveness model

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

#### Prune algorithm

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

#### Branches with liveness filter

Default `Branches(ctx, type, id)` returns ALL heads (live + dead)
for backward compatibility. New callers wanting only the
operationally-meaningful tip set call
`Branches(ctx, type, id, store.WithLiveOnly())`.

#### HTTP routes

```text
POST /:type/:id/prune         {max_versions, max_age_seconds}
POST /:type/:id/abandon       {seq}
GET  /:type/:id/branches?live=1
```

Request and response shapes: [engine-protocol.md](../reference/engine-protocol.md)
§"Pruning + Liveness".

The conformance suite (`versionstore_test.go` —
`TestVersionedDocumentStorePruningConformance`, 15 scenarios) and
the property test (`versioned_pruning_property_test.go`, 1000
iterations) run identical scenarios against both backends. The
restart integration test
(`cmd/kit/serve_pruning_integration_test.go`) verifies live bits
and post-prune state survive `kit serve` close + reopen.

ADR: ADR-0015.

## Next

- Run it: [run-the-engine.md](../guides/run-the-engine.md) — install,
  start, and connect a tool.
- Threat model + identity: [engine-security.md](../reference/engine-security.md).
- Wire format: [engine-protocol.md](../reference/engine-protocol.md).
- Sync internals: [engine-sync.md](../reference/engine-sync.md).
- Sidecar binary install + flag list:
  [`cmd/kit/README.md`](../../../cmd/kit/README.md).
