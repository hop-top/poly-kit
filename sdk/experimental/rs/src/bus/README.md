# bus

## What it answers

How do in-process components publish and subscribe to named events under a validated four-segment topic
grammar? Service lifecycle events are emitted by `hop_top_kit::serve`; cross-process transport (the Go
`NetworkAdapter` and SQLite adapter) is not ported.

## Use it when

- one component must react to another's state change without a direct call → `Bus::subscribe` with a
  pattern, `Bus::publish` an `Event`
- handlers register in one place and publishers live elsewhere → wrap in `SharedBus`, clone it
- an adopter rebrands topics under its own prefix → `prefix_topics(prefix, actions)`, which validates tense
- a publish must fail on a malformed topic rather than warn → `Bus::builder().enforce(Mode::Strict)`

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
- Dispatch is synchronous: handlers run inline in subscription order on the publisher's thread and the
  first handler error vetoes the publish. `Bus` is `Rc`-backed and `!Send`.
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
