//! Migration lifecycle events. Go `migrate_events.go`: the same topics,
//! source and payload fields, published synchronously in order.

use serde_json::{Map, Value};

use crate::bus::{prefix_topics, Bus, Context, DynBus, Event, SharedBus, Topic};

/// Event source, as Go's `publishSource`.
pub const SOURCE: &str = "core.upgrade";

/// The actions a prefix expands with, in Go's order.
const ACTIONS: [&str; 8] = [
    "run_started",
    "run_completed",
    "run_failed",
    "started",
    "applied",
    "verified",
    "failed",
    "reverted",
];

/// The topics a [`Migrator`](super::Migrator) publishes on. Go
/// `MigrationTopics`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct MigrationTopics {
    pub run_started: Topic,
    pub run_completed: Topic,
    pub run_failed: Topic,
    pub started: Topic,
    pub applied: Topic,
    pub verified: Topic,
    pub failed: Topic,
    pub reverted: Topic,
}

impl MigrationTopics {
    /// `<prefix>.<action>` for every action. Go
    /// `WithMigrationTopicPrefix`.
    ///
    /// # Panics
    ///
    /// When [`prefix_topics`] rejects `prefix`, as Go panics.
    pub fn from_prefix(prefix: &str) -> MigrationTopics {
        let (mut map, err) = prefix_topics(prefix, &ACTIONS);
        if let Some(err) = err {
            panic!("upgrade.WithMigrationTopicPrefix({prefix:?}): {err}");
        }
        let mut take = |a: &str| map.remove(a).expect("prefix_topics maps every action");
        MigrationTopics {
            run_started: take("run_started"),
            run_completed: take("run_completed"),
            run_failed: take("run_failed"),
            started: take("started"),
            applied: take("applied"),
            verified: take("verified"),
            failed: take("failed"),
            reverted: take("reverted"),
        }
    }

    /// Empty fields take the default topic. Go `WithMigrationTopics`.
    pub fn or_default(self) -> MigrationTopics {
        let d = MigrationTopics::default();
        let pick = |t: Topic, def: Topic| if t.as_str().is_empty() { def } else { t };
        MigrationTopics {
            run_started: pick(self.run_started, d.run_started),
            run_completed: pick(self.run_completed, d.run_completed),
            run_failed: pick(self.run_failed, d.run_failed),
            started: pick(self.started, d.started),
            applied: pick(self.applied, d.applied),
            verified: pick(self.verified, d.verified),
            failed: pick(self.failed, d.failed),
            reverted: pick(self.reverted, d.reverted),
        }
    }
}

impl Default for MigrationTopics {
    /// `kit.core.migration.<action>`. Go `DefaultMigrationTopics`.
    fn default() -> Self {
        MigrationTopics::from_prefix("kit.core.migration")
    }
}

/// Where a migrator's events go. Implemented for the kit buses and for
/// any `Fn(&Context, &Event)`. Publish errors are the sink's to drop:
/// they never fail a run.
pub trait MigrationPublisher: Send + Sync {
    fn publish(&self, ctx: &Context, event: &Event);
}

impl<F: Fn(&Context, &Event) + Send + Sync> MigrationPublisher for F {
    fn publish(&self, ctx: &Context, event: &Event) {
        self(ctx, event)
    }
}

impl MigrationPublisher for DynBus {
    fn publish(&self, ctx: &Context, event: &Event) {
        let _ = self.publish_ctx(ctx, event);
    }
}

impl MigrationPublisher for SharedBus {
    fn publish(&self, ctx: &Context, event: &Event) {
        let _ = self.publish_ctx(ctx, event);
    }
}

impl MigrationPublisher for Bus {
    fn publish(&self, ctx: &Context, event: &Event) {
        let _ = self.publish_ctx(ctx, event);
    }
}

/// Builds a payload object holding only the fields set, as Go's
/// `omitempty` leaves the rest out.
pub(super) struct Payload(Map<String, Value>);

impl Payload {
    pub(super) fn new(tool: &str) -> Payload {
        let mut m = Map::new();
        m.insert("tool".into(), Value::from(tool));
        Payload(m)
    }

    pub(super) fn set(mut self, key: &str, value: impl Into<Value>) -> Payload {
        self.0.insert(key.into(), value.into());
        self
    }

    pub(super) fn into_value(self) -> Value {
        Value::Object(self.0)
    }
}
