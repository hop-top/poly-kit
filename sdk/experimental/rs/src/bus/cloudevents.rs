//! CloudEvents 1.0 shaping for bus events.
//!
//! Ports `go/runtime/bus/cloudevents*.go` and the frame codec of
//! `network_frame.go`: the [`CloudEvent`] type with its validation and
//! structured-mode JSON codec, the [`Event`] mapping, and network frame
//! detection and encoding. Behaviour, error texts and wire shapes are
//! pinned by the shared vectors in `contracts/bus-v1/cloudevents`.
//!
//! Everything here is pure and works on `wasm32-unknown-unknown`, with one
//! exception: an [`Event`] without an id gets a fresh `evt_` id when it is
//! shaped, which needs a clock and randomness. That target has neither,
//! so there [`to_cloud_event`] reports `cloudevents: id: cannot mint`;
//! give the event an id first, from
//! [`mint_event_id`](crate::bus::event::mint_event_id).
//!
//! # Mapping
//!
//! | CloudEvents | [`Event`] |
//! |---|---|
//! | `id` | `id`, minted when empty |
//! | `source`, `subject`, `dataschema`, `datacontenttype` | same fields, verbatim |
//! | `type` | `topic`, verbatim (no topic grammar check) |
//! | `time` | `timestamp`; the zero instant leaves `time` unset; sent in UTC |
//! | `data` | `payload`; `null` leaves `data` unset |
//! | `data_base64` | binary data, beside the event (see below) |
//! | `workspaceid` extension | `workspace_id` |
//! | other extensions | `extensions` |
//!
//! When `data` is set and `datacontenttype` is empty, it becomes
//! `application/json`. With a non-JSON content type the payload must be a
//! string (or binary): CloudEvents JSON format §3.1.
//!
//! # Differences from Go
//!
//! - **Binary data.** Go carries `[]byte` in `Event.Payload`; here the
//!   payload is a [`serde_json::Value`], so binary data travels beside the
//!   event: [`to_cloud_event_binary`] takes it, and [`from_cloud_event`]
//!   returns it in [`DecodedEvent::binary`].
//! - **Data numbers.** A decoded payload keeps integers as integers; Go
//!   decodes every JSON number to `float64`. [`Data::Json`] keeps the
//!   exact text either way.
//! - **Nesting.** `serde_json` refuses documents nested deeper than 128
//!   levels (`cloudevents: invalid JSON`); Go's limit is 10000.
//! - **Extension values from an [`Event`]** follow
//!   [`Event::validate_extensions`]'s caveats: a JSON integer beyond `u64`
//!   or `-0` is a float to `serde_json`. Decoding CloudEvents JSON does
//!   not have them, since it reads the literal.
//!
//! [`Event`]: crate::bus::Event
//! [`Event::validate_extensions`]: crate::bus::Event::validate_extensions

use std::collections::BTreeMap;
use std::fmt;

use serde_json::Value;

mod frame;
mod json;
mod mapping;
mod rules;

pub use super::event::{EVENT_ID_PREFIX, ORIGIN_EXTENSION};
pub use frame::{decode_frame, encode_frame, frame_kind, Frame, FrameError, FrameFormat};
pub use mapping::{
    from_cloud_event, marshal_cloud_event, to_cloud_event, to_cloud_event_binary,
    unmarshal_cloud_event, DecodedEvent,
};

/// The only CloudEvents spec version kit encodes and accepts.
pub const SPEC_VERSION: &str = "1.0";

/// The CloudEvents extension carrying a stream offset on CloudEvents
/// frames over stream links. Owned by the network adapter, like
/// [`ORIGIN_EXTENSION`]: events may not set it.
pub const OFFSET_EXTENSION: &str = "kitoffset";

/// The `datacontenttype` stamped on JSON data.
const JSON_CONTENT_TYPE: &str = "application/json";

/// What an unset [`Event::timestamp`](crate::bus::Event::timestamp) holds: Go's zero `time.Time`.
const ZERO_TIMESTAMP: &str = "0001-01-01T00:00:00Z";

