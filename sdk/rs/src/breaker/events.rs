//! What a breaker emits: log records through a [`Logger`] and bus
//! events through a [`domain::EventPublisher`](crate::domain::EventPublisher).
//! Go's log.go and bus.go.

use std::fmt;

use serde::{Deserialize, Serialize};

use crate::bus::{prefix_topics, Topic};
use crate::domain::quote::quote;

use super::State;

/// The `source` every breaker event is published with.
pub const SOURCE: &str = "core.breaker";

/// The kit baseline topic of a manual trip.
pub const DEFAULT_TRIPPED: &str = "kit.core.breaker.tripped";
/// The kit baseline topic of an automatic move to open.
pub const DEFAULT_OPENED: &str = "kit.core.breaker.opened";
/// The kit baseline topic of a move to closed.
pub const DEFAULT_CLOSED: &str = "kit.core.breaker.closed";
/// The kit baseline topic of a move to half-open.
pub const DEFAULT_HALF_OPENED: &str = "kit.core.breaker.half_opened";

/// The actions a prefix is expanded with, in Go's order, so the first
/// failing action reported is the same.
const ACTIONS: [&str; 4] = ["tripped", "opened", "closed", "half_opened"];

/// The per-moment topics. Go's `Topics`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Topics {
    /// A manual [`trip`](super::Breaker::trip).
    pub tripped: Topic,
    /// Every move to open, the one a manual trip causes included.
    pub opened: Topic,
    /// Every move to closed.
    pub closed: Topic,
    /// Every move to half-open.
    pub half_opened: Topic,
}

impl Default for Topics {
    fn default() -> Self {
        Topics {
            tripped: Topic::from(DEFAULT_TRIPPED),
            opened: Topic::from(DEFAULT_OPENED),
            closed: Topic::from(DEFAULT_CLOSED),
            half_opened: Topic::from(DEFAULT_HALF_OPENED),
        }
    }
}

impl Topics {
    /// All four from a 3-segment prefix, `source.category.object`:
    /// `<prefix>.tripped`, `.opened`, `.closed`, `.half_opened`.
    ///
    /// # Panics
    ///
    /// If [`prefix_topics`] rejects `prefix`, with Go's text:
    /// `breaker.WithTopicPrefix("<prefix>"): <reason>`.
    pub fn from_prefix(prefix: &str) -> Self {
        let (mut map, err) = prefix_topics(prefix, &ACTIONS);
        if let Some(err) = err {
            panic!("breaker.WithTopicPrefix({}): {err}", quote(prefix));
        }
        let mut take = |action: &str| map.remove(action).expect("prefix_topics maps every action");
        Topics {
            tripped: take(ACTIONS[0]),
            opened: take(ACTIONS[1]),
            closed: take(ACTIONS[2]),
            half_opened: take(ACTIONS[3]),
        }
    }

    /// `self` with every empty field replaced by the default. Go's
    /// `WithTopics`.
    pub(super) fn or_defaults(self) -> Self {
        let d = Topics::default();
        let pick = |t: Topic, d: Topic| if t.as_str().is_empty() { d } else { t };
        Topics {
            tripped: pick(self.tripped, d.tripped),
            opened: pick(self.opened, d.opened),
            closed: pick(self.closed, d.closed),
            half_opened: pick(self.half_opened, d.half_opened),
        }
    }

    pub(super) fn for_state(&self, to: State) -> &Topic {
        match to {
            State::Open => &self.opened,
            State::HalfOpen => &self.half_opened,
            State::Closed => &self.closed,
        }
    }
}

/// The payload of a manual trip. Go's `TrippedPayload`; keys `Reason`,
/// `From`, `To`, states as Go's ints.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct TrippedPayload {
    /// The reason given to `trip`.
    pub reason: String,
    /// The state before the trip.
    pub from: State,
    /// Always [`State::Open`].
    pub to: State,
}

/// The payload of a state change. Go's breaker `TransitionPayload`;
/// keys `From`, `To`, `Trigger`.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub struct TransitionPayload {
    /// The state left.
    pub from: State,
    /// The state entered.
    pub to: State,
    /// `auto`: every transition the circuit makes, manual ones included.
    pub trigger: String,
}

/// A log record's severity.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Level {
    /// Recovery and expiry.
    Info,
    /// Re-opening, and refusals `OnTrip(Warn)` lets through.
    Warn,
    /// Opening, and manual trips.
    Error,
}

impl Level {
    /// `info`, `warn` or `error`, as Go's charm/log JSON writes it.
    pub fn as_str(self) -> &'static str {
        match self {
            Level::Info => "info",
            Level::Warn => "warn",
            Level::Error => "error",
        }
    }
}

/// A log field value.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Value {
    /// A string field.
    Str(String),
    /// `breaker.trips_total`.
    U64(u64),
}

impl fmt::Display for Value {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Value::Str(s) => write!(f, "{s:?}"),
            Value::U64(n) => write!(f, "{n}"),
        }
    }
}

/// One structured log record: Go's message and key/value pairs, keys
/// such as `breaker.name` and `breaker.state` in Go's order.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Record {
    /// Severity.
    pub level: Level,
    /// `breaker state transition`, `breaker tripped manually`,
    /// `breaker reset` or `breaker would reject`.
    pub message: &'static str,
    /// The fields, in order.
    pub fields: Vec<(&'static str, Value)>,
}

/// Where a breaker's log records go. Called on the caller's thread,
/// with no lock held. Any `Fn(&Record)` closure is one.
pub trait Logger: Send + Sync {
    /// Writes one record.
    fn log(&self, record: &Record);
}

impl<F: Fn(&Record) + Send + Sync> Logger for F {
    fn log(&self, record: &Record) {
        self(record)
    }
}

/// The default [`Logger`]: logfmt-style lines on stderr, the port's
/// stand-in for Go's kit/log default.
#[derive(Clone, Copy, Debug, Default)]
pub struct StderrLogger;

impl Logger for StderrLogger {
    fn log(&self, record: &Record) {
        let mut line = format!("level={} msg={:?}", record.level.as_str(), record.message);
        for (k, v) in &record.fields {
            line.push_str(&format!(" {k}={v}"));
        }
        eprintln!("{line}");
    }
}

/// Go's `go func() { ... }()`: a thread of its own; the event is
/// dropped if one cannot be started.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub(super) fn spawn_default(publish: impl FnOnce() + Send + 'static) {
    let _ = std::thread::Builder::new()
        .name("breaker-publish".into())
        .spawn(publish);
}

/// No threads on this target: run inline.
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
pub(super) fn spawn_default(publish: impl FnOnce() + Send + 'static) {
    publish();
}
