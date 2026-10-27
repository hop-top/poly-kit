//! [`Breaker`] and its [`Builder`]. Go's breaker.go and options.go.

use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, SystemTime};

use crate::bus::{Context, Spawn, Topic};
use crate::domain::{EventPublisher, Payload};

use super::circuit::{Bursty, Circuit, Transition};
use super::events::{spawn_default, Level, Logger, Record, StderrLogger, Topics, Value, SOURCE};
use super::{
    Action, BrokenCircuit, CircuitOpts, Clock, State, Stats, TransitionPayload, TrippedPayload,
};

/// `Stats::last_trip_reason` of an automatic open, and the
/// `breaker.policy` of every transition.
const CIRCUIT: &str = "circuit";

/// A circuit breaker with optional budgets and rate limit. Go's
/// `Breaker`. `Send + Sync`; share it behind an `Arc`.
///
/// Every `allow` that returns `Ok` should be followed by one `record`
/// of the outcome: a half-open circuit admits a probe per permit and
/// gets the permit back from `record`.
pub struct Breaker {
    name: String,
    action: Action,
    max_ops: Option<u64>,
    max_bytes: Option<u64>,
    publisher: Option<Arc<dyn EventPublisher>>,
    topics: Topics,
    logger: Arc<dyn Logger>,
    clock: Arc<dyn Clock>,
    spawner: Option<Arc<dyn Spawn>>,
    core: Mutex<Core>,
}

struct Core {
    circuit: Circuit,
    rate: Option<Bursty>,
    stats: Stats,
}

/// What a call emits, in order, once the lock is released.
enum Emit {
    Log(Record),
    Event(Topic, Payload),
}

impl Breaker {
    /// A builder for a breaker named `name`, with Go's defaults.
    pub fn builder(name: impl Into<String>) -> Builder {
        Builder::new(name.into())
    }

    /// The name given to the builder; the `breaker.name` log field.
    pub fn name(&self) -> &str {
        &self.name
    }

    /// Asks the circuit for a permit, then the rate limiter, the byte
    /// budget and the op budget, in that order. Records nothing on the
    /// circuit; a permit is returned when a later check refuses.
    ///
    /// # Errors
    ///
    /// [`BrokenCircuit`] when a policy refuses and the action is not
    /// [`Action::Warn`].
    pub fn allow(&self) -> Result<(), BrokenCircuit> {
        let now = self.clock.now();
        let mut out = Vec::new();
        let mut core = self.lock();
        let mut ts = Vec::new();
        let admitted = core.circuit.try_acquire(now, &mut ts);
        self.transitions(&mut core, ts, now, &mut out);
        let refused = if admitted {
            let policy = self.preflight(&mut core, now);
            if policy.is_some() {
                core.circuit.release();
            }
            policy
        } else {
            Some(CIRCUIT)
        };
        let result = match (refused, self.action) {
            (None, _) => Ok(()),
            (Some(policy), Action::Warn) => {
                out.push(Emit::Log(Record {
                    level: Level::Warn,
                    message: "breaker would reject",
                    fields: vec![
                        ("breaker.name", self.str(&self.name)),
                        ("breaker.state", self.str(core.circuit.state().as_str())),
                        ("breaker.policy", self.str(policy)),
                        ("breaker.action", self.str(Action::Warn.as_str())),
                    ],
                }));
                Ok(())
            }
            (Some(_), _) => Err(BrokenCircuit),
        };
        drop(core);
        self.flush(out);
        result
    }

    /// Feeds one outcome to the circuit and adds `n` bytes and one op
    /// to the budgets' counters.
    pub fn record(&self, success: bool, n: u64) {
        let now = self.clock.now();
        let mut out = Vec::new();
        let mut core = self.lock();
        let counters = &mut core.stats.counters;
        *counters.entry("bytes".into()).or_insert(0) += n;
        *counters.entry("ops".into()).or_insert(0) += 1;
        let mut ts = Vec::new();
        core.circuit.record(success, now, &mut ts);
        self.transitions(&mut core, ts, now, &mut out);
        drop(core);
        self.flush(out);
    }

