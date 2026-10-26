# bus

## What it answers

How do in-process components publish and subscribe to named events under a validated four-segment topic
grammar? Service lifecycle events are emitted by `hop_top_kit::serve`; peers across processes link over
WebSocket through `network/` (feature `bus-network`); the SQLite adapter is out of scope; see
[rust-bus-parity.md](../../../../../docs/contributors/architecture/rust-bus-parity.md).

## Use it when

- one component must react to another's state change without a direct call → `DynBus::subscribe` with a
  pattern, `DynBus::publish` an `Event`; clones share one bus, so register and publish anywhere
- an adopter rebrands topics under its own prefix → `prefix_topics(prefix, actions)`, which validates tense
- a publish must fail on a malformed topic rather than warn → `DynBus::builder().enforce(Mode::Strict)`
- a handler must not hold up the publisher → `DynBus::subscribe_async` with a `Spawn` for your executor
- the transport must be swappable → `DynBus::builder().with_adapter(a)`; `MemoryAdapter` is in-process
- processes (Go or Rust) must share events → `DynBus::builder().with_network(addrs)` or `NetworkAdapter`
- you need only the in-memory core, no layers → `Bus`, or `SharedBus` to share it

## Quick start

```rust
use hop_top_kit::bus::{DynBus, Event, Mode};
use serde_json::json;

let bus = DynBus::builder().enforce(Mode::Strict).build();
bus.subscribe("crm.sales.deal.*", |e| {
    println!("{} from {}", e.topic, e.source);
    Ok(())
});

let event = Event::new("crm.sales.deal.created", "crm", json!({"id": 1}));
bus.publish(&event).unwrap();
```

## Contract

- Feature `bus` pulls in `serde` and `serde_json` only. Authority: the crate
  [feature table](../../README.md#features).
- Topic grammar: `[Source].[Category].[Object].[Action]`, each segment `^[a-z][a-z0-9_]*$`, total length at
  most 128. `validate` checks that; `validate_topic` additionally requires a past-tense action (`ed`
  suffix or `PAST_TENSE_WHITELIST`) and backs `prefix_topics`.
- Subscribe patterns: `*` matches exactly one segment, `#` matches zero or more trailing segments and must
  be last.
- `Mode` defaults to `Warn` (validate, report, still deliver); `Strict` rejects; `Off` skips validation.
- Sync dispatch: handlers run inline in subscription order on the publisher's thread and the first
  handler error vetoes the publish. `Bus` and `SharedBus` are `Send + Sync`, handlers must be too.
- Async (`SharedBus::subscribe_async`, Go's `SubscribeAsync`): launched in subscription order after every
  sync handler succeeded, never failing a publish. No runtime is pulled in: a `Spawn` puts each task on your
  executor, e.g. `handle.spawn(t)` on a captured `tokio::runtime::Handle` (bare `tokio::spawn` panics off
  the runtime) or `wasm_bindgen_futures::spawn_local`. At most `max_async` run at once (Go's
  `WithMaxAsync`, default 256); a publish blocks while the pool is full. Details, wasm32 included, are in
  the `async_dispatch` module docs.
- `Context` is the Rust form of Go's `context.Context`: cancellation, a deadline, typed values and Go's
  error texts. Handlers get it unread, as in Go. No deadline on `wasm32-unknown-unknown` (no clock).
- `SharedBus::unsubscribe` waits until no in-flight publish can still call that handler, as Go's closure
  always locks, and admits no new async task. `close_ctx(&ctx)` waits for sync publishes with no deadline,
  then for async tasks until `ctx` is done (Go's `ctx.Err()` text; the bus stays closed). On
  `wasm32-unknown-unknown` it returns `BusError::NotDrained` instead of blocking.
- `DynBus` layers in Go's `New` order: adapter behind topic enforcement (else the in-memory `SharedBus`),
  then network (`bus-network`; without it `with_network` panics on any address), then env sinks.
- Subscribe after close registers nothing. `publish` after `close` is `BusError::Closed`.
- Re-entrancy: a handler may publish, subscribe, unsubscribe and close on the `SharedBus` dispatching to
  it; Go deadlocks on all but the nested publish. Two handlers closing at once deadlock, as in Go.
- Parity: [event-topics.md](../../../../../docs/contracts/event-topics.md); the topic grammar is the
  parity obligation, there is no loaded fixture row.

## Neighbours

- `hop_top_kit::serve`: publishes the six `kit.serve.*` transitions through its own `Publisher` trait
- `hop_top_kit::bus::qualifiers`: `reason`, `mechanism`, `property`, `circumstance` lifted from a payload by
  `qualifiers_from`

## See also

- [Hook a CLI into the bus](../../../../../docs/adopters/guides/hook-cli-into-bus.md)
- [Configure bus enforcement](../../../../../docs/adopters/guides/configure-bus-enforcement.md)
- [Crate README, Bus](../../../../../docs/adopters/reference/rs-sdk.md#bus)
- Go reference: [go/runtime/bus](../../../../../go/runtime/bus/README.md)