/// Context attribute names an extension may not take.
const RESERVED_ATTRIBUTES: [&str; 9] = [
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

/// The first CloudEvents rule an event breaks. Its text is wire contract,
/// shared with every kit SDK: `cloudevents: <attribute>: <reason>`, or
/// `cloudevents: <reason>` for document errors.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CloudEventError {
    attribute: String,
    reason: String,
}

impl CloudEventError {
    fn new(attribute: &str, reason: &str) -> Self {
        CloudEventError {
            attribute: attribute.to_string(),
            reason: reason.to_string(),
        }
    }

    fn document(reason: &str) -> Self {
        Self::new("", reason)
    }

    /// The offending attribute or extension name, as printed (an empty
    /// extension name is `""` with its quotes); empty for document errors
    /// such as invalid JSON.
    pub fn attribute(&self) -> &str {
        &self.attribute
    }

    /// The rule broken, for example `required`.
    pub fn reason(&self) -> &str {
        &self.reason
    }
}

impl fmt::Display for CloudEventError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.attribute.is_empty() {
            write!(f, "cloudevents: {}", self.reason)
        } else {
            write!(f, "cloudevents: {}: {}", self.attribute, self.reason)
        }
    }
}

impl std::error::Error for CloudEventError {}

/// Event data: the `data` member or the decoded `data_base64` member.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Data {
    /// JSON text, as Go's `json.RawMessage`. Encoding compacts it;
    /// decoding keeps the member's text, compacted.
    Json(String),
    /// Binary data, sent as `data_base64`.
    Binary(Vec<u8>),
}

/// An extension attribute value. Integers must be in `i32` range (the
/// CloudEvents Integer type); they are held as `i64`, as in Go.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ExtensionValue {
    /// A string.
    String(String),
    /// A boolean.
    Bool(bool),
    /// An integer.
    Integer(i64),
}

impl From<&str> for ExtensionValue {
    fn from(s: &str) -> Self {
        ExtensionValue::String(s.to_string())
    }
}

impl From<String> for ExtensionValue {
    fn from(s: String) -> Self {
        ExtensionValue::String(s)
    }
}

impl From<bool> for ExtensionValue {
    fn from(b: bool) -> Self {
        ExtensionValue::Bool(b)
    }
}

impl From<i64> for ExtensionValue {
    fn from(n: i64) -> Self {
        ExtensionValue::Integer(n)
    }
}

impl From<i32> for ExtensionValue {
    fn from(n: i32) -> Self {
        ExtensionValue::Integer(n.into())
    }
}

impl From<ExtensionValue> for Value {
    fn from(v: ExtensionValue) -> Self {
        match v {
            ExtensionValue::String(s) => Value::String(s),
            ExtensionValue::Bool(b) => Value::Bool(b),
            ExtensionValue::Integer(n) => Value::from(n),
        }
    }
}

/// A CloudEvents 1.0 event. Empty strings and `None` mean "unset".
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CloudEvent {
    /// `specversion`; must be [`SPEC_VERSION`].
    pub spec_version: String,
    /// `id`.
    pub id: String,
    /// `source`; any non-empty string (not checked as a URI-reference).
    pub source: String,
    /// `type`.
    pub event_type: String,
    /// `subject`.
    pub subject: String,
    /// `time`: RFC 3339 per the CloudEvents grammar, with any offset; sent
    /// in UTC. The zero instant `0001-01-01T00:00:00Z` counts as unset,
    /// as Go's zero `time.Time`. Decoding stores the form
    /// [`canonical_timestamp`](crate::bus::event::canonical_timestamp)
    /// returns, offset kept.
    pub time: String,
    /// `datacontenttype`.
    pub data_content_type: String,
    /// `dataschema`; any non-empty string (not checked as a URI).
    pub data_schema: String,
    /// `data` or `data_base64`.
    pub data: Option<Data>,
    /// Extension attributes, encoded in ascending name order.
    pub extensions: BTreeMap<String, ExtensionValue>,
}

