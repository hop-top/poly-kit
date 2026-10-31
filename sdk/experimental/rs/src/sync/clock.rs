//! The hybrid logical clock and the wall clocks under it. Go's clock.go
//! and wallclock.go.
//!
//! [`Clock`] orders events across nodes: a [`Timestamp`] is wall time in
//! nanoseconds, a logical counter and the node that stamped it. The
//! wall time comes from a [`WallClock`], so the clock is pure given one:
//! wasm32-unknown-unknown has no system clock, and there a caller
//! supplies the time (`Date.now()`, say) through its own [`WallClock`].
//!
//! # Where this differs from Go
//!
//! - Go's `time.Time` is a [`SystemTime`]. A time before the Unix epoch
//!   is stamped as negative nanoseconds; one beyond `i64` nanoseconds
//!   (year 2262) saturates, where Go's `UnixNano` is undefined.
//! - Go's `System` global and nil-means-system fallback become
//!   [`SystemWallClock`] and [`Clock::new`]; a [`Clock`] built with
//!   [`Clock::with_wall_clock`] always has a clock.
//! - [`SystemWallClock`] and [`Clock::new`] are not compiled for
//!   wasm32-unknown-unknown.

use std::cmp::Ordering;
use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};

/// A hybrid logical clock value: wall time, a logical counter and the
/// originating node. Go's `Timestamp`; the JSON keys are `physical`,
/// `logical` and `node_id`, which other ports must agree with to order
/// events.
#[derive(Clone, Debug, Default, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(default)]
pub struct Timestamp {
    /// Wall clock, in nanoseconds since the Unix epoch.
    pub physical: i64,
    /// Breaks ties between events sharing a physical time.
    pub logical: u32,
    /// The node that stamped it; the final tie-break.
    pub node_id: String,
}

impl Timestamp {
    /// Whether `self` precedes `other` in causal order: by physical
    /// time, then logical counter, then node id. Go's `Before`.
    pub fn before(&self, other: &Timestamp) -> bool {
        self.cmp(other) == Ordering::Less
    }

    /// Whether the two are the same timestamp. Go's `Equal`.
    pub fn equal(&self, other: &Timestamp) -> bool {
        self == other
    }
}

impl Ord for Timestamp {
    fn cmp(&self, other: &Self) -> Ordering {
        self.physical
            .cmp(&other.physical)
            .then(self.logical.cmp(&other.logical))
            .then_with(|| self.node_id.cmp(&other.node_id))
    }
}

impl PartialOrd for Timestamp {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

/// The source of physical wall-clock time, so callers can substitute a
/// deterministic one. Go's `WallClock`.
///
/// Distinct from [`Clock`], which returns a [`Timestamp`] fit for
/// causal ordering; a `WallClock` returns only the instant.
pub trait WallClock: Send + Sync {
    /// The current wall-clock time.
    fn wall_time(&self) -> SystemTime;
}

/// The production [`WallClock`], backed by [`SystemTime::now`]. Go's
/// `SystemWallClock`. Not compiled for wasm32-unknown-unknown, where
/// that read panics: give one of your own.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[derive(Clone, Copy, Debug, Default)]
pub struct SystemWallClock;

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
impl WallClock for SystemWallClock {
    // The one sanctioned clock read in sync: compiled out on
    // wasm32-unknown-unknown, where CI denies `SystemTime::now`.
    #[allow(clippy::disallowed_methods)]
    fn wall_time(&self) -> SystemTime {
        SystemTime::now()
    }
}

/// A [`WallClock`] that always reports one instant. Go's `FixedClock`.
#[derive(Clone, Copy, Debug)]
pub struct FixedClock(SystemTime);

impl FixedClock {
    /// A clock stuck at `t`.
    pub fn new(t: SystemTime) -> Self {
        FixedClock(t)
    }
}

impl WallClock for FixedClock {
    fn wall_time(&self) -> SystemTime {
        self.0
    }
}

/// A thread-safe [`WallClock`] moved only by [`MockWallClock::advance`].
/// Go's `MockWallClock`.
#[derive(Debug)]
pub struct MockWallClock {
    now: Mutex<SystemTime>,
}

impl MockWallClock {
    /// A clock reading `start`.
    pub fn new(start: SystemTime) -> Self {
        MockWallClock {
            now: Mutex::new(start),
        }
    }

    /// Shifts the clock forward by `d`.
    pub fn advance(&self, d: Duration) {
        *lock(&self.now) += d;
    }
}

impl WallClock for MockWallClock {
    fn wall_time(&self) -> SystemTime {
        *lock(&self.now)
    }
}

/// A hybrid logical clock bound to one node. Go's `Clock`.
pub struct Clock {
    node_id: String,
    wall: Arc<dyn WallClock>,
    last: Mutex<Timestamp>,
}

impl Clock {
    /// A clock for `node_id` on the process's system wall clock.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    pub fn new(node_id: impl Into<String>) -> Self {
        Self::with_wall_clock(node_id, Arc::new(SystemWallClock))
    }

    /// A clock for `node_id` on `wall`. Pass a [`FixedClock`] or a
    /// [`MockWallClock`] in tests. Go's `NewClockWithWallClock`.
    pub fn with_wall_clock(node_id: impl Into<String>, wall: Arc<dyn WallClock>) -> Self {
        Clock {
            node_id: node_id.into(),
            wall,
            last: Mutex::new(Timestamp::default()),
        }
    }

    /// A new, strictly increasing timestamp.
    pub fn now(&self) -> Timestamp {
        let mut last = lock(&self.last);
        let wall = unix_nanos(self.wall.wall_time());
        if wall > last.physical {
            *last = Timestamp {
                physical: wall,
                logical: 0,
                node_id: self.node_id.clone(),
            };
        } else {
            last.logical = last.logical.wrapping_add(1);
            last.node_id.clone_from(&self.node_id);
        }
        last.clone()
    }

    /// Folds a remote timestamp into the local state and returns the
    /// new local timestamp, which follows both.
    pub fn update(&self, remote: &Timestamp) -> Timestamp {
        let mut last = lock(&self.last);
        let wall = unix_nanos(self.wall.wall_time());
        let max_phys = wall.max(last.physical).max(remote.physical);

        let logical = if max_phys == last.physical && max_phys == remote.physical {
            last.logical.max(remote.logical).wrapping_add(1)
        } else if max_phys == last.physical {
            last.logical.wrapping_add(1)
        } else if max_phys == remote.physical {
            remote.logical.wrapping_add(1)
        } else {
            0
        };

        *last = Timestamp {
            physical: max_phys,
            logical,
            node_id: self.node_id.clone(),
        };
        last.clone()
    }

    /// The node this clock stamps for.
    pub fn node_id(&self) -> &str {
        &self.node_id
    }
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

/// Nanoseconds since the Unix epoch: negative before it, saturating at
/// the `i64` ends.
pub(crate) fn unix_nanos(t: SystemTime) -> i64 {
    match t.duration_since(UNIX_EPOCH) {
        Ok(d) => i64::try_from(d.as_nanos()).unwrap_or(i64::MAX),
        Err(e) => i64::try_from(e.duration().as_nanos()).map_or(i64::MIN, |n| -n),
    }
}
