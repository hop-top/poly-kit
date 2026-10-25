//! The [`Event`] envelope carried by the bus.
//!
//! Mirrors `go/runtime/bus/event.go`. JSON keys are lowercase, as
//! there — cross-process subscribers parse lowercase, so
//! capitalized keys would break them. `workspace_id` is snake_case and
//! omitted when empty, for backward compatibility with v0.1 publishers.

use std::time::{SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Serialize};
use serde_json::Value;

use super::topic::Topic;

/// The standard envelope for all bus messages.
///
/// # Payload typing
///
/// The Go envelope types `Payload` as `any`, letting in-process
/// subscribers receive the original Go value while cross-process
/// subscribers see the JSON-decoded form. Rust has no equivalent
/// erasure that survives a wire hop, so [`Event::payload`] is a
/// [`serde_json::Value`] unconditionally: in-process and cross-process
/// subscribers see the same shape, and typed consumers recover their
/// struct with `serde_json::from_value`.
///
/// Publishers should use payload types that round-trip cleanly through
/// JSON (avoid durations, non-string map keys, and unrepresentable
/// numerics).
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Event {
    /// Identifies the event type (e.g. `crm.sales.deal.created`).
    pub topic: Topic,
    /// Identifies the emitter (e.g. `llm.client`, `tool.exec`).
    pub source: String,
    /// When the event was created, as an RFC 3339-ish UTC timestamp.
    pub timestamp: String,
    /// Event-specific data.
    pub payload: Value,
    /// Scopes the event to a workspace. Empty means a global event.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub workspace_id: String,
}

impl Event {
    /// Creates an [`Event`] stamped with the current time. The analogue
    /// of Go's `NewEvent`.
    ///
    /// Not available on `wasm32-unknown-unknown`: that target has no
    /// system clock, and [`SystemTime::now`] panics there at run time.
    /// Use [`Event::new_at`] with a time from the host (for example
    /// `Date.now()` in a browser) instead.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    // The one sanctioned clock read in the bus: compiled out on
    // wasm32-unknown-unknown, where CI denies `SystemTime::now`.
    #[allow(clippy::disallowed_methods)]
    pub fn new(topic: impl Into<Topic>, source: impl Into<String>, payload: Value) -> Self {
        Self::new_at(topic, source, payload, SystemTime::now())
    }

    /// Creates an [`Event`] stamped with `at`.
    ///
    /// The clock seam: Go's `NewEvent` reads `time.Now()`, and Go callers
    /// that need another instant set `Timestamp` on the struct. This is
    /// the Rust form of that, on every target. Instants before the Unix
    /// epoch are stamped as the epoch.
    pub fn new_at(
        topic: impl Into<Topic>,
        source: impl Into<String>,
        payload: Value,
        at: SystemTime,
    ) -> Self {
        Event {
            topic: topic.into(),
            source: source.into(),
            timestamp: format_rfc3339(at),
            payload,
            workspace_id: String::new(),
        }
    }

    /// Sets the workspace scope, consuming and returning the event.
    pub fn with_workspace_id(mut self, id: impl Into<String>) -> Self {
        self.workspace_id = id.into();
        self
    }
}

/// Formats `at` as `YYYY-MM-DDTHH:MM:SS.sssZ`.
///
/// Hand-rolled rather than pulling in `chrono`/`time`: the bus needs one
/// UTC timestamp format and nothing else from a date library, and the
/// `bus` feature is meant to stay dependency-light.
fn format_rfc3339(at: SystemTime) -> String {
    let d = at.duration_since(UNIX_EPOCH).unwrap_or_default();
    format_epoch(d.as_secs(), d.subsec_millis())
}

fn format_epoch(secs: u64, millis: u32) -> String {
    let days = (secs / 86_400) as i64;
    let secs_of_day = secs % 86_400;
    let (y, m, d) = civil_from_days(days);
    format!(
        "{y:04}-{m:02}-{d:02}T{:02}:{:02}:{:02}.{millis:03}Z",
        secs_of_day / 3600,
        (secs_of_day % 3600) / 60,
        secs_of_day % 60,
    )
}

/// Howard Hinnant's `civil_from_days` algorithm: days since the Unix epoch
/// to a proleptic Gregorian (year, month, day).
fn civil_from_days(z: i64) -> (i64, u32, u32) {
    let z = z + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = (z - era * 146_097) as u64;
    let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365;
    let y = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = (doy - (153 * mp + 2) / 5 + 1) as u32;
    let m = if mp < 10 { mp + 3 } else { mp - 9 } as u32;
    (if m <= 2 { y + 1 } else { y }, m, d)
}
