//! The [`Event`] mapping: `ToCloudEvent` and `FromCloudEvent` of
//! `go/runtime/bus/cloudevents.go`.

use std::collections::BTreeMap;

use serde_json::Value;

use super::{
    check_extension, check_extension_name, network_reserved, rules, CloudEvent, CloudEventError,
    Data, ExtensionValue, JSON_CONTENT_TYPE, ZERO_TIMESTAMP,
};
use crate::bus::event::{canonical_timestamp, Event, WORKSPACE_EXTENSION};
use crate::bus::topic::Topic;

/// Converts an [`Event`] extension value: string, bool, or an integer in
/// `i32` range.
fn extension_from_value(name: &str, v: &Value) -> Result<ExtensionValue, CloudEventError> {
    if name == WORKSPACE_EXTENSION {
        return Err(CloudEventError::new(name, "reserved for the workspace id"));
    }
    if let Some(reason) = network_reserved(name) {
        return Err(CloudEventError::new(name, reason));
    }
    check_extension_name(name)?;
    let value = match v {
        Value::String(s) => ExtensionValue::String(s.clone()),
        Value::Bool(b) => ExtensionValue::Bool(*b),
        // `as_i64` is set only for integer literals that fit; floats such
        // as 1.0 and 1.5 fall through.
        Value::Number(n) => match n.as_i64() {
            Some(i) => ExtensionValue::Integer(i),
            None if n.is_u64() => return Err(CloudEventError::new(name, "integer out of range")),
            None => {
                return Err(CloudEventError::new(
                    name,
                    "unsupported extension value type",
                ))
            }
        },
        _ => {
            return Err(CloudEventError::new(
                name,
                "unsupported extension value type",
            ))
        }
    };
    check_extension(name, &value)?;
    Ok(value)
}

/// An [`Event`] decoded from a CloudEvent, with the binary data its
/// payload cannot hold.
#[derive(Clone, Debug, PartialEq)]
pub struct DecodedEvent {
    /// The event. Its payload is `null` when the data was binary.
    pub event: Event,
    /// The decoded `data_base64`, when the CloudEvent carried binary data.
    pub binary: Option<Vec<u8>>,
}

/// Maps an [`Event`] to a [`CloudEvent`] (see the [module](super) docs).
///
/// Checks run in Go's order: the payload against `datacontenttype`, then
/// extensions by ascending name (`workspaceid`, `kitorigin` and
/// `kitoffset` are reserved, then the name and value rules), then
/// [`CloudEvent::validate`].
pub fn to_cloud_event(e: &Event) -> Result<CloudEvent, CloudEventError> {
    shape(e, None)
}

/// [`to_cloud_event`] with binary data in place of the payload, sent as
/// `data_base64`. No content type is defaulted. The event's payload must
/// be `null`: a payload and binary data together are
/// `cloudevents: data: data and data_base64 are mutually exclusive`.
pub fn to_cloud_event_binary(e: &Event, data: Vec<u8>) -> Result<CloudEvent, CloudEventError> {
    shape(e, Some(data))
}

