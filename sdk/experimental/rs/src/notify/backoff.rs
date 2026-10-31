//! Backoff between retry attempts.

use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use crate::util::retry::{Rng, SplitMix64};

/// The duration to wait before a retry: Go's `BackoffFunc`. `attempt` is
/// 0-indexed: 0 is the first retry, after the initial failure.
///
/// Pure on purpose: it takes only the attempt number and cannot be
/// woken early. Cancellation belongs to the caller ([`RetrySink`]),
/// which owns the wait.
///
/// [`RetrySink`]: super::RetrySink
pub type BackoffFn = Arc<dyn Fn(u32) -> Duration + Send + Sync>;

/// The longest delay: Go caps at `time.Duration(math.MaxInt64)`
/// nanoseconds, so a very large attempt cannot wrap.
const MAX_NANOS: f64 = i64::MAX as f64;

/// Exponential backoff: `base * factor^attempt`, as Go's
/// `ExponentialBackoff`. With `jitter` the delay is drawn uniformly from
/// `[0, base * factor^attempt)` to spread callers.
///
/// A zero `base`, a non-positive or non-finite product (`factor` of 0,
/// negative or NaN; an attempt so large it overflows `f64`) gives zero;
/// a `factor` of 1 gives `base` every time; a product past `i64::MAX`
/// nanoseconds is capped there. The jitter source is std's randomly keyed
/// hasher through [`SplitMix64`]: fine for spreading retries, not
/// cryptographic, and fixed on wasm32-unknown-unknown.
pub fn exponential_backoff(base: Duration, factor: f64, jitter: bool) -> BackoffFn {
    let rng = Mutex::new(SplitMix64::from_entropy());
    Arc::new(move |attempt| {
        if base.is_zero() {
            return Duration::ZERO;
        }
        let mut d = base.as_nanos() as f64 * factor.powf(f64::from(attempt));
        if d.is_nan() || d.is_infinite() || d <= 0.0 {
            return Duration::ZERO;
        }
        if jitter {
            let bits = rng
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .next_u64();
            d *= (bits >> 11) as f64 / (1u64 << 53) as f64;
        }
        if d >= MAX_NANOS {
            return Duration::from_nanos(i64::MAX as u64);
        }
        Duration::from_nanos(d as u64)
    })
}
