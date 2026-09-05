//! The six lifecycle transitions, their topic strings, and the two
//! sinks that carry them.

use std::collections::BTreeMap;

/// The 2-segment source.category prefix serve events publish under.
pub const DEFAULT_TOPIC_PREFIX: &str = "kit.serve";

/// Action segment for "a service has been asked to start".
pub const ACTION_STARTED: &str = "started";
/// Action segment for "reported ready".
///
/// The action is `ready_reported`, not a bare `ready`: the bare form
/// fails the past-tense validation in `bus::validate_topic`, so a port
/// emitting it would publish a topic Go subscribers reject.
pub const ACTION_READY_REPORTED: &str = "ready_reported";
/// Action segment for "failed".
pub const ACTION_FAILED: &str = "failed";
/// Action segment for "finished stopping".
pub const ACTION_STOPPED: &str = "stopped";

/// Object segment for a service-scoped transition. The service
/// identifier travels in the payload, not the topic, so subscribers are
/// not forced to re-bind when a tool gains a service.
pub const OBJECT_SERVICE: &str = "service";
/// Object segment for a supervisor-scoped transition.
pub const OBJECT_SUPERVISOR: &str = "supervisor";

/// Payload key carrying the service identifier. Never in the topic.
pub const PAYLOAD_KEY_SERVICE: &str = "service";
/// Payload key carrying the failure reason. Never in the topic.
pub const PAYLOAD_KEY_ERROR: &str = "error";
/// Payload key carrying the resolved listen address, on
/// `ready_reported` only.
pub const PAYLOAD_KEY_ADDRESS: &str = "address";

/// The six `<object>.<action>` keys, in contract-table order.
pub const TRANSITIONS: &[(&str, &str)] = &[
    (OBJECT_SERVICE, ACTION_STARTED),
    (OBJECT_SERVICE, ACTION_READY_REPORTED),
    (OBJECT_SERVICE, ACTION_FAILED),
    (OBJECT_SERVICE, ACTION_STOPPED),
    (OBJECT_SUPERVISOR, ACTION_READY_REPORTED),
    (OBJECT_SUPERVISOR, ACTION_STOPPED),
];

/// The conformant topic set for `prefix`, keyed `<object>.<action>`.
///
/// These strings are contract: a subscriber is written against them and
/// does not know which language published. An empty `prefix` falls back
/// to [`DEFAULT_TOPIC_PREFIX`] rather than producing 2-segment topics
/// the bus would reject.
pub fn default_topics(prefix: &str) -> BTreeMap<String, String> {
    let p = if prefix.is_empty() {
        DEFAULT_TOPIC_PREFIX
    } else {
        prefix
    };
    TRANSITIONS
        .iter()
        .map(|(object, action)| {
            (
                format!("{object}.{action}"),
                format!("{p}.{object}.{action}"),
            )
        })
        .collect()
}

/// The body of every serve lifecycle event.
///
/// Only `service`, `error` and `address` are contract. `reason` and
/// `elapsed_ms` are carried because they are cheap and useful in a
/// startup trace; nothing downstream is specified to read them.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct EventPayload {
    /// Service the event concerns. Absent for supervisor-scoped events.
    pub service: Option<String>,
    /// Failure text for a `failed` event.
    pub error: Option<String>,
    /// Why, for events that have a reason. Not contract.
    pub reason: Option<String>,
    /// Where the service accepts work, on `ready_reported` only.
    pub address: Option<String>,
    /// Milliseconds since the supervisor began the run. Not contract.
    pub elapsed_ms: u128,
}

impl EventPayload {
    /// A payload naming `service` and nothing else.
    pub fn for_service(service: impl Into<String>, elapsed_ms: u128) -> Self {
        EventPayload {
            service: Some(service.into()),
            elapsed_ms,
            ..EventPayload::default()
        }
    }

    /// The payload as JSON, with the contract keys at the top level and
    /// unset optional keys omitted.
    pub fn to_json(&self) -> serde_json::Value {
        let mut map = serde_json::Map::new();
        if let Some(s) = &self.service {
            map.insert(PAYLOAD_KEY_SERVICE.to_string(), s.clone().into());
        }
        if let Some(e) = &self.error {
            map.insert(PAYLOAD_KEY_ERROR.to_string(), e.clone().into());
        }
        if let Some(a) = &self.address {
            map.insert(PAYLOAD_KEY_ADDRESS.to_string(), a.clone().into());
        }
        if let Some(r) = &self.reason {
            map.insert("reason".to_string(), r.clone().into());
        }
        map.insert(
            "elapsed_ms".to_string(),
            serde_json::Value::from(self.elapsed_ms as u64),
        );
        serde_json::Value::Object(map)
    }
}

