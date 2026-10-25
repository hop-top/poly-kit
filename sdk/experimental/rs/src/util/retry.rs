//! Retry and exponential backoff with full jitter.
//!
//! Mirrors Go `kit/core/util` (`RetryConfig`, `Backoff`, `Retry`).
//! Behaviour is pinned by `contracts/retry-v1`.
//!
//! # Runtime-free by design
//!
//! The policy (delay computation, stop rule) is plain `std` and builds on
//! `wasm32-unknown-unknown`: nothing here reads the clock, spawns, or
//! sleeps. Waiting is a seam the caller supplies:
//!
//! - [`RetryConfig::next_delay`] is the pure "next delay" step for a
//!   caller-driven loop;
//! - [`retry`] and [`retry_async`] drive that loop with a caller-provided
//!   `sleep`. Go's `ctx.Done()` becomes the sleep returning `false`
//!   (cancelled), so a tokio caller passes `|d| async { select! { .. } }`
//!   and a wasm caller passes a `gloo-timers` future, with no runtime
//!   dependency here.
//!
//! As in Go, cancellation is observed only while waiting between
//! attempts, never before the first call.
//!
//! # Jitter
//!
//! Full jitter draws uniformly from `[0, delay]` inclusive. Randomness is
//! injectable through [`Rng`]; [`RetryConfig::backoff`] seeds a
//! [`SplitMix64`] from std's per-process hash keys. That source is not
//! cryptographic and is fixed on wasm32-unknown-unknown, so wasm callers
//! wanting real jitter should pass their own [`Rng`].

use std::collections::hash_map::RandomState;
use std::fmt;
use std::future::Future;
use std::hash::{BuildHasher, Hasher};
use std::time::Duration;

const DEFAULT_BASE: Duration = Duration::from_millis(100);
const DEFAULT_MAX: Duration = Duration::from_secs(30);

/// Source of 64 random bits. Closures `FnMut() -> u64` implement it.
pub trait Rng {
    fn next_u64(&mut self) -> u64;
}

impl<F: FnMut() -> u64> Rng for F {
    fn next_u64(&mut self) -> u64 {
        self()
    }
}

/// SplitMix64: tiny, fast, seedable. Deterministic for a given seed.
#[derive(Debug, Clone)]
pub struct SplitMix64(u64);

impl SplitMix64 {
    pub fn new(seed: u64) -> Self {
        Self(seed)
    }

    /// Seed from std's randomly keyed hasher.
    pub fn from_entropy() -> Self {
        Self(RandomState::new().build_hasher().finish())
    }
}

impl Rng for SplitMix64 {
    fn next_u64(&mut self) -> u64 {
        self.0 = self.0.wrapping_add(0x9E37_79B9_7F4A_7C15);
        let mut z = self.0;
        z = (z ^ (z >> 30)).wrapping_mul(0xBF58_476D_1CE4_E5B9);
        z = (z ^ (z >> 27)).wrapping_mul(0x94D0_49BB_1331_11EB);
        z ^ (z >> 31)
    }
}

/// Exponential backoff settings. `Default` is Go's zero value: unlimited
/// attempts, default delays, jitter off. See [`RetryConfig::with_backoff`].
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub struct RetryConfig {
    /// Total attempts including the first; 0 = unlimited.
    pub max_attempts: u32,
    /// Delay before the second attempt; zero means 100ms.
    pub base_delay: Duration,
    /// Delay cap; zero means 30s.
    pub max_delay: Duration,
    /// Full jitter: draw each delay from `[0, delay]`.
    pub jitter: bool,
}

impl RetryConfig {
    /// Unlimited attempts, 100ms base, 30s cap, jitter on (Go
    /// `RetryWithBackoff`).
    pub fn with_backoff() -> Self {
        Self {
            jitter: true,
            ..Self::default()
        }
    }

    /// Effective base delay.
    pub fn base(&self) -> Duration {
        if self.base_delay.is_zero() {
            DEFAULT_BASE
        } else {
            self.base_delay
        }
    }

    /// Effective delay cap.
    pub fn max(&self) -> Duration {
        if self.max_delay.is_zero() {
            DEFAULT_MAX
        } else {
            self.max_delay
        }
    }

