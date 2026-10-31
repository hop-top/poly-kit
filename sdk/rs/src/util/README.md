# util

## What it answers

How do I retry a fallible call with exponential backoff and full jitter, the same way Go `kit/core/util` does, without tying the policy to a runtime or to wasm-unsafe clocks?

## Use it when

- you need the next reconnect or retry wait → `RetryConfig::next_delay(attempt, &mut rng)`
- you want a ready loop with your own timer → `retry` (sync) or `retry_async`; `sleep` returns `false` to cancel
- a test needs fixed jitter → pass a `SplitMix64::new(seed)` or any `FnMut() -> u64` as the RNG

## Quick start

```rust
use std::time::Duration;
use hop_top_kit::util::retry::{retry, RetryConfig, RetryError, SplitMix64};

let cfg = RetryConfig { max_attempts: 3, ..RetryConfig::with_backoff() };
let mut tries = 0;
let out = retry(
    &cfg,
    &mut SplitMix64::from_entropy(),
    || { tries += 1; if tries < 3 { Err("down") } else { Ok(tries) } },
    |d: Duration| { std::thread::sleep(d); true },
);
assert_eq!(out, Ok::<_, RetryError<&str>>(3));
```

## Contract

`max_attempts` 0 is unlimited; base 100ms and max 30s when zero; delay is `min(base * 2^n, max)`, jittered over `[0, delay]`. Pinned by `contracts/retry-v1`. Cancellation is observed only while waiting. No feature flag and no dependencies: the policy builds on wasm32.

## See also

- [`contracts/retry-v1/`](../../../../contracts/retry-v1/README.md)
