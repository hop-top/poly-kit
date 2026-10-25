//! The [`Event`] envelope carried by the bus.
//!
//! Mirrors `go/runtime/bus/event.go` and its wire shape, pinned by the
//! shared vectors in `contracts/bus-v1`. JSON keys are lowercase, as
//! there — cross-process subscribers parse lowercase, so capitalized
//! keys would break them. Members are emitted in Go's order: `topic`,
//! `source`, `timestamp` and `payload` always, then `workspace_id`,
//! `id`, `subject`, `datacontenttype`, `dataschema` and `extensions`,
//! each omitted when empty.
//!
//! # Differences from Go
//!
//! - **Timestamp.** [`Event::timestamp`] is a `String`, not an instant.
//!   Decoding validates it as Go's `time.Time` does and rewrites it to the
//!   form Go re-encodes (see [`canonical_timestamp`]), so the same JSON
//!   round-trips to the same text in both SDKs. Go's decoder also accepts
//!   offsets of `+24:00` and offset minutes of `60`; this one refuses them.
//! - **Ids.** [`Event::new`] mints an `evt_` TypeID like Go's `NewEvent`.
//!   [`Event::new_at`], the clock seam, does not: minting needs
//!   randomness, which `wasm32-unknown-unknown` does not have without a
//!   JS backend the core refuses to carry. Mint with [`mint_event_id`]
//!   and attach it with [`Event::with_id`]; see [`mint_event_id`].
//! - **Extension integers.** Extension values are [`serde_json::Value`]s,
//!   so an integer literal beyond `u64` is seen as a float and reports
//!   `unsupported extension value type`, where Go reports
//!   `integer out of range`.
//! - **Bytes.** Go's `encoding/json` escapes `<`, `>`, `&`, U+2028 and
//!   U+2029; `serde_json` does not. The vectors compare JSON values.

use std::collections::BTreeMap;
use std::fmt;
use std::time::{SystemTime, UNIX_EPOCH};

use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;

use super::topic::Topic;

mod timestamp;

use timestamp::format_rfc3339;
pub use timestamp::{canonical_timestamp, TimestampError};

/// The TypeID prefix of minted event ids (`evt_…`).
pub const EVENT_ID_PREFIX: &str = "evt";

/// The CloudEvents extension that carries [`Event::workspace_id`]. Events
/// may not set it in [`Event::extensions`].
pub const WORKSPACE_EXTENSION: &str = "workspaceid";

/// The extension owned by the network adapter. Events may not set it in
/// [`Event::extensions`].
pub const ORIGIN_EXTENSION: &str = "kitorigin";

/// What an absent or `null` timestamp decodes to: Go's zero `time.Time`.
const ZERO_TIMESTAMP: &str = "0001-01-01T00:00:00Z";

/// CloudEvents context attribute names, which extensions may not reuse.
const RESERVED_ATTRIBUTES: &[&str] = &[
    "specversion",
    "id",
    "source",
    "type",
    "subject",
    "time",
    "datacontenttype",
    "dataschema",
    "data",
];

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
///
/// # Decoding
///
/// As in Go, decoding is lenient: an absent or `null` member is unset,
/// unknown members are ignored, and [`Event::extensions`] is not
/// validated (see [`Event::validate_extensions`]). Only the timestamp and
/// the member types are checked.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct Event {
    /// Identifies the event type (e.g. `crm.sales.deal.created`).
    #[serde(default = "empty_topic", deserialize_with = "topic_or_empty")]
    pub topic: Topic,
    /// Identifies the emitter (e.g. `llm.client`, `tool.exec`).
    #[serde(default, deserialize_with = "unset_if_null")]
    pub source: String,
    /// When the event was created: RFC 3339 with up to nanosecond
    /// precision and the original zone offset, in the form
    /// [`canonical_timestamp`] returns. [`Event::new_at`] writes UTC.
    #[serde(default = "zero_timestamp", deserialize_with = "timestamp")]
    pub timestamp: String,
    /// Event-specific data.
    #[serde(default)]
    pub payload: Value,
    /// Scopes the event to a workspace. Empty means a global event.
    #[serde(
        default,
        deserialize_with = "unset_if_null",
        skip_serializing_if = "String::is_empty"
    )]
    pub workspace_id: String,
    /// Identifies the event; an `evt_` TypeID minted by [`Event::new`].
    /// Empty on events built by hand. A decoded id is kept verbatim.
    #[serde(
        default,
        deserialize_with = "unset_if_null",
        skip_serializing_if = "String::is_empty"
    )]
    pub id: String,
    /// What, within [`Event::source`], the event is about.
    #[serde(
        default,
        deserialize_with = "unset_if_null",
        skip_serializing_if = "String::is_empty"
    )]
    pub subject: String,
    /// The media type of [`Event::payload`].
    #[serde(
        default,
        rename = "datacontenttype",
        deserialize_with = "unset_if_null",
        skip_serializing_if = "String::is_empty"
    )]
    pub data_content_type: String,
    /// A URI identifying the schema the payload adheres to.
    #[serde(
        default,
        rename = "dataschema",
        deserialize_with = "unset_if_null",
        skip_serializing_if = "String::is_empty"
    )]
    pub data_schema: String,
    /// CloudEvents extension attributes, emitted in ascending name order.
    /// Names are lowercase ASCII letters and digits; values are a string,
    /// a bool or an integer in `i32` range. `workspaceid` (set
    /// [`Event::workspace_id`] instead) and `kitorigin` (owned by the
    /// network adapter) are reserved. Checked by
    /// [`Event::validate_extensions`], not on decode.
    #[serde(
        default,
        deserialize_with = "unset_if_null",
        skip_serializing_if = "BTreeMap::is_empty"
    )]
    pub extensions: BTreeMap<String, Value>,
}