    /// `min(base * 2^attempt, max)` for 0-indexed `attempt`, before jitter.
    /// A product that overflows or is not positive yields the cap.
    pub fn unjittered(&self, attempt: u32) -> Duration {
        let max = clamp_ns(self.max());
        // f64 product, as in Go; the saturating cast stands in for Go's
        // overflow guard, which also catches the wrapped (<= 0) result.
        let raw = (clamp_ns(self.base()) as f64) * 2f64.powf(f64::from(attempt));
        let ns = raw as i64;
        Duration::from_nanos(if ns > max || ns <= 0 { max } else { ns } as u64)
    }

    /// Delay for 0-indexed `attempt`, with jitter drawn from `rng`.
    pub fn backoff_with(&self, attempt: u32, rng: &mut impl Rng) -> Duration {
        let delay = self.unjittered(attempt);
        if !self.jitter {
            return delay;
        }
        // delay <= i64::MAX ns, so +1 cannot overflow.
        let n = delay.as_nanos() as u64 + 1;
        Duration::from_nanos(rng.next_u64() % n)
    }

    /// [`backoff_with`](Self::backoff_with) using an entropy-seeded RNG.
    pub fn backoff(&self, attempt: u32) -> Duration {
        self.backoff_with(attempt, &mut SplitMix64::from_entropy())
    }

    /// Whether a failure of 0-indexed `attempt` ends the retries.
    pub fn is_last_attempt(&self, attempt: u32) -> bool {
        self.max_attempts > 0 && u64::from(attempt) + 1 >= u64::from(self.max_attempts)
    }

    /// Pure step for caller-driven loops: after a failure of 0-indexed
    /// `attempt`, `None` when attempts are exhausted, else the wait.
    pub fn next_delay(&self, attempt: u32, rng: &mut impl Rng) -> Option<Duration> {
        if self.is_last_attempt(attempt) {
            None
        } else {
            Some(self.backoff_with(attempt, rng))
        }
    }
}

fn clamp_ns(d: Duration) -> i64 {
    i64::try_from(d.as_nanos()).unwrap_or(i64::MAX)
}

/// Why [`retry`] stopped without a value.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RetryError<E> {
    /// Attempts exhausted; carries the last error.
    Failed(E),
    /// `sleep` reported cancellation while waiting.
    Cancelled,
}

impl<E: fmt::Display> fmt::Display for RetryError<E> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Failed(e) => e.fmt(f),
            Self::Cancelled => f.write_str("retry cancelled"),
        }
    }
}

impl<E: std::error::Error + 'static> std::error::Error for RetryError<E> {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Failed(e) => Some(e),
            Self::Cancelled => None,
        }
    }
}

/// Calls `f` until it succeeds or `cfg` runs out of attempts. `sleep`
/// waits the given delay and returns `false` if cancelled.
pub fn retry<T, E>(
    cfg: &RetryConfig,
    rng: &mut impl Rng,
    mut f: impl FnMut() -> Result<T, E>,
    mut sleep: impl FnMut(Duration) -> bool,
) -> Result<T, RetryError<E>> {
    for attempt in 0u32.. {
        let err = match f() {
            Ok(v) => return Ok(v),
            Err(e) => e,
        };
        match cfg.next_delay(attempt, rng) {
            None => return Err(RetryError::Failed(err)),
            Some(d) if !sleep(d) => return Err(RetryError::Cancelled),
            Some(_) => {}
        }
    }
    unreachable!("attempt counter exhausted")
}

/// Async [`retry`]; `sleep` is any future resolving to `false` on cancel.
/// Runtime-agnostic: the caller picks the timer.
pub async fn retry_async<T, E, Fut, SFut>(
    cfg: &RetryConfig,
    rng: &mut impl Rng,
    mut f: impl FnMut() -> Fut,
    mut sleep: impl FnMut(Duration) -> SFut,
) -> Result<T, RetryError<E>>
where
    Fut: Future<Output = Result<T, E>>,
    SFut: Future<Output = bool>,
{
    for attempt in 0u32.. {
        let err = match f().await {
            Ok(v) => return Ok(v),
            Err(e) => e,
        };
        match cfg.next_delay(attempt, rng) {
            None => return Err(RetryError::Failed(err)),
            Some(d) if !sleep(d).await => return Err(RetryError::Cancelled),
            Some(_) => {}
        }
    }
    unreachable!("attempt counter exhausted")
}
