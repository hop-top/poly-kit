//! Runs the shared retry corpus in `contracts/retry-v1`. The vectors are
//! the source of truth: a failure means this port drifted from the
//! contract (Go is canonical), not that a vector should be edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.

use std::fs;
use std::path::PathBuf;
use std::time::Duration;

use hop_top_kit::util::retry::{RetryConfig, SplitMix64};
use serde::Deserialize;

const FILES: &[&str] = &["backoff.json"];

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Cfg {
    #[serde(default)]
    base_ns: u64,
    #[serde(default)]
    max_ns: u64,
}

impl Cfg {
    fn config(&self, jitter: bool) -> RetryConfig {
        RetryConfig {
            max_attempts: 0,
            base_delay: Duration::from_nanos(self.base_ns),
            max_delay: Duration::from_nanos(self.max_ns),
            jitter,
        }
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Defaults {
    base_ns: u64,
    max_ns: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct BackoffCase {
    name: String,
    config: Cfg,
    attempt: u32,
    delay_ns: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct JitterCase {
    name: String,
    config: Cfg,
    attempt: u32,
    max_ns: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AttemptsCase {
    name: String,
    max_attempts: u32,
    attempt: u32,
    stop: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Backoff {
    version: String,
    #[allow(dead_code)]
    description: String,
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    defaults: Defaults,
    backoff: Vec<BackoffCase>,
    jitter_bounds: Vec<JitterCase>,
    max_attempts: Vec<AttemptsCase>,
}

fn load() -> Backoff {
    let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/retry-v1");
    let mut seen: Vec<String> = fs::read_dir(&dir)
        .unwrap()
        .map(|e| e.unwrap().file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    seen.sort();
    assert_eq!(seen, FILES, "corpus files and runner registry differ");
    let v: Backoff = serde_json::from_str(&fs::read_to_string(dir.join("backoff.json")).unwrap())
        .expect("parse backoff.json");
    assert_eq!(v.version, "v1");
    v
}

#[test]
fn defaults_match_vector() {
    let v = load();
    let c = RetryConfig::default();
    assert_eq!(c.base(), Duration::from_nanos(v.defaults.base_ns));
    assert_eq!(c.max(), Duration::from_nanos(v.defaults.max_ns));
}

#[test]
fn backoff_table() {
    for c in load().backoff {
        let got = c.config.config(false).backoff(c.attempt);
        assert_eq!(got, Duration::from_nanos(c.delay_ns), "{}", c.name);
    }
}

#[test]
fn jitter_stays_within_bounds_and_reaches_both_ends() {
    for c in load().jitter_bounds {
        let cfg = c.config.config(true);
        let ceiling = Duration::from_nanos(c.max_ns);
        assert_eq!(cfg.unjittered(c.attempt), ceiling, "{}", c.name);
        let mut rng = SplitMix64::new(42);
        let (mut lo, mut hi) = (ceiling, Duration::ZERO);
        for _ in 0..4000 {
            let d = cfg.backoff_with(c.attempt, &mut rng);
            assert!(d <= ceiling, "{}: {:?} > {:?}", c.name, d, ceiling);
            lo = lo.min(d);
            hi = hi.max(d);
        }
        if c.max_ns <= 1000 {
            assert_eq!(lo, Duration::ZERO, "{}: zero never drawn", c.name);
            assert_eq!(hi, ceiling, "{}: ceiling never drawn", c.name);
        }
    }
}

#[test]
fn max_attempts_table() {
    for c in load().max_attempts {
        let cfg = RetryConfig {
            max_attempts: c.max_attempts,
            ..RetryConfig::default()
        };
        assert_eq!(cfg.is_last_attempt(c.attempt), c.stop, "{}", c.name);
    }
}
