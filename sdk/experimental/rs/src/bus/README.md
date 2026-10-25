# bus

## What it answers

How do in-process components publish and subscribe to named events under a validated four-segment topic
grammar? Service lifecycle events are emitted by `hop_top_kit::serve`; cross-process transport (the Go
`NetworkAdapter`) is not yet ported and the SQLite adapter is out of scope; see
[rust-bus-parity.md](../../../../../docs/contributors/architecture/rust-bus-parity.md).

## Use it when

- one component must react to another's state change without a direct call → `Bus::subscribe` with a
  pattern, `Bus::publish` an `Event`
- handlers register in one place and publishers live elsewhere → wrap in `SharedBus`, clone it
- an adopter rebrands topics under its own prefix → `prefix_topics(prefix, actions)`, which validates tense
- a publish must fail on a malformed topic rather than warn → `Bus::builder().enforce(Mode::Strict)`
- a handler must not hold up the publisher → `SharedBus::subscribe_async` with a `Spawn` for your executor

## Quick start

```rust
use hop_top_kit::bus::{Bus, Event, Mode};
use serde_json::json;

let mut bus = Bus::builder().enforce(Mode::Strict).build();
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
- Async dispatch, Go's `SubscribeAsync`, on `SharedBus` only: `subscribe_async(pattern, spawner, handler)`.
  Handlers get the publish context and a copy of the event, are launched in subscription order after every
  sync handler succeeded (a veto launches none), and return nothing, so they never fail a publish. No
  async runtime is pulled in: `Spawn` (any `Fn(Task)`) puts each task on the caller's executor, e.g. a
  closure calling `tokio::spawn` or `wasm_bindgen_futures::spawn_local`. A tokio adapter may follow behind
  its own feature.
- Pool: at most `max_async` handlers run at once (`Bus::builder().max_async(n)`, Go's `WithMaxAsync`;
  default `DEFAULT_MAX_ASYNC` = 256, `0` means the default). A publish blocks while the pool is full, as
  Go's does, so the executor must run tasks independently of the publishing thread. After close a
  blocked publish returns `Ok` and launches nothing. On `wasm32-unknown-unknown` the pool is uncapped.
- `Context` is the Rust form of Go's `context.Context`: cancellation, a deadline, typed values, and Go's
  error texts (`context canceled`, `context deadline exceeded`). `publish_ctx` hands it to `subscribe_ctx`
  handlers without reading it, as Go does; `publish` and `subscribe` are the background-context forms.
  On `wasm32-unknown-unknown` a context has no deadline (no clock).
- `SharedBus::unsubscribe` waits until no in-flight publish can still call that handler, even when it
  returns `false` because close or another unsubscribe removed it first, as Go's closure always locks.
  Unsubscribing an async subscription admits no new task for it; tasks already launched run on.
  `SharedBus::close_ctx(&ctx)` marks the bus closed, then waits for in-flight sync publishes with no
  deadline, as Go's lock does, then for launched async tasks until `ctx` is done: on expiry it returns
  Go's `ctx.Err()` text (`BusError::Context`) and the bus stays closed. `close()` is `close_ctx` with the
  background context. With `ctx` already done and nothing left to drain it returns `Ok`; Go's `select`
  picks nil or `ctx.Err()` at random. On `wasm32-unknown-unknown` close cannot block for tasks: with
  some still running and `ctx` live it returns `BusError::NotDrained`; call it again once they ran.
- Subscribe after close registers nothing and drops the handler, so a handler holding a clone of the bus
  cannot keep it alive. Go keeps the subscription on the closed bus.
- Re-entrancy: a handler may publish, subscribe, unsubscribe and close on the `SharedBus` dispatching to
  it; Go deadlocks on all but the nested publish. Waits skip the calling thread's own publishes.
  Unsubscribe from a handler does not wait for other threads and takes effect at once. Close from a
  handler waits for other threads' publishes, so two handlers closing at once deadlock, as in Go.
- `publish` after `close` is `BusError::Closed`.
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
