# domain

## What it answers

May this entity move from one state to another, and who hears about it? `StateMachine` checks a transition
against its rules, lets a subscriber veto it, then announces it. Wrong place for the events' transport: that
is `hop_top_kit::bus`, or whatever your `EventPublisher` wraps.

## Use it when

- an entity has a lifecycle (`open` → `closed`) → `StateMachine::new(rules, publisher)` then `transition`
- a policy must be able to block a transition → return an error from the publisher on the pre topic
- other components react after a transition → subscribe to the post topic; it runs off the caller's thread
  (wasm32: give it a spawner)
- an adopter rebrands the topics → `with_topic_prefix("myapp.task.state")`, or `with_topics` per phase
- the post event must run on your executor, or on wasm32 off the caller → `with_spawner(spawn)`
- a store of entities must be swappable (memory, SQLite, an API) → implement `Repository<T: Entity>`; failures are `RepoError` (`NotFound`, `Conflict`, `Validation`, `Other`)

## Quick start

Verified by the `domain` module doctest:

```rust
use hop_top_kit::bus::Context;
use hop_top_kit::domain::{Rules, State, StateMachine};

let rules: Rules = [
    (State::from("open"), vec![State::from("closed")]),
    (State::from("closed"), vec![]),
]
.into_iter()
.collect();
let sm = StateMachine::new(rules, None);

sm.transition(&Context::background(), "open", "closed", false).unwrap();
let err = sm.transition(&Context::background(), "closed", "open", false).unwrap_err();
assert!(err.is_invalid_transition());
```

## Contract

- Feature `domain` is `bus` plus nothing of its own; wasm-safe. Authority: the crate
  [feature table](../../README.md#features).
- Go is canonical: error texts, the veto wrap, payload JSON and topic panics are pinned by
  [`contracts/statemachine-v1`](../../../../contracts/statemachine-v1/README.md).
- Two error forms: `no rules for state "x" (attempted "x" → "y")` when `allowed` is `None`, and
  `"x" → "y" (allowed: [a b])` otherwise, brackets kept when the list is empty. A veto is
  `pre-transition veto: <err>`, the publisher's error as its `source()`.
- `is_invalid_transition()` is Go's `errors.Is(err, ErrInvalidTransition)`, chain walk included.
- Events carry source `domain.statemachine` and a `TransitionPayload` whose JSON keys are `From`, `To`,
  `Force` (Go has no json tags). `to_json()` gives Go's bytes; plain `serde_json` skips Go's `<`, `>`, `&`,
  U+2028 and U+2029 escapes.
- Default topics `kit.runtime.state.{pre,post}_transitioned`. `with_topic_prefix` panics on a prefix
  `prefix_topics` rejects, with Go's text; `with_topics` keeps the default for an empty field.
- Post event: the given `Spawn`; without one, a thread of its own, or inline on wasm32-unknown-unknown.
- `Repository<T>` is Go's `domain.Repository[T]` port with `&self` methods (bring your own interior mutability), owned `T` returns and a `Context` first; `Query` is `limit`/`offset`/`sort`/`search`, zero meaning unset. Go's `ErrNotFound`, `ErrConflict`, `ErrValidation` are `RepoError` variants with Go's texts (`domain: not found`, ...) and `is_not_found()` etc. for `errors.Is`. No implementation ships: the port is dependency-free.
- Not ported: `Service`, the validator and the audit repository.

## Neighbours

- `hop_top_kit::bus`: topics, `Context`, `prefix_topics` and the `Spawn` seam this module builds on
- `hop_top_kit::serve`: service lifecycle transitions, published through its own `Publisher`

## See also

- Go reference: [go/runtime/domain](../../../../go/runtime/domain/README.md)