fn shape(e: &Event, binary: Option<Vec<u8>>) -> Result<CloudEvent, CloudEventError> {
    let id = if e.id.is_empty() {
        mint_id()?
    } else {
        e.id.clone()
    };
    let time = canonical_timestamp(&e.timestamp).unwrap_or_else(|_| e.timestamp.clone());
    let mut c = CloudEvent {
        id,
        source: e.source.clone(),
        event_type: e.topic.as_str().to_string(),
        subject: e.subject.clone(),
        data_content_type: e.data_content_type.clone(),
        data_schema: e.data_schema.clone(),
        time,
        ..CloudEvent::new("", "", "")
    };
    if c.instant().is_some_and(|t| t.is_zero()) {
        c.time.clear();
    }
    c.data = match (&e.payload, binary) {
        (Value::Null, Some(bytes)) => Some(Data::Binary(bytes)),
        (_, Some(_)) => {
            return Err(CloudEventError::new(
                "data",
                "data and data_base64 are mutually exclusive",
            ))
        }
        (Value::Null, None) => None,
        (p @ Value::String(_), None) => Some(Data::Json(p.to_string())),
        (p, None) => {
            if !c.data_content_type.is_empty() && !rules::is_json_media_type(&c.data_content_type) {
                return Err(CloudEventError::new(
                    "data",
                    "non-JSON datacontenttype needs a string or binary payload",
                ));
            }
            Some(Data::Json(p.to_string()))
        }
    };
    if c.data_content_type.is_empty() && matches!(c.data, Some(Data::Json(_))) {
        c.data_content_type = JSON_CONTENT_TYPE.to_string();
    }
    for (name, v) in &e.extensions {
        let value = extension_from_value(name, v)?;
        c.extensions.insert(name.clone(), value);
    }
    if !e.workspace_id.is_empty() {
        c.extensions.insert(
            WORKSPACE_EXTENSION.to_string(),
            ExtensionValue::String(e.workspace_id.clone()),
        );
    }
    c.validate()?;
    Ok(c)
}

/// A fresh `evt_` id through [`Event::new`], the bus's one sanctioned
/// clock and randomness read.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn mint_id() -> Result<String, CloudEventError> {
    Ok(Event::new(Topic::from(""), "", Value::Null).id)
}

/// No clock or randomness on this target: the caller sets the id.
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
fn mint_id() -> Result<String, CloudEventError> {
    Err(CloudEventError::new("id", "cannot mint"))
}

/// Maps a [`CloudEvent`] back to an [`Event`]: the inverse of
/// [`to_cloud_event`]. Validates first. JSON data becomes the payload and
/// `data_base64` becomes [`DecodedEvent::binary`]. The `workspaceid`
/// extension moves to `workspace_id` and must be a string; `kitorigin`
/// and `kitoffset` are refused (the network layer strips them first); all
/// other extensions are kept.
pub fn from_cloud_event(c: CloudEvent) -> Result<DecodedEvent, CloudEventError> {
    c.validate()?;
    let timestamp = if c.time.is_empty() || c.instant().is_some_and(|t| t.is_zero()) {
        ZERO_TIMESTAMP.to_string()
    } else {
        // Validated above, so this only uppercases `t`/`z` and trims.
        canonical_timestamp(&rules::upper_time(&c.time)).unwrap_or(c.time)
    };
    let mut e = Event {
        topic: Topic::from(c.event_type),
        source: c.source,
        timestamp,
        payload: Value::Null,
        workspace_id: String::new(),
        id: c.id,
        subject: c.subject,
        data_content_type: c.data_content_type,
        data_schema: c.data_schema,
        extensions: BTreeMap::new(),
    };
    let mut binary = None;
    match c.data {
        Some(Data::Json(text)) => {
            e.payload = serde_json::from_str(&text)
                .map_err(|_| CloudEventError::new("data", "invalid JSON"))?;
        }
        Some(Data::Binary(bytes)) => binary = Some(bytes),
        None => {}
    }
    for (name, v) in c.extensions {
        if let Some(reason) = network_reserved(&name) {
            return Err(CloudEventError::new(&name, reason));
        }
        if name == WORKSPACE_EXTENSION {
            let ExtensionValue::String(ws) = v else {
                return Err(CloudEventError::new(&name, "must be a string"));
            };
            e.workspace_id = ws;
            continue;
        }
        e.extensions.insert(name, v.into());
    }
    Ok(DecodedEvent { event: e, binary })
}

/// Encodes `e` as a structured-mode CloudEvents JSON document.
pub fn marshal_cloud_event(e: &Event) -> Result<String, CloudEventError> {
    to_cloud_event(e)?.to_json()
}

/// Decodes a structured-mode CloudEvents JSON document into an event.
pub fn unmarshal_cloud_event(input: &[u8]) -> Result<DecodedEvent, CloudEventError> {
    from_cloud_event(CloudEvent::from_json(input)?)
}