    /// The current state, cooldown expiry applied.
    pub fn state(&self) -> State {
        let now = self.clock.now();
        let mut out = Vec::new();
        let mut core = self.lock();
        let mut ts = Vec::new();
        core.circuit.expire(now, &mut ts);
        self.transitions(&mut core, ts, now, &mut out);
        let state = core.circuit.state();
        drop(core);
        self.flush(out);
        state
    }

    /// Closes the circuit with a fresh failure window and zeroes the
    /// budgets' counters. Trips and the rate limiter are kept.
    pub fn reset(&self) {
        let now = self.clock.now();
        let mut out = Vec::new();
        let mut core = self.lock();
        core.stats.counters.clear();
        out.push(Emit::Log(Record {
            level: Level::Info,
            message: "breaker reset",
            fields: vec![
                ("breaker.name", self.str(&self.name)),
                ("breaker.state", self.str(State::Closed.as_str())),
                ("breaker.policy", self.str("manual")),
            ],
        }));
        let mut ts = Vec::new();
        core.circuit.reset(now, &mut ts);
        self.transitions(&mut core, ts, now, &mut out);
        drop(core);
        self.flush(out);
    }

    /// Opens the circuit by hand, holding it for a full cooldown, and
    /// counts a trip with `reason`.
    pub fn trip(&self, reason: &str) {
        let now = self.clock.now();
        let mut out = Vec::new();
        let mut core = self.lock();
        let mut ts = Vec::new();
        core.circuit.expire(now, &mut ts);
        self.transitions(&mut core, ts, now, &mut out);
        let from = core.circuit.state();
        count_trip(&mut core.stats, reason, now);
        out.push(Emit::Log(Record {
            level: Level::Error,
            message: "breaker tripped manually",
            fields: vec![
                ("breaker.name", self.str(&self.name)),
                ("breaker.state", self.str(State::Open.as_str())),
                ("breaker.reason", self.str(reason)),
                ("breaker.policy", self.str("manual")),
                ("breaker.trips_total", Value::U64(core.stats.trips)),
            ],
        }));
        if self.publisher.is_some() {
            let payload = Payload::BreakerTripped(TrippedPayload {
                reason: reason.to_string(),
                from,
                to: State::Open,
            });
            out.push(Emit::Event(self.topics.tripped.clone(), payload));
        }
        let mut ts = Vec::new();
        core.circuit.open(now, &mut ts);
        self.transitions(&mut core, ts, now, &mut out);
        drop(core);
        self.flush(out);
    }

    /// A snapshot of trips and counters.
    pub fn stats(&self) -> Stats {
        self.lock().stats.clone()
    }

    fn lock(&self) -> MutexGuard<'_, Core> {
        // A panic in a Logger or Clock cannot leave Core half-updated
        // in a way later calls trip over, so poisoning is ignored.
        self.core.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn str(&self, s: &str) -> Value {
        Value::Str(s.to_string())
    }

