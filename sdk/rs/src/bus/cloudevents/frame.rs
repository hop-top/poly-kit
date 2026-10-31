//! Network event frames in both formats. Mirrors the frame codec of
//! `go/runtime/bus/network_frame.go` for links without stream offsets;
//! the network adapter itself is not ported yet.

use std::collections::BTreeMap;
use std::fmt;

use serde_json::Value;

use super::json::{members, valid};
use super::rules::wide_offset_timestamp;
use super::{
    from_cloud_event, to_cloud_event, CloudEvent, CloudEventError, ExtensionValue,
    OFFSET_EXTENSION, ORIGIN_EXTENSION, ZERO_TIMESTAMP,
};
use crate::bus::event::{canonical_timestamp, Event, TimestampError};
use crate::bus::topic::Topic;

/// How an event frame is encoded. Inbound frames are accepted in either
/// format, told apart by [`frame_kind`].
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum FrameFormat {
    /// The original kit frame:
    /// `{topic, source, timestamp, payload, origin, workspace_id?, id?}`.
    /// The default in every kit SDK, and the only format every peer
    /// reads.
    #[default]
    Kit,
    /// A CloudEvents structured-mode document (see
    /// [`CloudEvent::to_json`]) with the loop-prevention origin in the
    /// `kitorigin` extension. Peers without CloudEvents support misread
    /// it, so use it only once every peer decodes it.
    CloudEvents,
}

impl fmt::Display for FrameFormat {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            FrameFormat::Kit => "kit",
            FrameFormat::CloudEvents => "cloudevents",
        })
    }
}

/// Why a frame was dropped. Only the drop is contract, not the text.
#[derive(Clone, Debug, PartialEq, Eq)]
#[non_exhaustive]
pub enum FrameError {
    /// The frame is not JSON.
    InvalidJson,
    /// The frame is JSON but not an object (`null`, an array, a scalar).
    NotObject,
    /// A kit frame member has the wrong JSON type, or an unusable
    /// timestamp.
    Member(&'static str),
    /// A CloudEvents frame broke a CloudEvents rule.
    CloudEvent(CloudEventError),
}

impl fmt::Display for FrameError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            FrameError::InvalidJson => f.write_str("bus: frame is not valid JSON"),
            FrameError::NotObject => f.write_str("bus: frame is not a JSON object"),
            FrameError::Member(name) => write!(f, "bus: frame member {name:?} is invalid"),
            FrameError::CloudEvent(e) => e.fmt(f),
        }
    }
}

impl std::error::Error for FrameError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            FrameError::CloudEvent(e) => Some(e),
            _ => None,
        }
    }
}

impl From<CloudEventError> for FrameError {
    fn from(e: CloudEventError) -> Self {
        FrameError::CloudEvent(e)
    }
}

/// One decoded event frame.
#[derive(Clone, Debug, PartialEq)]
pub struct Frame {
    /// The format the frame came in.
    pub format: FrameFormat,
    /// The event, without the origin.
    pub event: Event,
    /// Binary data from a CloudEvents frame's `data_base64`.
    pub binary: Option<Vec<u8>>,
    /// The loop-prevention origin: kit `origin` or CloudEvents
    /// `kitorigin`; empty when absent.
    pub origin: String,
}

/// Classifies an inbound frame. A JSON object with a member named exactly
/// `specversion` (any value, `null` included; no case folding) is a
/// CloudEvents frame; any other object is a kit frame. Anything else is
/// dropped.
pub fn frame_kind(data: &[u8]) -> Result<FrameFormat, FrameError> {
    let doc = String::from_utf8_lossy(data);
    Ok(classify(&doc)?.0)
}

fn classify(doc: &str) -> Result<(FrameFormat, BTreeMap<String, &str>), FrameError> {
    if !valid(doc) {
        return Err(FrameError::InvalidJson);
    }
    let m = members(doc).ok_or(FrameError::NotObject)?;
    let kind = if m.contains_key("specversion") {
        FrameFormat::CloudEvents
    } else {
        FrameFormat::Kit
    };
    Ok((kind, m))
}

