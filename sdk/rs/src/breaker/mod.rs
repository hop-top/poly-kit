//! Runtime blast-radius limits: a circuit breaker, MaxOps/MaxBytes
//! budgets and a bursty rate limiter. Gated behind the `breaker`
//! feature, which builds on `domain` for the event publisher.
//!
//! Ports the Go canonical `hop.top/kit/go/core/breaker`; behaviour is
//! pinned by `contracts/breaker-v1`. The bulkhead, timeout, fallback,
//! YAML config, registry and wrap helpers are not ported: breakers are
//! plain values, so names need not be unique, and `OnTrip(Degrade)`
//! behaves like `Halt` until a fallback exists.
//!
//! The time comes from a [`Clock`]. [`SystemClock`] is the default
//! everywhere but wasm32-unknown-unknown, which has no clock: give one
//! with [`Builder::clock`] there.
//!
//! # Example
//!
//! ```
//! use std::sync::{Arc, Mutex};
//! use std::time::{Duration, SystemTime};
//! use hop_top_kit::breaker::{Breaker, CircuitOpts, Clock, State};
//!
//! struct Fixed(Mutex<SystemTime>);
//! impl Clock for Fixed {
//!     fn now(&self) -> SystemTime { *self.0.lock().unwrap() }
//! }
//! let clock = Arc::new(Fixed(Mutex::new(SystemTime::UNIX_EPOCH)));
//!
//! let b = Breaker::builder("llm-calls")
//!     .circuit(CircuitOpts { failure_threshold: 2, delay: Duration::from_secs(10), ..Default::default() })
//!     .max_ops(100)
//!     .clock(clock.clone())
//!     .logger(Arc::new(|_: &hop_top_kit::breaker::Record| {}))
//!     .build();
//!
//! b.allow().unwrap();
//! b.record(false, 0);
//! b.record(false, 0);
//! assert_eq!(b.state(), State::Open);
//! assert!(b.allow().is_err());
//!
//! *clock.0.lock().unwrap() += Duration::from_secs(10);
//! assert_eq!(b.state(), State::HalfOpen);
//! b.allow().unwrap();
//! b.record(true, 0);
//! assert_eq!(b.state(), State::Closed);
//! ```

mod circuit;
pub mod events;
mod handle;

use std::collections::BTreeMap;
use std::fmt;
use std::time::{Duration, SystemTime};

use serde::{Deserialize, Deserializer, Serialize, Serializer};

pub use events::{
    Level, Logger, Record, StderrLogger, Topics, TransitionPayload, TrippedPayload, Value,
    DEFAULT_CLOSED, DEFAULT_HALF_OPENED, DEFAULT_OPENED, DEFAULT_TRIPPED, SOURCE,
};
pub use handle::{Breaker, Builder};

/// The circuit's lifecycle. Go's `State`; serializes as Go's int
/// (closed 0, open 1, half-open 2), the form its payloads carry.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum State {
    /// Executions are allowed.
    Closed,
    /// Executions are refused until the cooldown elapses.
    Open,
    /// A bounded number of probes decide between closed and open.
    HalfOpen,
}

impl State {
    /// `closed`, `open` or `half_open`, as Go's log attrs write it.
    pub fn as_str(self) -> &'static str {
        match self {
            State::Closed => "closed",
            State::Open => "open",
            State::HalfOpen => "half_open",
        }
    }

    fn code(self) -> u8 {
        match self {
            State::Closed => 0,
            State::Open => 1,
            State::HalfOpen => 2,
        }
    }
}

impl fmt::Display for State {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

impl Serialize for State {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_u8(self.code())
    }
}

impl<'de> Deserialize<'de> for State {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        match u8::deserialize(d)? {
            0 => Ok(State::Closed),
            1 => Ok(State::Open),
            2 => Ok(State::HalfOpen),
            n => Err(serde::de::Error::custom(format!(
                "unknown breaker state {n}"
            ))),
        }
    }
}

/// What `allow` does when a policy refuses. Go's `Action`.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Action {
    /// Fail closed: `allow` returns [`BrokenCircuit`].
    #[default]
    Halt,
    /// Go answers with its fallback; with none ported, like `Halt`.
    Degrade,
    /// Log `breaker would reject` at warn and allow.
    Warn,
}

impl Action {
    /// `halt`, `degrade` or `warn`.
    pub fn as_str(self) -> &'static str {
        match self {
            Action::Halt => "halt",
            Action::Degrade => "degrade",
            Action::Warn => "warn",
        }
    }
}

/// Circuit tuning. Go's `CircuitOpts`: a zero field keeps its default
/// (5 consecutive failures, 1 probe success, 30s cooldown).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct CircuitOpts {
    /// Consecutive failures that open a closed circuit.
    pub failure_threshold: u32,
    /// Probe successes that close a half-open circuit, and the number
    /// of probes it admits at once.
    pub success_threshold: u32,
    /// How long open lasts before half-open.
    pub delay: Duration,
}

impl Default for CircuitOpts {
    fn default() -> Self {
        CircuitOpts {
            failure_threshold: 5,
            success_threshold: 1,
            delay: Duration::from_secs(30),
        }
    }
}

/// A point-in-time snapshot. Go's `Stats`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Stats {
    /// Manual trips plus automatic opens.
    pub trips: u64,
    /// When the last trip or automatic open happened, by the breaker's clock.
    pub last_trip_at: Option<SystemTime>,
    /// The reason given to `trip`, or `circuit` for an automatic open.
    pub last_trip_reason: String,
    /// `bytes` and `ops` recorded since construction or `reset`; empty
    /// until the first `record`.
    pub counters: BTreeMap<String, u64>,
}

/// `allow`'s refusal. Go's `ErrBrokenCircuit`, whichever policy refused.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct BrokenCircuit;

impl fmt::Display for BrokenCircuit {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("breaker: circuit open")
    }
}

impl std::error::Error for BrokenCircuit {}

/// The breaker's time source: cooldowns, rate periods and trip stamps.
pub trait Clock: Send + Sync {
    /// The current time.
    fn now(&self) -> SystemTime;
}

/// The system clock. Not compiled for wasm32-unknown-unknown, where
/// reading it panics.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[derive(Clone, Copy, Debug, Default)]
pub struct SystemClock;

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
impl Clock for SystemClock {
    #[allow(clippy::disallowed_methods)]
    fn now(&self) -> SystemTime {
        SystemTime::now()
    }
}
