# retry-v1

## What it answers

What the retry backoff policy in `kit/core/util` must compute, independent of language: the delay for each attempt, the overflow guard, the full-jitter range and when a failed attempt is the last. Wrong place for waiting and cancellation; those depend on each runtime's timer and have no vectors.

## Use it when

- you port or change `RetryConfig` defaults, `Backoff` or the max-attempts rule in any SDK: run both runners
- you change retry behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-retry
# or separately:
go test ./go/core/util/ -run '^TestConformanceRetry'
cargo test --test retry_conformance   # from sdk/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. The file carries `version: "v1"` and a `description` stating the rules it pins. Durations are integer nanoseconds.

| Section of `backoff.json` | Pins |
|---------------------------|------|
| `defaults` | zero or absent base is 100ms, zero or absent max is 30s |
| `backoff[]` `{config, attempt, delay_ns}` | with jitter off, `min(base * 2^attempt, max)`; a product that overflows or is not positive is the cap |
| `jitter_bounds[]` `{config, attempt, max_ns}` | with jitter on, every delay lies in `[0, max_ns]`; `max_ns` is the unjittered delay |
| `max_attempts[]` `{max_attempts, attempt, stop}` | 0 is unlimited, otherwise a failure of 0-indexed `attempt` stops when `attempt + 1 >= max_attempts` |

Go accepts a negative `MaxAttempts` as unlimited; Rust's `u32` cannot represent it, so use 0.

Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail the run. Jitter is checked as a range property, never as exact draws, so ports may use any RNG.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/util/retry_conformance_test.go` (`TestConformanceRetry*`) |
| Rust | `sdk/rs/tests/retry_conformance.rs` |

## See also

- [`go/core/util/retry.go`](../../go/core/util/retry.go)
- [`sdk/rs/src/util/retry.rs`](../../sdk/rs/src/util/retry.rs)
