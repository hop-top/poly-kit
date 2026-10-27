//! The circuit state machine and the bursty rate limiter. Go's
//! circuit.go and failsafe-go's bursty limiter; both read the time
//! they are handed, never a clock of their own.

use std::time::{Duration, SystemTime};

use super::State;

/// One state change; logged and published once the lock is released.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) struct Transition {
    pub(super) from: State,
    pub(super) to: State,
    /// Caused by `trip` or `reset` rather than by an outcome or expiry.
    pub(super) manual: bool,
}

/// Count-based consecutive-failure thresholding while closed, a
/// cooldown while open, a bounded number of probes while half-open.
#[derive(Debug)]
pub(super) struct Circuit {
    failure_threshold: u32,
    success_threshold: u32,
    delay: Duration,

    state: State,
    opened_at: SystemTime,
    /// Consecutive failures while closed.
    failures: u32,
    /// Probe successes while half-open.
    successes: u32,
    /// Probes half-open may still admit.
    permits: u32,
}

impl Circuit {
    pub(super) fn new(failure_threshold: u32, success_threshold: u32, delay: Duration) -> Self {
        Circuit {
            failure_threshold,
            success_threshold,
            delay,
            state: State::Closed,
            opened_at: SystemTime::UNIX_EPOCH,
            failures: 0,
            successes: 0,
            permits: 0,
        }
    }

    pub(super) fn state(&self) -> State {
        self.state
    }

    /// Open turns half-open once the cooldown has elapsed, boundary
    /// inclusive. A clock that stepped back counts as no time elapsed.
    pub(super) fn expire(&mut self, now: SystemTime, out: &mut Vec<Transition>) {
        if self.state == State::Open {
            let elapsed = now.duration_since(self.opened_at).unwrap_or_default();
            if elapsed >= self.delay {
                out.push(self.move_to(State::HalfOpen, false, now));
            }
        }
    }

    /// Admits one execution: always while closed, never while open,
    /// up to `success_threshold` probes while half-open.
    pub(super) fn try_acquire(&mut self, now: SystemTime, out: &mut Vec<Transition>) -> bool {
        self.expire(now, out);
        match self.state {
            State::Closed => true,
            State::Open => false,
            State::HalfOpen if self.permits == 0 => false,
            State::HalfOpen => {
                self.permits -= 1;
                true
            }
        }
    }

    /// Returns a permit whose execution a later check refused.
    pub(super) fn release(&mut self) {
        if self.state == State::HalfOpen && self.permits < self.success_threshold {
            self.permits += 1;
        }
    }

    /// Feeds one outcome. Half-open: a failure re-opens, enough
    /// successes close, any other outcome returns its permit. Open
    /// ignores outcomes.
    pub(super) fn record(&mut self, success: bool, now: SystemTime, out: &mut Vec<Transition>) {
        self.expire(now, out);
        match self.state {
            State::Closed if success => self.failures = 0,
            State::Closed => {
                self.failures += 1;
                if self.failures >= self.failure_threshold {
                    out.push(self.move_to(State::Open, false, now));
                }
            }
            State::HalfOpen if !success => out.push(self.move_to(State::Open, false, now)),
            State::HalfOpen => {
                self.successes += 1;
                if self.successes >= self.success_threshold {
                    out.push(self.move_to(State::Closed, false, now));
                } else {
                    self.release();
                }
            }
            State::Open => {}
        }
    }

    /// Forces open; a no-op while open within its cooldown.
    pub(super) fn open(&mut self, now: SystemTime, out: &mut Vec<Transition>) {
        self.expire(now, out);
        if self.state != State::Open {
            out.push(self.move_to(State::Open, true, now));
        }
    }

    /// Forces closed with a fresh failure window, in every state.
    pub(super) fn reset(&mut self, now: SystemTime, out: &mut Vec<Transition>) {
        self.expire(now, out);
        if self.state == State::Closed {
            self.failures = 0;
        } else {
            out.push(self.move_to(State::Closed, true, now));
        }
    }

    fn move_to(&mut self, to: State, manual: bool, now: SystemTime) -> Transition {
        let t = Transition {
            from: self.state,
            to,
            manual,
        };
        self.state = to;
        match to {
            State::Closed => self.failures = 0,
            State::Open => self.opened_at = now,
            State::HalfOpen => {
                self.successes = 0;
                self.permits = self.success_threshold;
            }
        }
        t
    }
}

/// failsafe-go's bursty limiter: `permits` per `period`, periods
/// counted from construction, refilled whole at each new period. A
/// refusal takes nothing.
#[derive(Debug)]
pub(super) struct Bursty {
    permits: u32,
    period: Duration,
    start: SystemTime,
    current_period: u128,
    available: u32,
}

impl Bursty {
    pub(super) fn new(permits: u32, period: Duration, start: SystemTime) -> Self {
        Bursty {
            permits,
            period,
            start,
            current_period: 0,
            available: permits,
        }
    }

    pub(super) fn try_acquire(&mut self, now: SystemTime) -> bool {
        let elapsed = now.duration_since(self.start).unwrap_or_default();
        let period = elapsed.as_nanos() / self.period.as_nanos();
        if period > self.current_period {
            self.current_period = period;
            self.available = self.permits;
        }
        if self.available == 0 {
            return false;
        }
        self.available -= 1;
        true
    }
}
