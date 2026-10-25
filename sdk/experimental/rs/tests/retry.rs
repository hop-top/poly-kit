//! Ports of the Go retry tests plus the Rust-only seams (injectable RNG,
//! injectable sleep). No test sleeps for real: the sleeper records the
//! requested delay and returns at once.

use std::cell::RefCell;
use std::collections::HashSet;
use std::time::Duration;

use hop_top_kit::util::retry::{retry, retry_async, RetryConfig, RetryError, SplitMix64};

const MS: Duration = Duration::from_millis(1);

/// Records requested delays; never sleeps. Cancels after `cancel_after`
/// sleeps when set.
struct Sleeper {
    delays: RefCell<Vec<Duration>>,
    cancel_after: Option<usize>,
}

impl Sleeper {
    fn new(cancel_after: Option<usize>) -> Self {
        Self {
            delays: RefCell::new(vec![]),
            cancel_after,
        }
    }
    fn sleep(&self, d: Duration) -> bool {
        let mut v = self.delays.borrow_mut();
        v.push(d);
        self.cancel_after.is_none_or(|n| v.len() <= n)
    }
}

fn nojitter(max_attempts: u32) -> RetryConfig {
    RetryConfig {
        max_attempts,
        base_delay: MS,
        ..RetryConfig::default()
    }
}

#[test]
fn retry_succeeds_first_try() {
    let s = Sleeper::new(None);
    let mut calls = 0;
    let r: Result<_, RetryError<&str>> = retry(
        &nojitter(3),
        &mut SplitMix64::new(1),
        || {
            calls += 1;
            Ok(7)
        },
        |d| s.sleep(d),
    );
    assert_eq!(r.unwrap(), 7);
    assert_eq!(calls, 1);
    assert!(s.delays.borrow().is_empty());
}

#[test]
fn retry_succeeds_third_attempt() {
    let s = Sleeper::new(None);
    let mut calls = 0;
    let r = retry(
        &nojitter(5),
        &mut SplitMix64::new(1),
        || {
            calls += 1;
            if calls < 3 {
                Err("not yet")
            } else {
                Ok(())
            }
        },
        |d| s.sleep(d),
    );
    assert!(r.is_ok());
    assert_eq!(calls, 3);
    assert_eq!(*s.delays.borrow(), vec![MS, 2 * MS]);
}

#[test]
fn retry_respects_max_attempts() {
    let s = Sleeper::new(None);
    let mut calls = 0;
    let r: Result<(), _> = retry(
        &nojitter(3),
        &mut SplitMix64::new(1),
        || {
            calls += 1;
            Err("fail")
        },
        |d| s.sleep(d),
    );
    assert_eq!(r, Err(RetryError::Failed("fail")));
    assert_eq!(calls, 3);
    // no sleep after the final failure
    assert_eq!(s.delays.borrow().len(), 2);
}

#[test]
fn retry_zero_max_attempts_is_unlimited() {
    let s = Sleeper::new(None);
    let mut calls = 0;
    let r = retry(
        &nojitter(0),
        &mut SplitMix64::new(1),
        || {
            calls += 1;
            if calls < 50 {
                Err("x")
            } else {
                Ok(calls)
            }
        },
        |d| s.sleep(d),
    );
    assert_eq!(r, Ok(50));
}

#[test]
fn retry_cancellation_during_wait() {
    let s = Sleeper::new(Some(0));
    let mut calls = 0;
    let r: Result<(), RetryError<&str>> = retry(
        &nojitter(0),
        &mut SplitMix64::new(1),
        || {
            calls += 1;
            Err("fail")
        },
        |d| s.sleep(d),
    );
    assert_eq!(r, Err(RetryError::Cancelled));
    assert_eq!(calls, 1);
}

#[test]
fn retry_jittered_waits_stay_within_ceiling() {
    let s = Sleeper::new(None);
    let cfg = RetryConfig {
        max_attempts: 6,
        base_delay: MS,
        jitter: true,
        ..RetryConfig::default()
    };
    let _: Result<(), _> = retry(&cfg, &mut SplitMix64::new(9), || Err("x"), |d| s.sleep(d));
    for (i, d) in s.delays.borrow().iter().enumerate() {
        assert!(*d <= cfg.unjittered(i as u32));
    }
}

