# policy

## What it answers

May this actor do this now? Declarative rules, written as YAML with CEL
expressions, veto the state machine's pre-transition event and the entity
pre-validate and pre-persist events. Port of Go `go/runtime/policy`
(rules, deny-overrides, `PolicyDeniedError`, the stage ruleset and the
stage-violated emit). Feature `policy`; builds on `domain`, wasm-safe.

## Use it when

- a mutating operation needs a who-may-do-what gate outside Rust code →
  `parse_config(yaml)?`, `Engine::with_cel(&cfg)?`, `wire(&bus, Arc::new(engine))`
- the actor comes from your auth layer → `with_principal(&ctx, Principal { .. })`;
  without it the role is read from `KIT_POLICY_ROLE` and the id from `USER`
- a rule reads a `--note` or host flags → `with_context_attrs(&ctx, ContextAttrs { .. })`
- scopes have an operating mode → `stage_config()` (the shipped six rules),
  `.stage_resolver(..)`, and `.violation_publisher(..)` to hear denials
- you need the typed denial, not bus text → `Engine::decide(topic, activation)` or
  `on_event(&ctx, &event)`; `PolicyDeniedError::to_cli_error()` (with `output`)
  is the CONFLICT envelope, exit 4
- another expression language → implement `Evaluator`, pass it to `EngineBuilder::evaluator`

## Quick start

```rust
use std::sync::Arc;
use hop_top_kit::bus::DynBus;
use hop_top_kit::policy::{parse_config, wire, Engine};

let cfg = parse_config(include_bytes!("policies.yaml"))?;
let engine = Engine::with_cel(&cfg)?;      // compile errors surface here
let bus = DynBus::builder().build();
let unwire = wire(&bus, Arc::new(engine)); // vetoes ride the bus
```

## Contract

- Rules: `name` (unique), `on` (one of `kit.runtime.state.pre_transitioned`,
  `kit.runtime.entity.pre_validated`, `kit.runtime.entity.pre_persisted`), `when`
  (CEL), `effect` (when true) and `otherwise` (when false) as `allow|deny`,
  `message`. `async` is rejected. Messages are Go's.
- No rule on the topic: allow. Every matching rule is evaluated; the first deny in file
  order supplies the error. An expression that errors or is not a bool denies
  (`evaluation error: ...`).
- Activation: `principal {id, role, source}`, `resource {id, kind, fields}`,
  `entity {kind, op, track_type}`, `context {note, request_attrs}`, `payload` (the
  event payload as an object) and `stage`. A transition payload is Go's: `payload.From`,
  `payload.To`, `payload.Force` (`force` does not bypass a rule).
- Entity payloads are `PreEntityPayload` JSON (`Op`, `Phase`, `EntityID`, `Entity`),
  recognised by shape; the entity service itself is not ported, so whatever publishes
  the entity topics builds them.
- A denial through the bus reaches the publisher as `BusError::Handler` text
  (`policy "x" denied: msg`); the bus carries no typed error. A denied state machine
  transition is `StateMachineError::Veto` with that text.
- A stage-driven rule (its `when` mentions `stage` as a whole word) that denies emits
  `kit.runtime.stage.violated` with a `ViolationPayload` to the wired publisher.
- `load_config` and the env principal are compiled off wasm32; everything else is pure.

## Differences from Go

- CEL is `cel-rust`, not `cel-go`: no static type check. A typo'd identifier fails at
  evaluation and denies; `==` across types is false, so a mistyped rule takes its
  `otherwise`. `timestamp()` and `duration()` are not compiled in (the `chrono` feature
  needs `chrono-tz`); a `stage.since` is a string. Everything the shipped rules and Go's
  tests use (`has`, `in`, `&&`, `||`, `!=`, `matches`, `size`) works.
- No `ErrConflict` sentinel: `PolicyDeniedError::is_conflict()`.
- Go's `withcel.New(cfg, opts...)` is `EngineBuilder::cel()` then `build(&cfg)`.
- `Engine::decide` takes the activation by value; Go mutates the caller's map.
- No contract directory: Go has no shared policy vectors. The Go tests are ported in
  `tests/policy*.rs`, reading Go's rule files in place; `stage.yaml` is embedded and a
  test pins it byte-for-byte to Go's.

## Dependency vetting

`cel =0.15.0` (cel-rust/cel-rust), default features off, `regex` on.

- Alternatives: `cel-interpreter` 0.10.0 is the same project's older name, last released
  2025-07-23 and superseded by `cel`; no other maintained CEL implementation exists.
- Maintenance: pushed 2026-10-23, 35 contributors, 8 releases since 2026-03, not archived.
  `rsx analyze` scores 6.2/10 and passes the balanced policy (maintenance 10, license 10;
  adoption and security scores are low for registry reach, not for findings).
- Licence: MIT; the tree adds `antlr4rust` (BSD-3-Clause), `nom`, `winnow`,
  `typed-arena` (MIT), and MIT/Apache crates (`bit-set`, `bit-vec`, `borsh`, `byteorder`,
  `murmur3`, `pastey`, `minimal-lexical`, `proc-macro-crate`, `toml_edit`). All
  permissive, compatible with the crate's `MIT AND Apache-2.0`.
- Advisories: `cargo deny check advisories` clean on the resolved tree.
- Age: 0.15.0 published 2026-10-06, 25 days before the pin. Exact-pinned because rule
  semantics are the policy contract.
- wasm: `cargo build --target wasm32-unknown-unknown --features policy` builds; the
  smoke test runs under Node. 48 normal crates in the tree.

## Neighbours

- `go/runtime/policy`: the canonical implementation, tests and `stage.yaml`.
- `domain`: the state machine that publishes the pre-transition event.
- `bus`: the subscription and veto path; `output` (optional): the CONFLICT envelope.
