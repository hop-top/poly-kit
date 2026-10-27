# statemachine-v1

## What it answers

What the domain state machine in `kit/runtime/domain` must do, independent of language: which transitions
fail and with what text, how a pre-transition veto is wrapped, which events an accepted transition publishes
with which bytes, and which topics the topic options compose. Wrong place for when the post-transition event
runs; that depends on each runtime's executor and has no vectors.

## Use it when

- you port or change `StateMachine`, `TransitionError`, `TransitionPayload` or the topic options in any
  SDK: run both runners
- you change state machine behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-statemachine
# or separately:
go test -race ./go/runtime/domain/ -run '^TestConformanceStateMachine'
cargo test --features domain --test domain_statemachine_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted,
not that the vector should be edited. The file carries `version: "v1"` and a `description` stating the
rules it pins.

| Section of `statemachine.json` | Pins |
|--------------------------------|------|
| `source`, `default_topics` | the event source and the two default topics |
| `transitions[]` `{from, to, force, error \| payload}` | against `rules`: the error text of a rejected transition, which publishes nothing, or the payload bytes published on pre then post |
| `transition_errors[]` `{from, to, allowed, error}` | both message forms; `allowed` null is no rules, `[]` is rules that allow nothing; `%q` quoting |
| `vetoes[]` `{veto, error}` | `pre-transition veto: <err>`, the cause kept, not an invalid transition |
| `payloads[]` `{from, to, force, json}` | `encoding/json` bytes: keys `From`, `To`, `Force`, HTML and U+2028/9 escapes |
| `topic_prefixes[]` `{prefix, pre_transitioned, post_transitioned \| panic}` | composed topics, or the panic text |
| `topic_overrides[]` | per-phase overrides; an empty topic keeps the default |

Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail
the run.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/runtime/domain/statemachine_conformance_test.go` (`TestConformanceStateMachine*`) |
| Rust | `sdk/experimental/rs/tests/domain_statemachine_conformance.rs` |

## See also

- [`go/runtime/domain/statemachine.go`](../../go/runtime/domain/statemachine.go)
- [`sdk/experimental/rs/src/domain/statemachine.rs`](../../sdk/experimental/rs/src/domain/statemachine.rs)