#[test]
fn backoff_exponential_growth() {
    let cfg = RetryConfig {
        base_delay: 10 * MS,
        max_delay: Duration::from_secs(1),
        ..RetryConfig::default()
    };
    let mut prev = cfg.backoff(0);
    for i in 1..5 {
        let cur = cfg.backoff(i);
        assert!(cur >= prev, "attempt {i}: {cur:?} < {prev:?}");
        prev = cur;
    }
}

#[test]
fn backoff_caps_at_max_delay() {
    let cfg = RetryConfig {
        base_delay: 10 * MS,
        max_delay: 100 * MS,
        ..RetryConfig::default()
    };
    assert_eq!(cfg.backoff(100), 100 * MS);
    assert_eq!(cfg.backoff(u32::MAX), 100 * MS);
}

#[test]
fn backoff_jitter_adds_randomness() {
    let cfg = RetryConfig {
        base_delay: 100 * MS,
        max_delay: Duration::from_secs(10),
        jitter: true,
        ..RetryConfig::default()
    };
    // entropy-seeded entry point
    let seen: HashSet<_> = (0..20).map(|_| cfg.backoff(5)).collect();
    assert!(seen.len() >= 2, "identical values across 20 calls");
}

#[test]
fn backoff_injected_rng_is_deterministic() {
    let cfg = RetryConfig {
        jitter: true,
        ..RetryConfig::default()
    };
    let a = cfg.backoff_with(3, &mut SplitMix64::new(5));
    let b = cfg.backoff_with(3, &mut SplitMix64::new(5));
    assert_eq!(a, b);
    // closures are RNGs: 0 draws the floor, u64::MAX-1 the ceiling
    assert_eq!(cfg.backoff_with(3, &mut || 0u64), Duration::ZERO);
    let ceiling = cfg.unjittered(3);
    let n = ceiling.as_nanos() as u64 + 1;
    assert_eq!(cfg.backoff_with(3, &mut || n - 1), ceiling);
}

#[test]
fn with_backoff_defaults() {
    let c = RetryConfig::with_backoff();
    assert_eq!(c.max_attempts, 0);
    assert!(c.jitter);
    assert_eq!(c.base(), 100 * MS);
    assert_eq!(c.max(), Duration::from_secs(30));
    assert!(!RetryConfig::default().jitter);
}

#[test]
fn next_delay_combines_stop_rule_and_backoff() {
    let cfg = nojitter(2);
    let mut rng = SplitMix64::new(1);
    assert_eq!(cfg.next_delay(0, &mut rng), Some(MS));
    assert_eq!(cfg.next_delay(1, &mut rng), None);
}

fn block_on<F: std::future::Future>(f: F) -> F::Output {
    tokio::runtime::Builder::new_current_thread()
        .build()
        .unwrap()
        .block_on(f)
}

#[test]
fn async_retry_succeeds_and_cancels() {
    let delays = RefCell::new(vec![]);
    let calls = RefCell::new(0);
    let r = block_on(retry_async(
        &nojitter(5),
        &mut SplitMix64::new(1),
        || async {
            *calls.borrow_mut() += 1;
            if *calls.borrow() < 3 {
                Err("not yet")
            } else {
                Ok("done")
            }
        },
        |d| {
            delays.borrow_mut().push(d);
            async { true }
        },
    ));
    assert_eq!(r, Ok("done"));
    assert_eq!(*delays.borrow(), vec![MS, 2 * MS]);

    let r: Result<(), RetryError<&str>> = block_on(retry_async(
        &nojitter(0),
        &mut SplitMix64::new(1),
        || async { Err("fail") },
        |_| async { false },
    ));
    assert_eq!(r, Err(RetryError::Cancelled));

    let r: Result<(), _> = block_on(retry_async(
        &nojitter(2),
        &mut SplitMix64::new(1),
        || async { Err("fail") },
        |_| async { true },
    ));
    assert_eq!(r, Err(RetryError::Failed("fail")));
}