/// The narrow slice of an event bus the supervisor needs.
///
/// A trait rather than a concrete `Bus` because [`crate::bus::Bus`] is
/// `Rc`-backed and therefore `!Send`, while the supervisor's futures
/// may move between tokio worker threads. An adopter driving the
/// supervisor on a current-thread runtime wires the kit bus behind this
/// trait; anyone else supplies their own sink. Omitting one means
/// events are not published, and the log counterpart still runs.
pub trait Publisher: Send + Sync {
    /// Publishes one lifecycle transition. A publish failure is
    /// swallowed by the caller: an event sink is observability, not
    /// correctness, and must never fail the lifecycle.
    fn publish(&self, topic: &str, source: &str, payload: &EventPayload);
}

/// The narrow slice of a logger the supervisor needs.
///
/// Rust's kit port has no structured logger yet, so the contract's
/// fallback applies: the same four transitions with the same field
/// names go to whatever the port writes to stderr. `keyvals` arrives
/// already flattened into `(key, value)` pairs so an implementation
/// that *does* have structured logging emits them as fields rather
/// than interpolating them into the message.
pub trait ServeLogger: Send + Sync {
    fn info(&self, msg: &str, keyvals: &[(&str, String)]);
    fn error(&self, msg: &str, keyvals: &[(&str, String)]);
}

/// A [`ServeLogger`] writing `key=value` pairs to stderr.
///
/// The contract's floor for a port with neither a bus nor a structured
/// logger: never silent about a service that started, became ready,
/// failed, or stopped, and the identifier and address are separable
/// fields rather than prose.
#[derive(Clone, Copy, Debug, Default)]
pub struct StderrLogger;

impl ServeLogger for StderrLogger {
    fn info(&self, msg: &str, keyvals: &[(&str, String)]) {
        eprintln!("level=info msg={msg:?}{}", render_keyvals(keyvals));
    }

    fn error(&self, msg: &str, keyvals: &[(&str, String)]) {
        eprintln!("level=error msg={msg:?}{}", render_keyvals(keyvals));
    }
}

fn render_keyvals(keyvals: &[(&str, String)]) -> String {
    let mut out = String::new();
    for (k, v) in keyvals {
        out.push(' ');
        out.push_str(k);
        out.push('=');
        out.push_str(&format!("{v:?}"));
    }
    out
}

/// Publishes one lifecycle transition to both sinks.
pub(crate) struct Emitter {
    topics: BTreeMap<String, String>,
    source: String,
    publisher: Option<std::sync::Arc<dyn Publisher>>,
    logger: Option<std::sync::Arc<dyn ServeLogger>>,
}

impl Emitter {
    pub(crate) fn new(
        topics: BTreeMap<String, String>,
        source: String,
        publisher: Option<std::sync::Arc<dyn Publisher>>,
        logger: Option<std::sync::Arc<dyn ServeLogger>>,
    ) -> Self {
        Emitter {
            topics,
            source,
            publisher,
            logger,
        }
    }

    pub(crate) fn emit(&self, object: &str, action: &str, payload: &EventPayload) {
        self.log_event(object, action, payload);
        let Some(pubr) = &self.publisher else {
            return;
        };
        let Some(topic) = self.topics.get(&format!("{object}.{action}")) else {
            return;
        };
        // An event sink is observability, not correctness: a sink that
        // panics must not take the lifecycle down with it. The
        // supervisor would otherwise unwind mid-shutdown and leave
        // services running.
        let outcome = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            pubr.publish(topic, &self.source, payload);
        }));
        if outcome.is_err() {
            // Deliberately silent beyond the log sink: reporting a
            // publish failure through the publisher would be the same
            // trap again.
            self.note_publish_failure(topic);
        }
    }

    fn note_publish_failure(&self, topic: &str) {
        let Some(log) = &self.logger else {
            return;
        };
        log.error(
            "serve: event publish failed",
            &[("topic", topic.to_string())],
        );
    }

    fn log_event(&self, object: &str, action: &str, payload: &EventPayload) {
        let Some(log) = &self.logger else {
            return;
        };
        let mut kv: Vec<(&str, String)> = vec![
            ("object", object.to_string()),
            ("elapsed_ms", payload.elapsed_ms.to_string()),
        ];
        if let Some(s) = &payload.service {
            kv.push((PAYLOAD_KEY_SERVICE, s.clone()));
        }
        if let Some(a) = &payload.address {
            kv.push((PAYLOAD_KEY_ADDRESS, a.clone()));
        }
        if let Some(r) = &payload.reason {
            kv.push(("reason", r.clone()));
        }
        let msg = format!("serve: {action}");
        if action == ACTION_FAILED {
            if let Some(e) = &payload.error {
                kv.push((PAYLOAD_KEY_ERROR, e.clone()));
            }
            log.error(&msg, &kv);
            return;
        }
        log.info(&msg, &kv);
    }
}
