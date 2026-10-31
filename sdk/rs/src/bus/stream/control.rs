//! Frame classification and control frames: what an acceptor and a
//! dialer each make of a control frame, and the encoders for every
//! control frame either side sends.

use serde::Serialize;
use serde_json::Value;

use super::error::{ProtocolError, ProtocolKind, StreamError, CODE_BAD_REQUEST, CODE_UNKNOWN_TYPE};
use super::wire::{
    format_offset, is_null, member_str, parse_offset_token, parse_patterns, parse_scope,
    valid_stream_id, Members, Scope, MAX_REQUEST_ID,
};
use crate::bus::quote::quote;

/// How a received frame is handled.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum FrameKind {
    /// Not a JSON object: ignored.
    Drop,
    /// A kit event frame.
    Kit,
    /// A CloudEvents event frame (a member named exactly `specversion`).
    CloudEvents,
    /// A control frame (a string `type`, on stream links only).
    Control,
}

impl FrameKind {
    /// The kind as the vectors spell it.
    pub fn as_str(self) -> &'static str {
        match self {
            FrameKind::Drop => "drop",
            FrameKind::Kit => "kit",
            FrameKind::CloudEvents => "cloudevents",
            FrameKind::Control => "control",
        }
    }
}

/// Classifies one received frame and returns its members: drop
/// non-objects, `specversion` makes a CloudEvents event, a string `type`
/// makes a control frame on stream links only, anything else is a kit
/// event.
pub fn classify_frame(data: &[u8], stream_link: bool) -> (FrameKind, Option<Members>) {
    let members = match serde_json::from_slice::<Value>(data) {
        Ok(Value::Object(m)) => m,
        _ => return (FrameKind::Drop, None),
    };
    if members.contains_key("specversion") {
        return (FrameKind::CloudEvents, Some(members));
    }
    if stream_link && matches!(members.get("type"), Some(Value::String(_))) {
        return (FrameKind::Control, Some(members));
    }
    (FrameKind::Kit, Some(members))
}

/// A subscription request type.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum RequestType {
    /// Adds patterns, replaces the scope, or both.
    Subscribe,
    /// Removes patterns.
    Unsubscribe,
}

impl RequestType {
    /// The `type` member on the wire.
    pub fn as_str(self) -> &'static str {
        match self {
            RequestType::Subscribe => "subscribe",
            RequestType::Unsubscribe => "unsubscribe",
        }
    }
}

/// A subscribe or unsubscribe request from a dialer. `patterns` and
/// `scope` are `None` when absent.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CtlRequest {
    /// Subscribe or unsubscribe.
    pub typ: RequestType,
    /// Request id, echoed by the reply; 1-64 bytes.
    pub id: String,
    /// Patterns to add or remove.
    pub patterns: Option<Vec<String>>,
    /// Scope to set (subscribe only).
    pub scope: Option<Scope>,
}

/// An error reply; `id` is empty when the request had no valid id.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct CtlError {
    /// The request id echoed, or empty.
    pub id: String,
    /// Code and message.
    pub error: StreamError,
}

/// What an acceptor does with a control frame from its dialer.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum AcceptorControl {
    /// Apply this request, then ack it.
    Request(CtlRequest),
    /// Send this error reply.
    Reply(CtlError),
    /// Ignore the frame.
    Ignore,
}

fn reply(id: &str, code: &str, message: String) -> AcceptorControl {
    AcceptorControl::Reply(CtlError {
        id: id.to_string(),
        error: StreamError::new(code, message),
    })
}

/// Handles a control frame received by an acceptor.
pub fn acceptor_control(m: &Members) -> AcceptorControl {
    let typ = member_str(m, "type").unwrap_or("");
    let id = member_str(m, "id")
        .filter(|id| !id.is_empty() && id.len() <= MAX_REQUEST_ID)
        .unwrap_or("");
    let typ = match typ {
        "ack" | "error" | "puback" => return AcceptorControl::Ignore,
        "reset" | "position" => {
            return reply(
                id,
                CODE_BAD_REQUEST,
                format!("{typ} is sent by acceptors only"),
            )
        }
        "subscribe" => RequestType::Subscribe,
        "unsubscribe" => RequestType::Unsubscribe,
        other => {
            return reply(
                id,
                CODE_UNKNOWN_TYPE,
                format!("unknown control frame type {}", quote(other)),
            )
        }
    };
    if id.is_empty() {
        return reply(
            "",
            CODE_BAD_REQUEST,
            format!("id must be a string of 1-{MAX_REQUEST_ID} bytes"),
        );
    }
    let mut r = CtlRequest {
        typ,
        id: id.to_string(),
        patterns: None,
        scope: None,
    };
    if let Some(raw) = m.get("patterns").filter(|v| !v.is_null()) {
        match parse_patterns(raw, false) {
            Ok(ps) => r.patterns = Some(ps),
            Err(error) => return AcceptorControl::Reply(CtlError { id: r.id, error }),
        }
    }
    if typ == RequestType::Subscribe && !is_null(m.get("scope")) {
        match parse_scope(&m["scope"]) {
            Ok(s) => r.scope = Some(s),
            Err(error) => return AcceptorControl::Reply(CtlError { id: r.id, error }),
        }
    }
    if r.patterns.is_none() && (typ == RequestType::Unsubscribe || r.scope.is_none()) {
        return reply(
            id,
            CODE_BAD_REQUEST,
            format!("{} needs patterns", typ.as_str()),
        );
    }
    AcceptorControl::Request(r)
}

/// A reset frame: the dialer's position moves to `head`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ResetInfo {
    /// The acceptor's stream id.
    pub stream: String,
    /// Why events were skipped; unknown reasons are kept.
    pub reason: String,
    /// The position the dialer had (from the acceptor's view).
    pub after: u64,
    /// The new position.
    pub head: u64,
}