/// Decodes an inbound frame of either format (see [`frame_kind`]).
///
/// A kit frame's members are matched by exact key; `null` or absent means
/// unset, and a member of the wrong JSON type drops the frame. Its
/// `timestamp` is read as Go's `time.Time` JSON: an offset hour up to 24
/// and minute up to 60 is accepted and the timestamp kept in UTC. A
/// CloudEvents frame must decode as a [`CloudEvent`]; its `kitorigin`
/// must be a string, and it and `kitoffset` are stripped before the event
/// is built (an offset on a link without offsets is not checked).
pub fn decode_frame(data: &[u8]) -> Result<Frame, FrameError> {
    let doc = String::from_utf8_lossy(data);
    let (kind, m) = classify(&doc)?;
    if kind == FrameFormat::Kit {
        return decode_kit(&m);
    }
    let mut c = CloudEvent::from_json(doc.as_bytes())?;
    let origin = match c.extensions.remove(ORIGIN_EXTENSION) {
        None => String::new(),
        Some(ExtensionValue::String(s)) => s,
        Some(_) => return Err(CloudEventError::new(ORIGIN_EXTENSION, "must be a string").into()),
    };
    c.extensions.remove(OFFSET_EXTENSION);
    let d = from_cloud_event(c)?;
    Ok(Frame {
        format: FrameFormat::CloudEvents,
        event: d.event,
        binary: d.binary,
        origin,
    })
}

fn decode_kit(m: &BTreeMap<String, &str>) -> Result<Frame, FrameError> {
    let raw = |key| m.get(key).copied().filter(|raw| *raw != "null");
    let string = |key: &'static str| -> Result<String, FrameError> {
        raw(key).map_or(Ok(String::new()), |raw| {
            serde_json::from_str(raw).map_err(|_| FrameError::Member(key))
        })
    };
    let timestamp = match raw("timestamp") {
        None => ZERO_TIMESTAMP.to_string(),
        Some(raw) => serde_json::from_str::<String>(raw)
            .ok()
            .and_then(|s| match canonical_timestamp(&s) {
                Ok(t) => Some(t),
                // Go's time.Time JSON takes offsets up to +24:60; an
                // event timestamp cannot carry them, so they become UTC.
                Err(TimestampError::Offset) => wide_offset_timestamp(&s),
                Err(TimestampError::Invalid) => None,
            })
            .ok_or(FrameError::Member("timestamp"))?,
    };
    let payload = match raw("payload") {
        None => Value::Null,
        Some(raw) => serde_json::from_str(raw).map_err(|_| FrameError::Member("payload"))?,
    };
    let event = Event {
        topic: Topic::from(string("topic")?),
        source: string("source")?,
        timestamp,
        payload,
        workspace_id: string("workspace_id")?,
        id: string("id")?,
        subject: String::new(),
        data_content_type: String::new(),
        data_schema: String::new(),
        extensions: BTreeMap::new(),
    };
    Ok(Frame {
        format: FrameFormat::Kit,
        event,
        binary: None,
        origin: string("origin")?,
    })
}

/// Encodes `e` as one outbound frame in `format`, stamping `origin` for
/// loop prevention.
///
/// A kit frame carries `topic`, `source`, `timestamp`, `payload` (always,
/// `null` when unset) and `origin`, then `workspace_id` and `id` when set;
/// it drops `subject`, `datacontenttype`, `dataschema` and extensions. A
/// CloudEvents frame is [`to_cloud_event`] plus `kitorigin` when `origin`
/// is set, and fails as that does.
pub fn encode_frame(format: FrameFormat, e: &Event, origin: &str) -> Result<String, FrameError> {
    if format == FrameFormat::CloudEvents {
        let mut c = to_cloud_event(e)?;
        if !origin.is_empty() {
            c.extensions
                .insert(ORIGIN_EXTENSION.to_string(), origin.into());
        }
        return Ok(c.to_json()?);
    }
    let q = |s: &str| Value::from(s).to_string();
    let mut out = format!(
        r#"{{"topic":{},"source":{},"timestamp":{},"payload":{},"origin":{}"#,
        q(e.topic.as_str()),
        q(&e.source),
        q(&e.timestamp),
        e.payload,
        q(origin),
    );
    if !e.workspace_id.is_empty() {
        out.push_str(&format!(r#","workspace_id":{}"#, q(&e.workspace_id)));
    }
    if !e.id.is_empty() {
        out.push_str(&format!(r#","id":{}"#, q(&e.id)));
    }
    out.push('}');
    Ok(out)
}
