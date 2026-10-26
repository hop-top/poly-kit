//! Event frames with stream offsets, on top of the CloudEvents core's
//! frame codec: a kit frame carries `offset`, a CloudEvents frame the
//! `kitoffset` extension, both a decimal string. Mirrors Go's
//! `encodeFrameOffset` and `decodeEventFrame`.

use std::fmt;

use serde_json::Value;

use super::error::{ProtocolError, ProtocolKind};
use super::wire::{format_offset, parse_offset_token};
use crate::bus::cloudevents::{
    decode_frame, encode_frame, to_cloud_event, CloudEvent, ExtensionValue, Frame, FrameError,
    FrameFormat, OFFSET_EXTENSION, ORIGIN_EXTENSION,
};
use crate::bus::Event;

/// Encodes `e` as one outbound frame in `format`, stamping `origin`, and
/// on a stream link (`offset` set) its stream offset. A legacy link
/// passes `None` and gets exactly the CloudEvents core's frame.
pub fn encode_event_frame(
    format: FrameFormat,
    e: &Event,
    origin: &str,
    offset: Option<u64>,
) -> Result<String, FrameError> {
    let Some(off) = offset else {
        return encode_frame(format, e, origin);
    };
    if format == FrameFormat::CloudEvents {
        let mut c = to_cloud_event(e)?;
        if !origin.is_empty() {
            c.extensions
                .insert(ORIGIN_EXTENSION.to_string(), origin.into());
        }
        c.extensions.insert(
            OFFSET_EXTENSION.to_string(),
            format_offset(off).as_str().into(),
        );
        return Ok(c.to_json()?);
    }
    // Go's networkMsg ends with `offset`; the kit encoder's object closes
    // with `}`.
    let kit = encode_frame(format, e, origin)?;
    let body = kit.strip_suffix('}').expect("a kit frame is a JSON object");
    Ok(format!(r#"{body},"offset":"{}"}}"#, format_offset(off)))
}

/// A decoded event frame with its stream offset.
#[derive(Clone, Debug, PartialEq)]
pub struct OffsetFrame {
    /// The frame, `offset` / `kitoffset` stripped.
    pub frame: Frame,
    /// The parsed offset on a stream link, `None` on a legacy link.
    pub offset: Option<u64>,
}

/// Why an event frame was not delivered.
#[derive(Clone, Debug, PartialEq)]
pub enum EventFrameError {
    /// The frame is unusable: drop it, keep the link.
    Drop(FrameError),
    /// A stream link frame without a valid offset: close the link with
    /// 1002.
    Protocol(ProtocolError),
}

impl fmt::Display for EventFrameError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            EventFrameError::Drop(e) => e.fmt(f),
            EventFrameError::Protocol(e) => e.fmt(f),
        }
    }
}

impl std::error::Error for EventFrameError {}

/// Decodes an inbound event frame. `offset` (kit) and `kitoffset`
/// (CloudEvents) are always stripped; on a legacy link they are never
/// validated, on a stream link the offset must be a valid token, else a
/// protocol error. A frame unusable for any other reason is dropped,
/// which wins over a bad offset.
pub fn decode_event_frame(data: &[u8], stream_link: bool) -> Result<OffsetFrame, EventFrameError> {
    let frame = decode_frame(data).map_err(EventFrameError::Drop)?;
    if !stream_link {
        return Ok(OffsetFrame {
            frame,
            offset: None,
        });
    }
    let raw = raw_offset(data, frame.format);
    match parse_offset_token(raw.as_ref()) {
        Some(off) => Ok(OffsetFrame {
            frame,
            offset: Some(off),
        }),
        None => Err(EventFrameError::Protocol(ProtocolError::new(
            ProtocolKind::Offset,
            "event frame without a valid offset",
        ))),
    }
}

/// The raw offset member of a frame `decode_frame` accepted: the kit
/// `offset` member, or the CloudEvents `kitoffset` extension as the
/// CloudEvents decoder typed it (Go re-encodes the extension value).
fn raw_offset(data: &[u8], format: FrameFormat) -> Option<Value> {
    if format == FrameFormat::CloudEvents {
        let c = CloudEvent::from_json(data).ok()?;
        return match c.extensions.get(OFFSET_EXTENSION)? {
            ExtensionValue::String(s) => Some(Value::String(s.clone())),
            ExtensionValue::Bool(b) => Some(Value::Bool(*b)),
            ExtensionValue::Integer(i) => Some(Value::from(*i)),
        };
    }
    let doc = String::from_utf8_lossy(data);
    match serde_json::from_str::<Value>(&doc).ok()? {
        Value::Object(mut m) => m.remove("offset"),
        _ => None,
    }
}