fn empty_topic() -> Topic {
    Topic::from("")
}

fn zero_timestamp() -> String {
    ZERO_TIMESTAMP.to_string()
}

/// `null` is unset, as for Go's `encoding/json`.
fn unset_if_null<'de, D, T>(d: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

fn topic_or_empty<'de, D: Deserializer<'de>>(d: D) -> Result<Topic, D::Error> {
    Ok(Option::<Topic>::deserialize(d)?.unwrap_or_else(empty_topic))
}

fn timestamp<'de, D: Deserializer<'de>>(d: D) -> Result<String, D::Error> {
    match Option::<String>::deserialize(d)? {
        None => Ok(zero_timestamp()),
        Some(s) => canonical_timestamp(&s).map_err(serde::de::Error::custom),
    }
}

impl Event {
    /// Creates an [`Event`] stamped with the current time and a fresh
    /// `evt_` id. The analogue of Go's `NewEvent`.
    ///
    /// Not available on `wasm32-unknown-unknown`: that target has neither
    /// a system clock ([`SystemTime::now`] panics there at run time) nor
    /// a random source. Use [`Event::new_at`] with a time from the host
    /// (for example `Date.now()` in a browser), and attach an id from
    /// [`mint_event_id`] if one is wanted.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    // The one sanctioned clock read in the bus: compiled out on
    // wasm32-unknown-unknown, where CI denies `SystemTime::now`.
    #[allow(clippy::disallowed_methods)]
    pub fn new(topic: impl Into<Topic>, source: impl Into<String>, payload: Value) -> Self {
        let now = SystemTime::now();
        Self::new_at(topic, source, payload, now).with_id(mint_event_id(now, random_bytes()))
    }

    /// Creates an [`Event`] stamped with `at`, with no id.
    ///
    /// The clock seam: Go's `NewEvent` reads `time.Now()`, and Go callers
    /// that need another instant set `Timestamp` on the struct. This is
    /// the Rust form of that, on every target. Instants before the Unix
    /// epoch are stamped as the epoch.
    ///
    /// Unlike [`Event::new`] it mints no id: minting needs randomness,
    /// and this constructor is pure and identical on every target. Go
    /// events built as literals have no id either, and one is minted if
    /// the event is shaped as a CloudEvent. Use [`Event::with_id`] with
    /// [`mint_event_id`] to give it one.
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
            id: String::new(),
            subject: String::new(),
            data_content_type: String::new(),
            data_schema: String::new(),
            extensions: BTreeMap::new(),
        }
    }

    /// Sets the workspace scope, consuming and returning the event.
    pub fn with_workspace_id(mut self, id: impl Into<String>) -> Self {
        self.workspace_id = id.into();
        self
    }

    /// Sets the event id.
    pub fn with_id(mut self, id: impl Into<String>) -> Self {
        self.id = id.into();
        self
    }

    /// Sets the subject.
    pub fn with_subject(mut self, subject: impl Into<String>) -> Self {
        self.subject = subject.into();
        self
    }

    /// Sets the payload's media type.
    pub fn with_data_content_type(mut self, content_type: impl Into<String>) -> Self {
        self.data_content_type = content_type.into();
        self
    }

    /// Sets the payload's schema URI.
    pub fn with_data_schema(mut self, schema: impl Into<String>) -> Self {
        self.data_schema = schema.into();
        self
    }

    /// Sets one extension attribute. Not checked until
    /// [`Event::validate_extensions`].
    pub fn with_extension(mut self, name: impl Into<String>, value: impl Into<Value>) -> Self {
        self.extensions.insert(name.into(), value.into());
        self
    }

    /// Checks [`Event::extensions`] against the CloudEvents attribute
    /// rules, as Go does when it shapes an event as a CloudEvent.
    ///
    /// Entries are checked in ascending byte order of name and the first
    /// failure wins. Per entry: `workspaceid` and `kitorigin` are
    /// reserved to the envelope; then the name must be `[a-z0-9]+` (any
    /// length) and not a CloudEvents attribute name; then the value must
    /// be a string, a bool, or an integer in `i32` range.
    pub fn validate_extensions(&self) -> Result<(), EventError> {
        self.extensions
            .iter()
            .try_for_each(|(name, value)| check_extension(name, value))
    }
}

/// A violated envelope rule, with Go's error text:
/// `cloudevents: <attribute>: <reason>`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct EventError {
    attribute: String,
    reason: String,
}

