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

## Object modifier

The Object segment may carry a snake_case modifier joined with an
underscore. The wire form stays a single segment:

```
kit.config.snapshot_reload.failed
                ^^^^^^^^^^
                object   = snapshot
                modifier = reload
```

Use the modifier when the same Object participates in distinct event
flavours that should remain distinguishable on the wire (`snapshot`
vs `snapshot_reload`). Multi-word modifiers are fine: parsing splits
on the first underscore, so `snapshot_partial_reload` parses as
object=`snapshot`, modifier=`partial_reload`. ADR-0017 records the
full grammar rationale and the design pivot from sigils to
payload-side qualifiers.

## Qualifiers convention

The four semantic axes that describe why / how / with-what /
during-what do not live in the topic string. They live in the payload
via `bus.Qualifiers` (`Reason`, `Mechanism`, `Property`,
`Circumstance`), embedded in the payload struct and read back by
subscribers with `bus.QualifiersFrom`. Signatures and embed shapes:
[bus-api.md](../reference/bus-api.md#qualifiers).

## Migrating existing emitters

For most emitters no migration is required. Existing hand-written
topic constants stay valid as long as they pass `Validate`; the
builder and qualifier surface is purely additive.

For new code, prefer the builder over hand-written strings:

```diff
-const TopicSnapshotReloaded bus.Topic = "kit.config.snapshot.reloaded"
+var TopicSnapshotReloaded = bus.TopicOf("kit", "config", "snapshot").Action("reloaded")
```

If an event currently encodes a reason / mechanism / property /
circumstance in the topic itself (via a sigil-like character or extra
dot segments), migrate the qualifier into the payload:

```diff
-bus.NewEvent("kit.config.snapshot.reloaded?reason=sighup", "config", payload)
+payload := SnapshotReloaded{
+    Qualifiers: bus.Qualifiers{Reason: "sighup", Mechanism: "signal"},
+    // ... other payload fields
+}
+bus.NewEvent(
+    bus.TopicOf("kit", "config", "snapshot").Action("reloaded"),
+    "config",
+    payload,
+)
```

Audit checklist on upgrade:

1. Grep topic constants for the sigil characters `?`, `+`, `=`, `@`
   and for topics with more than 4 dot segments; none are expected,
   but verify.
2. Replace string-concatenated topic constants with
   `bus.TopicOf(...).Action(...)` (or `Mod(...).Action(...)`).
3. For events that distinguish via reason / mechanism / property /
   circumstance, embed `bus.Qualifiers` in the payload struct and stop
   encoding the qualifier in the topic.