impl CloudEvent {
    /// A CloudEvent with the required attributes set.
    pub fn new(
        id: impl Into<String>,
        source: impl Into<String>,
        event_type: impl Into<String>,
    ) -> Self {
        CloudEvent {
            spec_version: SPEC_VERSION.to_string(),
            id: id.into(),
            source: source.into(),
            event_type: event_type.into(),
            ..CloudEvent::default()
        }
    }

    /// Checks the event against CloudEvents 1.0 and returns the first
    /// violation, in this order: specversion, id, source, type, time (UTC
    /// year 0000-9999), data, then extensions by ascending name.
    pub fn validate(&self) -> Result<(), CloudEventError> {
        if self.spec_version.is_empty() {
            return Err(CloudEventError::new("specversion", "required"));
        }
        if self.spec_version != SPEC_VERSION {
            let reason = format!("unsupported value {}", rules::go_quote(&self.spec_version));
            return Err(CloudEventError::new("specversion", &reason));
        }
        for (name, v) in [
            ("id", &self.id),
            ("source", &self.source),
            ("type", &self.event_type),
        ] {
            if v.is_empty() {
                return Err(CloudEventError::new(name, "required"));
            }
        }
        if !self.time.is_empty() {
            match self.instant() {
                Some(t) if t.is_zero() || t.encodable() => {}
                _ => return Err(CloudEventError::new("time", "invalid RFC 3339 timestamp")),
            }
        }
        if let Some(Data::Json(text)) = &self.data {
            if !json::valid(text) {
                return Err(CloudEventError::new("data", "invalid JSON"));
            }
        }
        self.extensions
            .iter()
            .try_for_each(|(name, v)| check_extension(name, v))
    }

    /// Encodes the event in structured mode. Members come in a fixed
    /// order: specversion, id, source, type, subject, time,
    /// datacontenttype, dataschema, extensions by ascending name, then
    /// data or data_base64. Unset attributes are omitted, time is UTC
    /// with trailing fraction zeros trimmed, and `<`, `>` and `&` are not
    /// escaped. Validates first.
    pub fn to_json(&self) -> Result<String, CloudEventError> {
        json::encode(self)
    }

    /// Decodes a structured-mode document. A JSON `null` member counts as
    /// unset. The first failure is returned, checked in two passes: shape
    /// (the document, then the string attributes in the order of
    /// [`CloudEvent::to_json`], then data, then extensions by ascending
    /// name), then [`CloudEvent::validate`].
    pub fn from_json(input: &[u8]) -> Result<CloudEvent, CloudEventError> {
        json::decode(input)
    }

    fn instant(&self) -> Option<rules::Instant> {
        rules::parse_time(&self.time)
    }
}

/// The empty name prints as `""` so the message stays unambiguous.
fn extension_label(name: &str) -> String {
    if name.is_empty() {
        "\"\"".to_string()
    } else {
        name.to_string()
    }
}

fn check_extension_name(name: &str) -> Result<(), CloudEventError> {
    if !rules::valid_extension_name(name) {
        return Err(CloudEventError::new(
            &extension_label(name),
            "invalid extension name",
        ));
    }
    if RESERVED_ATTRIBUTES.contains(&name) {
        return Err(CloudEventError::new(name, "reserved attribute name"));
    }
    Ok(())
}

fn check_extension(name: &str, v: &ExtensionValue) -> Result<(), CloudEventError> {
    check_extension_name(name)?;
    match v {
        ExtensionValue::Integer(n) if i32::try_from(*n).is_err() => {
            Err(CloudEventError::new(name, "integer out of range"))
        }
        _ => Ok(()),
    }
}

/// The reason an extension owned by the network adapter is refused.
fn network_reserved(name: &str) -> Option<&'static str> {
    match name {
        ORIGIN_EXTENSION => Some("reserved for the network origin"),
        OFFSET_EXTENSION => Some("reserved for the stream offset"),
        _ => None,
    }
}
