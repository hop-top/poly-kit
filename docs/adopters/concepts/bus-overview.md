# Kit bus overview

> Audience: end-users adopting kit who need to decide whether to
> publish or subscribe to events, and how the bus fits with the rest
> of their tool.

The kit bus (`hop.top/kit/go/runtime/bus`) is a lightweight pub/sub
hub for in-process — and, optionally, cross-machine — event delivery.

Publishers emit events to dot-separated topics; subscribers filter
with MQTT-style wildcards. Sync handlers can veto a publish by
returning an error. Async handlers run after sync handlers succeed
and never block the publisher.

## Topic shape

Every kit topic is exactly four lowercase segments:

```
[Source].[Category].[Object].[Action]
```

Example: `kit.runtime.entity.created`. The action segment must be
past-tense (`created`, `updated`, `published`, …). Validation is
enforced at publish time, in one of three modes — `off`, `warn`,
`strict` — set per process via `WithEnforce(...)` or via the
`KIT_BUS_ENFORCE` env var. See [choose-enforcement-mode.md](../guides/choose-enforcement-mode.md)
to pick a mode and [configure-bus-enforcement.md](../guides/configure-bus-enforcement.md)
to wire it up.

## Adapters and sinks

The bus ships three adapters and a sink interface:

- **Memory** — in-process, default. Bounded goroutine pool for async
  delivery; subscribers run concurrently after the sync phase.
- **Network** — bridges instances over WebSocket (depends on
  `go/transport/api`).
- **JSONL sink** — fans events to newline-delimited JSON for
  external consumers (logs, audit, metrics) without blocking the
  publisher.

Sinks are added via `TeeBus`; they're side-effect processors, not
subscribers — slow sinks don't back up the bus.

## When to reach for it

Use the bus for:

- Cross-cutting concerns where a publisher shouldn't know its
  subscribers (audit, metrics, side effects).
- Loosely-coupled module communication (e.g. `domain.Service` emits
  entity lifecycle events; multiple listeners react).
- Hooking CLI commands into observability — see
  [hook-cli-into-bus.md](../guides/hook-cli-into-bus.md).

Don't use it for:

- Direct calls between tightly-coupled modules — call the function.
- Request/response — bus delivery is fire-and-forget; the first
  sync handler error vetoes, but there is no structured reply.
- Durable cross-process queueing — the memory adapter is
  in-process; the network adapter is best-effort with reconnect,
  not a queue.

## Cost

Linear in subscriber count. Async delivery is bounded by a default
256-goroutine semaphore (configurable via `WithMaxAsync`). The bus
itself is ephemeral; durability requires an explicit sink or
adapter.

## Next

- Quickstart: [hook-cli-into-bus.md](../guides/hook-cli-into-bus.md) —
  publish and subscribe end to end.
- Decide: [choose-enforcement-mode.md](../guides/choose-enforcement-mode.md)
  — pick `off`, `warn`, or `strict`.
- Configure: [configure-bus-enforcement.md](../guides/configure-bus-enforcement.md)
  — wire the chosen mode.
- Catalog: [domain-events.md](../reference/domain-events.md) — canonical
  pre-defined topics.
- Reference: [bus-api.md](../reference/bus-api.md) — types, methods, sinks.
