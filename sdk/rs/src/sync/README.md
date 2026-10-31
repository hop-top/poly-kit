# sync

## What it answers

How do two stores of the same entities converge without a central server? A `Replicator` captures local changes as
`Diff`s, pushes them to named remotes, pulls theirs and applies them to your `Repository`. Wrong place for storing
the entities themselves: that is your `domain::Repository`.

## Use it when

- a local-first app must copy its changes to one or more peers → `Replicator::builder(repo).remote(..).build()`, then `start`
- a peer is a backup or a read replica → `SyncMode::PushOnly` or `SyncMode::PullOnly` on its `Remote`
- a remote may see only some entities → `Remote::filter(|d| d.entity_type == "notes")` (filters what is pushed)
- two nodes edited the same entity → `last_write_wins`, or `resolve_diff` with your own merge
- events must be ordered across nodes → `Clock` stamps `Timestamp`s (wall time, counter, node)
- tests or wasm32 need the time under control → `wall_clock(..)` on the builder, `Clock::with_wall_clock`
- the peer speaks HTTP → `HttpTransport` (feature `sync-http`)

## Quick start

Verified by the `sync` module doctest:

```rust
let ctx = Context::background();
let rep = Replicator::builder(repo.clone())
    .remote(Remote::new("origin", peer).mode(SyncMode::PullOnly))
    .build();
rep.sync_once(&ctx, "origin").unwrap();      // one round now, as a loop would
// or: rep.start(&ctx)?;  ...  rep.stop();   // a thread per remote, one round per interval
```

## Contract

- Feature `sync` is `domain` plus `base64`; wasm-safe given a caller `WallClock`. `sync-http` adds the HTTP
  transport and is native only. Authority: the crate [feature table](../../README.md#features).
- Go is canonical ([go/runtime/sync](../../../../go/runtime/sync)); no shared conformance corpus exists for it.
  The wire is pinned by `tests/sync_core.rs`: `Timestamp` keys `physical`, `logical`, `node_id`; `Diff` keys
  `entity_id`, `entity_type`, `operation` (0 create, 1 update, 2 delete), `before`, `after`, `timestamp`,
  `node_id`; `SyncMode` 0 bidirectional, 1 push only, 2 pull only.
- `before` and `after` are Go `[]byte`: standard base64 of the snapshot's JSON on the wire, omitted when empty,
  decoded from base64, `null` or absence. `serde_json` omits Go's `<`, `>`, `&`, U+2028, U+2029 escapes.
- `last_write_wins`: later `Timestamp` (physical, logical, node id) wins; on a full tie the greater `Diff.node_id`
  wins, local on equality (`>=`). Both sides must agree or they pick different winners.
- A round (`sync_once`): ping (failure: status `connected=false`, `sync.remote.error`), push unless `PullOnly`,
  pull unless `PushOnly`, trim, stamp `last_sync`. A push or pull failure is kept in the status as `last_error`
  and retried next round; it is not returned. Pushes pass the filter; the cursor moves past filtered-out diffs.
- Pulled diffs overwrite: no `after` deletes; otherwise update, falling back to create on any update error. The
  replicator never calls the `MergeFunc` it is given (as Go).
- Change capture: subscribes to `<prefix>.*` (default `kit.runtime.entity`, any 3-segment prefix via
  `subscription_prefix`, which panics on a malformed one); the last segment `created`, `updated` or `deleted`
  picks the operation, anything else is ignored. The payload must carry a string `id`. Create and update snapshot
  the live entity (none if the repository cannot find it).
- The queue holds 10000 diffs and drops the oldest. A remote's cursor is an absolute position, so neither a drop
  nor another remote's trim makes it skip a diff (Go's index cursors can).
- Kept from Go: a `PullOnly` remote never advances its cursor, so it blocks trimming and reports the whole queue
  as pending.
- Events on the bus: `sync.remote.connected|synced|error` and `sync.apply.error`, source `sync.replicator.<name>`
  (for apply errors `<entity id>`), payload `{"remote": <name or entity id>}`; the topics are Go's three segments,
  so a strict bus refuses them.
- Threads: one per remote, first round after one interval, ended by `stop`, a done context or `remove_remote`.
  wasm32-unknown-unknown has none: drive `sync_once` from your timer.
- Not ported: durable cursor, `BusPublisher`, the SQLite repository and `domain/version.VersionedRepository`
  (Go follow-ups or other packages).

## Neighbours

- `hop_top_kit::domain`: the `Repository`, `Entity` and `RepoError` the replicator applies diffs through
- `hop_top_kit::bus`: change events in, `sync.*` events out, `Context` everywhere
- `hop_top_kit::netpolicy`: `HttpTransport` goes through its guarded client, so `--offline` and `netpolicy.allow_hosts` refuse remote hosts; redirects follow the guarded client default (cross-origin hops refused while the bearer token is set)

## See also

- Go reference: [go/runtime/sync](../../../../go/runtime/sync/README.md)
- Wire and cross-language scenario: [engine sync guide](../../../../docs/adopters/reference/engine-sync.md)
