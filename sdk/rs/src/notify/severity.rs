//! The severity convention: how a payload advertises urgency.

use std::fmt;

use serde_json::Value;

use crate::bus::Event;

/// Classifies an event for notification routing. [`Severity::Info`] is
/// the default when a payload does not advertise one. The order is
/// Debug < Info < Warn < Error < Critical, so a configured floor
/// compares with `>=`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Severity {
    /// Fine-grained development trace; usually muted in production.
    Debug = 0,
    /// Routine operational events; the default.
    Info = 1,
    /// Degradation worth a heads-up, not a page.
    Warn = 2,
    /// A failure that needs attention; the typical e-mail or chat floor.
    Error = 3,
    /// Page-now urgency; the typical pager or SMS floor.
    Critical = 4,
}

impl Severity {
    /// The lowercase wire form: `debug`, `info`, `warn`, `error`,
    /// `critical`. Go's `String`; a Rust value is always one of the five,
    /// so Go's `unknown` has no counterpart.
    pub fn as_str(self) -> &'static str {
        match self {
            Severity::Debug => "debug",
            Severity::Info => "info",
            Severity::Warn => "warn",
            Severity::Error => "error",
            Severity::Critical => "critical",
        }
    }

    /// The severity for a wire keyword. Case-sensitive, lowercase only,
    /// as emitters serialize it; anything else is `None`.
    pub fn from_keyword(s: &str) -> Option<Self> {
        match s {
            "debug" => Some(Severity::Debug),
            "info" => Some(Severity::Info),
            "warn" => Some(Severity::Warn),
            "error" => Some(Severity::Error),
            "critical" => Some(Severity::Critical),
            _ => None,
        }
    }

    /// The severity for its number (0 Debug .. 4 Critical); `None` out of
    /// range.
    pub fn from_i64(n: i64) -> Option<Self> {
        match n {
            0 => Some(Severity::Debug),
            1 => Some(Severity::Info),
            2 => Some(Severity::Warn),
            3 => Some(Severity::Error),
            4 => Some(Severity::Critical),
            _ => None,
        }
    }
}

impl fmt::Display for Severity {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Reads severity from an event's payload: Go's `SeverityOf`.
///
/// The payload is a JSON value, so there is no typed `WithSeverity`
/// path: a typed payload advertises severity by serializing a `severity`
/// key. Resolution: a JSON object with a `severity` member that is a
/// lowercase keyword string, or a number in `0..=4` (an integer, or a
/// float with no fraction); otherwise [`Severity::Info`]. An unknown
/// keyword, an out-of-range or fractional number, a missing key, a
/// member of another type, a non-object payload and `null` all give
/// `Info`.
pub fn severity_of(e: &Event) -> Severity {
    let Value::Object(m) = &e.payload else {
        return Severity::Info;
    };
    let found = match m.get("severity") {
        Some(Value::String(s)) => Severity::from_keyword(s),
        Some(Value::Number(n)) => match n.as_i64() {
            Some(i) => Severity::from_i64(i),
            None => n
                .as_f64()
                .filter(|x| x.fract() == 0.0 && x.abs() < 1e15)
                .and_then(|x| Severity::from_i64(x as i64)),
        },
        _ => None,
    };
    found.unwrap_or(Severity::Info)
}