impl EventError {
    fn new(attribute: &str, reason: &str) -> Self {
        EventError {
            attribute: attribute.to_string(),
            reason: reason.to_string(),
        }
    }

    /// The offending attribute or extension name, as printed (the empty
    /// name is `""` with its quotes).
    pub fn attribute(&self) -> &str {
        &self.attribute
    }

    /// Why it was refused, for example `reserved attribute name`.
    pub fn reason(&self) -> &str {
        &self.reason
    }
}

impl fmt::Display for EventError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "cloudevents: {}: {}", self.attribute, self.reason)
    }
}

impl std::error::Error for EventError {}

fn check_extension(name: &str, value: &Value) -> Result<(), EventError> {
    match name {
        WORKSPACE_EXTENSION => {
            return Err(EventError::new(name, "reserved for the workspace id"));
        }
        ORIGIN_EXTENSION => {
            return Err(EventError::new(name, "reserved for the network origin"));
        }
        _ => {}
    }
    let valid = !name.is_empty()
        && name
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit());
    if !valid {
        let label = if name.is_empty() { "\"\"" } else { name };
        return Err(EventError::new(label, "invalid extension name"));
    }
    if RESERVED_ATTRIBUTES.contains(&name) {
        return Err(EventError::new(name, "reserved attribute name"));
    }
    match value {
        Value::String(_) | Value::Bool(_) => Ok(()),
        Value::Number(n) => {
            // `as_i64` is only set for integer literals that fit i64;
            // 1.0 and 1e3 parse as floats and fall through.
            let in_range = match n.as_i64() {
                Some(i) => i32::try_from(i).is_ok(),
                None if n.is_u64() => false,
                None => return Err(EventError::new(name, "unsupported extension value type")),
            };
            if in_range {
                Ok(())
            } else {
                Err(EventError::new(name, "integer out of range"))
            }
        }
        _ => Err(EventError::new(name, "unsupported extension value type")),
    }
}

// --- ids ---------------------------------------------------------------

/// Crockford base32, lowercase: the TypeID suffix alphabet.
const BASE32: &[u8; 32] = b"0123456789abcdefghjkmnpqrstvwxyz";

/// Builds an `evt_` TypeID: the prefix, then the 26-character base32
/// suffix of a UUIDv7 made from `at` and `random`.
///
/// The UUID carries `at` as 48 bits of Unix milliseconds (an instant
/// before the epoch counts as the epoch), the version nibble 7, the
/// variant bits `10`, and 74 of the 80 bits in `random`; the encoding
/// is the one in `contracts/typeid-v1`.
///
/// This is the id seam. It is pure and works on every target, so
/// `wasm32-unknown-unknown` callers supply both inputs from the host
/// (for example `Date.now()` and `crypto.getRandomValues`):
///
/// ```
/// use std::time::{Duration, UNIX_EPOCH};
/// use hop_top_kit::bus::Event;
/// use hop_top_kit::bus::event::mint_event_id;
///
/// let at = UNIX_EPOCH + Duration::from_millis(1_700_000_000_123);
/// let event = Event::new_at("a.b.c.created", "src", serde_json::json!(null), at)
///     .with_id(mint_event_id(at, [7; 10]));
/// assert!(event.id.starts_with("evt_"));
/// ```
pub fn mint_event_id(at: SystemTime, random: [u8; 10]) -> String {
    let ms = at
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis() as u64;
    let mut b = [0u8; 16];
    b[..6].copy_from_slice(&ms.to_be_bytes()[2..]);
    b[6] = 0x70 | (random[0] & 0x0f);
    b[7] = random[1];
    b[8] = 0x80 | (random[2] & 0x3f);
    b[9..].copy_from_slice(&random[3..]);
    let n = u128::from_be_bytes(b);
    let mut s = String::with_capacity(EVENT_ID_PREFIX.len() + 27);
    s.push_str(EVENT_ID_PREFIX);
    s.push('_');
    for i in (0..26).rev() {
        s.push(BASE32[((n >> (5 * i)) & 31) as usize] as char);
    }
    s
}

/// Ten bytes that differ between calls, for [`Event::new`].
///
/// Taken from the per-process random keys of the standard library's
/// `RandomState`, mixed with a counter. That is unpredictable enough to
/// keep ids unique, which is all an event id needs; it is not a
/// cryptographic source and ids must not be used as secrets.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn random_bytes() -> [u8; 10] {
    use std::collections::hash_map::RandomState;
    use std::hash::{BuildHasher, Hasher};
    use std::sync::atomic::{AtomicU64, Ordering};

    static COUNTER: AtomicU64 = AtomicU64::new(0);
    let word = || {
        let mut h = RandomState::new().build_hasher();
        h.write_u64(COUNTER.fetch_add(1, Ordering::Relaxed));
        h.finish().to_le_bytes()
    };
    let (a, b) = (word(), word());
    let mut out = [0u8; 10];
    out[..8].copy_from_slice(&a);
    out[8..].copy_from_slice(&b[..2]);
    out
}
