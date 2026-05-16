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
[`engine/store/README.md`](../../../engine/store/README.md#branching-fork--merge)
for the public surface and [engine-protocol.md](../reference/engine-protocol.md)
for the wire shapes.

Long-running engines can bound history growth with opt-in pruning:
`/prune` and `/abandon` retire heads and reclaim storage on
`(type, id)` documents whose history has dead heads (from
`Abandon`, `Merge`, or `Revert`). See
[`engine/store/README.md`](../../../engine/store/README.md#pruning--liveness)
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

## Next

- Run it: [run-the-engine.md](../guides/run-the-engine.md) — install,
  start, and connect a tool.
- Threat model + identity: [engine-security.md](../reference/engine-security.md).
- Wire format: [engine-protocol.md](../reference/engine-protocol.md).
- Sync internals: [engine-sync.md](../reference/engine-sync.md).
- Sidecar binary install + flag list:
  [`cmd/kit/README.md`](../../../cmd/kit/README.md).
