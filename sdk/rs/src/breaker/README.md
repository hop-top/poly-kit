# breaker

## What it answers

How often, how much and how fast an operation may run before the tool stops itself: a circuit that opens after
consecutive failures, MaxOps/MaxBytes budgets, a bursty rate limiter. Wrong place for retrying a failed call: that
is `hop_top_kit::util::retry`, which a breaker can sit in front of.

## Use it when

- a dependency keeps failing and callers should back off → `circuit(CircuitOpts { .. })`, then `allow` + `record`
- an agent must stop after N tool calls or N tokens → `max_ops(n)`, `max_bytes(n)` with `record(ok, n)`
- calls must stay under N per period → `max_per_interval(n, d)` or `max_per_minute(n)`
- a refusal should log, not block, while migrating → `on_trip(Action::Warn)`
- other components react to trips → `publisher(p)`; rebrand topics with `topic_prefix` or `topics`
- tests, or wasm32, need the time under control → `clock(c)`

## Quick start

```rust
use std::sync::Arc;
use hop_top_kit::breaker::{Breaker, Record};

let b = Breaker::builder("llm-calls")
    .max_ops(100)
    .max_per_minute(30)
    .logger(Arc::new(|_: &Record| {}))
    .build();

if b.allow().is_ok() {
    let ok = true; // call the dependency
    b.record(ok, 0);
}
```

## Contract

- Feature `breaker` is `domain` plus nothing of its own; wasm-safe with a caller `Clock` (no `SystemClock` there).
  Authority: the crate [feature table](../../README.md#features).
- Go is canonical: the circuit, budgets, rate limiter, `OnTrip`, log records and events are pinned by
  [`contracts/breaker-v1`](../../../../contracts/breaker-v1/README.md).
- `allow` checks circuit, rate, bytes, ops in that order and records nothing; follow each `Ok` with one `record`.
- Closed opens after `failure_threshold` consecutive failures (default 5). Open turns half-open once `delay`
  has elapsed (default 30s, boundary inclusive), seen by `state` too. Half-open admits `success_threshold`
  probes (default 1); a failure re-opens, enough successes close; a probe a later check refuses gives its
  permit back.
- `trip` opens for a full cooldown; `reset` closes with a fresh window and zeroes counters. `Stats::trips`
  counts manual trips and automatic opens (reason `circuit`).
- `Action::Warn` allows a refusal and logs `breaker would reject` naming the policy; `Degrade` behaves like
  `Halt`: no fallback is ported.
- Logs go to the `Logger` on the caller's thread (`StderrLogger` by default); events go to the
  `domain::EventPublisher` as `Payload::BreakerTripped` / `BreakerTransition`, source `core.breaker`, topics
  `kit.core.breaker.{tripped,opened,closed,half_opened}`, on the `Spawn` given or a thread of their own.
- Not ported: bulkhead, timeout, fallback, `breaker.yaml`, the registry, the wrap helpers.

## Neighbours

- `hop_top_kit::domain`: the `EventPublisher` and `Payload` the events travel in
- `hop_top_kit::util::retry`: backoff for the calls a breaker admits

## See also

- Go reference: [go/core/breaker](../../../../go/core/breaker/README.md)