    /// The first pre-flight policy that refuses, by its log name.
    fn preflight(&self, core: &mut Core, now: SystemTime) -> Option<&'static str> {
        if let Some(rate) = &mut core.rate {
            if !rate.try_acquire(now) {
                return Some("rate");
            }
        }
        let counter = |k: &str| core.stats.counters.get(k).copied().unwrap_or(0);
        if self.max_bytes.is_some_and(|max| counter("bytes") >= max) {
            return Some("bytes");
        }
        if self.max_ops.is_some_and(|max| counter("ops") >= max) {
            return Some("ops");
        }
        None
    }

    /// Turns circuit transitions into a log record and, with a
    /// publisher, an event each. An automatic open counts as a trip.
    fn transitions(
        &self,
        core: &mut Core,
        ts: Vec<Transition>,
        now: SystemTime,
        out: &mut Vec<Emit>,
    ) {
        for t in ts {
            if t.to == State::Open && !t.manual {
                count_trip(&mut core.stats, CIRCUIT, now);
            }
            out.push(Emit::Log(Record {
                level: level(t.from, t.to),
                message: "breaker state transition",
                fields: vec![
                    ("breaker.name", self.str(&self.name)),
                    ("breaker.state", self.str(t.to.as_str())),
                    ("breaker.prev_state", self.str(t.from.as_str())),
                    ("breaker.policy", self.str(CIRCUIT)),
                    ("breaker.trips_total", Value::U64(core.stats.trips)),
                ],
            }));
            if self.publisher.is_some() {
                let payload = Payload::BreakerTransition(TransitionPayload {
                    from: t.from,
                    to: t.to,
                    trigger: "auto".into(),
                });
                out.push(Emit::Event(self.topics.for_state(t.to).clone(), payload));
            }
        }
    }

    /// Logs on the caller's thread; publishes fire-and-forget.
    fn flush(&self, out: Vec<Emit>) {
        for e in out {
            match e {
                Emit::Log(r) => self.logger.log(&r),
                Emit::Event(topic, payload) => {
                    let Some(publisher) = &self.publisher else {
                        continue;
                    };
                    let publisher = Arc::clone(publisher);
                    let publish = move || {
                        let ctx = Context::background();
                        let _ = publisher.publish(&ctx, topic.as_str(), SOURCE, &payload);
                    };
                    match &self.spawner {
                        Some(s) => s.spawn(Box::pin(async move { publish() })),
                        None => spawn_default(publish),
                    }
                }
            }
        }
    }
}

fn count_trip(stats: &mut Stats, reason: &str, now: SystemTime) {
    stats.trips += 1;
    stats.last_trip_at = Some(now);
    stats.last_trip_reason = reason.to_string();
}

/// Go's transitionLevel.
fn level(from: State, to: State) -> Level {
    match (from, to) {
        (State::Closed, State::Open) => Level::Error,
        (State::HalfOpen, State::Open) | (State::Closed, State::HalfOpen) => Level::Warn,
        _ => Level::Info,
    }
}

/// Configures a [`Breaker`]. Go's options, as builder methods.
#[must_use]
pub struct Builder {
    name: String,
    circuit: CircuitOpts,
    max_ops: Option<u64>,
    max_bytes: Option<u64>,
    rate: Option<(u32, Duration)>,
    action: Action,
    publisher: Option<Arc<dyn EventPublisher>>,
    topics: Topics,
    logger: Arc<dyn Logger>,
    clock: Option<Arc<dyn Clock>>,
    spawner: Option<Arc<dyn Spawn>>,
}

impl Builder {
    fn new(name: String) -> Self {
        Builder {
            name,
            circuit: CircuitOpts::default(),
            max_ops: None,
            max_bytes: None,
            rate: None,
            action: Action::Halt,
            publisher: None,
            topics: Topics::default(),
            logger: Arc::new(StderrLogger),
            clock: None,
            spawner: None,
        }
    }

    /// Circuit tuning; zero fields keep their defaults. Go's `WithCircuit`.
    pub fn circuit(mut self, opts: CircuitOpts) -> Self {
        if opts.failure_threshold > 0 {
            self.circuit.failure_threshold = opts.failure_threshold;
        }
        if opts.success_threshold > 0 {
            self.circuit.success_threshold = opts.success_threshold;
        }
        if !opts.delay.is_zero() {
            self.circuit.delay = opts.delay;
        }
        self
    }

    /// The cooldown before half-open. Go's `ResetAfter`.
    pub fn reset_after(mut self, delay: Duration) -> Self {
        self.circuit.delay = delay;
        self
    }

