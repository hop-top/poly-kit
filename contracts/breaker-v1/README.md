# breaker-v1

## What it answers

What the breaker in `kit/core/breaker` must do, independent of language: when the circuit opens, cools down and
closes, how half-open probes are counted, when the MaxOps/MaxBytes budgets and the bursty rate limiter refuse,
what `OnTrip` does with a refusal, and which log records and bus events every step produces. Wrong place for the
bulkhead, timeout, fallback, YAML config, registry and wrap helpers; Go's own tests cover those.

## Use it when

- you port or change the circuit, the budgets, the rate limiter, `OnTrip` or the breaker's logs and events in any
  SDK: run both runners
- you change breaker behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-breaker
# or separately:
go test -race ./go/core/breaker/ -run '^TestConformanceBreaker'
cargo test --features breaker --test breaker_conformance   # from sdk/experimental/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that
the vector should be edited. The file carries `version: "v1"` and a `description` stating the rules it pins.

| Section of `breaker.json` | Pins |
|---------------------------|------|
| `source`, `default_topics` | the event source and the four default topics |
| `scenarios[]` `{name, config, steps, logs, events?}` | a breaker named `name` on a fake clock, driven by `steps` |
| `steps[]` | `allow`, `record`, `advance` (ms), `state`, `trip`, `reset`, `stats` with their expected results |
| `logs[]` `{level, msg, fields}` | every log record, in order, with its full field set |
| `events[]` `{topic, payload}` | with `publisher` true, every bus event as a multiset; payload as Go's `encoding/json` writes it |
| `topic_prefixes[]` `{prefix, topics \| panic}` | the topics a prefix composes, or the panic text |

Rules the scenarios pin:

- `Allow` records nothing on the circuit; refusals by the budgets or the rate limiter do not feed it
- Closed opens after `failure_threshold` consecutive failures (default 5); a success resets the count
- Open turns half-open once `delay_ms` has elapsed (default 30s), boundary inclusive, seen by `state` too
- Half-open admits `success_threshold` probes (default 1); one failure re-opens, enough successes close
- `trips_total` counts manual trips and automatic opens (reason `circuit`); `reset` clears the failure window
- `on_trip` `warn` allows a refusal and logs `breaker would reject` naming the policy; `degrade` without a fallback
  halts

Rate vectors stay inside the first period: Go's limiter reads failsafe-go's wall clock, not the fake one. Runners
are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail the run.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/breaker/conformance_test.go` (`TestConformanceBreaker*`) |
| Rust | `sdk/experimental/rs/tests/breaker_conformance.rs` |

## See also

- [`go/core/breaker/README.md`](../../go/core/breaker/README.md)
- [`sdk/experimental/rs/src/breaker/README.md`](../../sdk/experimental/rs/src/breaker/README.md)