/// A position frame: the acceptor's cursor on a quiet link.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct PositionInfo {
    /// The acceptor's stream id.
    pub stream: String,
    /// The acceptor's cursor for this link.
    pub offset: u64,
}

/// A control frame as a dialer reads it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum DialerCtl {
    /// A request was applied; events after `at` use the new set.
    Ack {
        /// The request id.
        id: String,
        /// The acceptor's cursor when it applied the request.
        at: u64,
    },
    /// A request, or an unattributable frame, was refused.
    Error {
        /// The request id, empty when the acceptor could not read one.
        id: String,
        /// Contract code.
        code: String,
        /// Detail.
        message: String,
    },
    /// The acceptor skipped events.
    Reset(ResetInfo),
    /// The acceptor's position on a quiet link.
    Position(PositionInfo),
    /// Nothing the dialer can use.
    Ignore,
}

/// Handles a control frame received by a dialer. A malformed reset or
/// position is a protocol error; anything else it cannot use is ignored.
pub fn dialer_control(m: &Members) -> Result<DialerCtl, ProtocolError> {
    let stream = || member_str(m, "stream").filter(|s| valid_stream_id(s));
    match member_str(m, "type").unwrap_or("") {
        "ack" => Ok(
            match (member_str(m, "id"), parse_offset_token(m.get("at"))) {
                (Some(id), Some(at)) => DialerCtl::Ack {
                    id: id.to_string(),
                    at,
                },
                _ => DialerCtl::Ignore,
            },
        ),
        "error" => Ok(match member_str(m, "code").filter(|c| !c.is_empty()) {
            Some(code) => DialerCtl::Error {
                id: member_str(m, "id").unwrap_or("").to_string(),
                code: code.to_string(),
                message: member_str(m, "message").unwrap_or("").to_string(),
            },
            None => DialerCtl::Ignore,
        }),
        "reset" => {
            let reason = member_str(m, "reason").filter(|r| !r.is_empty());
            let after = parse_offset_token(m.get("after"));
            let head = parse_offset_token(m.get("head"));
            match (stream(), reason, after, head) {
                (Some(s), Some(r), Some(after), Some(head)) => Ok(DialerCtl::Reset(ResetInfo {
                    stream: s.to_string(),
                    reason: r.to_string(),
                    after,
                    head,
                })),
                _ => Err(ProtocolError::new(
                    ProtocolKind::ResetMalformed,
                    "malformed reset",
                )),
            }
        }
        "position" => match (stream(), parse_offset_token(m.get("offset"))) {
            (Some(s), Some(offset)) => Ok(DialerCtl::Position(PositionInfo {
                stream: s.to_string(),
                offset,
            })),
            _ => Err(ProtocolError::new(
                ProtocolKind::PositionMalformed,
                "malformed position",
            )),
        },
        _ => Ok(DialerCtl::Ignore),
    }
}

// Encoders. Field order follows Go's encoding/json, which sorts map keys,
// so the bytes match the Go port's (modulo Go's HTML escaping).

fn to_vec<T: Serialize>(v: &T) -> Vec<u8> {
    serde_json::to_vec(v).expect("control frames always encode")
}

/// Encodes a dialer's subscribe or unsubscribe request.
pub fn encode_request(r: &CtlRequest) -> Vec<u8> {
    #[derive(Serialize)]
    struct F<'a> {
        id: &'a str,
        #[serde(skip_serializing_if = "Option::is_none")]
        patterns: Option<&'a Vec<String>>,
        #[serde(skip_serializing_if = "Option::is_none")]
        scope: Option<&'a Scope>,
        #[serde(rename = "type")]
        typ: &'static str,
    }
    to_vec(&F {
        id: &r.id,
        patterns: r.patterns.as_ref(),
        scope: r.scope.as_ref(),
        typ: r.typ.as_str(),
    })
}

/// Encodes an acceptor's ack of request `id` at cursor `at`.
pub fn encode_ack_reply(id: &str, at: u64) -> Vec<u8> {
    #[derive(Serialize)]
    struct F<'a> {
        at: String,
        id: &'a str,
        #[serde(rename = "type")]
        typ: &'static str,
    }
    to_vec(&F {
        at: format_offset(at),
        id,
        typ: "ack",
    })
}

/// Encodes an acceptor's error reply; `id` is omitted when empty.
pub fn encode_error_reply(e: &CtlError) -> Vec<u8> {
    #[derive(Serialize)]
    struct F<'a> {
        code: &'a str,
        #[serde(skip_serializing_if = "str::is_empty")]
        id: &'a str,
        message: &'a str,
        #[serde(rename = "type")]
        typ: &'static str,
    }
    to_vec(&F {
        code: &e.error.code,
        id: &e.id,
        message: &e.error.message,
        typ: "error",
    })
}

/// Encodes a reset frame.
pub fn encode_reset(r: &ResetInfo) -> Vec<u8> {
    #[derive(Serialize)]
    struct F<'a> {
        after: String,
        head: String,
        reason: &'a str,
        stream: &'a str,
        #[serde(rename = "type")]
        typ: &'static str,
    }
    to_vec(&F {
        after: format_offset(r.after),
        head: format_offset(r.head),
        reason: &r.reason,
        stream: &r.stream,
        typ: "reset",
    })
}

/// Encodes a position frame.
pub fn encode_position(p: &PositionInfo) -> Vec<u8> {
    #[derive(Serialize)]
    struct F<'a> {
        offset: String,
        stream: &'a str,
        #[serde(rename = "type")]
        typ: &'static str,
    }
    to_vec(&F {
        offset: format_offset(p.offset),
        stream: &p.stream,
        typ: "position",
    })
}