    /// Refuses once `n` outcomes were recorded. Go's `MaxOps`.
    pub fn max_ops(mut self, n: u64) -> Self {
        self.max_ops = Some(n);
        self
    }

    /// Refuses once `n` bytes were recorded. Go's `MaxBytes`.
    pub fn max_bytes(mut self, n: u64) -> Self {
        self.max_bytes = Some(n);
        self
    }

    /// At most `n` admissions per `period`, bursty: the whole allowance
    /// is available at the start of each period, counted from
    /// construction. Go's `MaxPerInterval`.
    pub fn max_per_interval(mut self, n: u32, period: Duration) -> Self {
        self.rate = Some((n, period));
        self
    }

    /// `max_per_interval(n, 1 minute)`. Go's `MaxPerMinute`.
    pub fn max_per_minute(self, n: u32) -> Self {
        self.max_per_interval(n, Duration::from_secs(60))
    }

    /// What `allow` does with a refusal. Go's `OnTrip`.
    pub fn on_trip(mut self, action: Action) -> Self {
        self.action = action;
        self
    }

    /// Publishes lifecycle events; none without one. Go's `WithPublisher`.
    pub fn publisher(mut self, publisher: Arc<dyn EventPublisher>) -> Self {
        self.publisher = Some(publisher);
        self
    }

    /// All four topics from a 3-segment prefix. Go's `WithTopicPrefix`.
    ///
    /// # Panics
    ///
    /// As [`Topics::from_prefix`].
    pub fn topic_prefix(mut self, prefix: &str) -> Self {
        self.topics = Topics::from_prefix(prefix);
        self
    }

    /// Per-moment topics; an empty field keeps the default. Go's `WithTopics`.
    pub fn topics(mut self, topics: Topics) -> Self {
        self.topics = topics.or_defaults();
        self
    }

    /// Where log records go; [`StderrLogger`] by default. Go's `Logger`.
    pub fn logger(mut self, logger: Arc<dyn Logger>) -> Self {
        self.logger = logger;
        self
    }

    /// The time source; [`SystemClock`](super::SystemClock) by default,
    /// required on wasm32-unknown-unknown. Go's `WithClock`.
    pub fn clock(mut self, clock: Arc<dyn Clock>) -> Self {
        self.clock = Some(clock);
        self
    }

    /// Runs each publish as a task on `spawner` instead of a thread of
    /// its own (inline on wasm32-unknown-unknown).
    pub fn spawner(mut self, spawner: Arc<dyn Spawn>) -> Self {
        self.spawner = Some(spawner);
        self
    }

    /// The breaker.
    ///
    /// # Panics
    ///
    /// On an empty name, a zero budget, a zero rate period, or no clock
    /// on wasm32-unknown-unknown: wiring errors, caught at boot.
    pub fn build(self) -> Breaker {
        assert!(!self.name.is_empty(), "breaker: name is required");
        assert!(
            self.max_ops != Some(0) && self.max_bytes != Some(0),
            "breaker/policy: max must be > 0"
        );
        let clock = self.clock.unwrap_or_else(default_clock);
        let rate = self.rate.map(|(n, period)| {
            assert!(!period.is_zero(), "breaker: rate period must be > 0");
            Bursty::new(n, period, clock.now())
        });
        let c = self.circuit;
        Breaker {
            name: self.name,
            action: self.action,
            max_ops: self.max_ops,
            max_bytes: self.max_bytes,
            publisher: self.publisher,
            topics: self.topics,
            logger: self.logger,
            clock,
            spawner: self.spawner,
            core: Mutex::new(Core {
                circuit: Circuit::new(c.failure_threshold, c.success_threshold, c.delay),
                rate,
                stats: Stats::default(),
            }),
        }
    }
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn default_clock() -> Arc<dyn Clock> {
    Arc::new(super::SystemClock)
}

#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
fn default_clock() -> Arc<dyn Clock> {
    panic!("breaker: no system clock on wasm32-unknown-unknown; set one with Builder::clock")
}
