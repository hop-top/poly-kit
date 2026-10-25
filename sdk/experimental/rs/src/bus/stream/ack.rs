//! The acceptor's handshake reply: stream info, the resume outcome, and
//! the `auth_ok` / `session_error` frames.

use serde::Serialize;
use serde_json::Value;

use super::error::{AckError, ProtocolError, ProtocolKind, StreamError};
use super::handshake::{parse_caps, StreamCaps, MAX_HEARTBEAT_MS, MIN_HEARTBEAT_MS};
use super::wire::{
    format_offset, is_null, member_str, parse_offset_token, valid_stream_id, Members,
};
use crate::bus::quote::quote;

/// The acceptor's stream, as advertised in the ack.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StreamInfo {
    /// Stream id, 1-128 printable ASCII bytes.
    pub id: String,
    /// First retained offset; `head + 1` when empty.
    pub oldest: u64,
    /// Last assigned offset.
    pub head: u64,
    /// The acceptor's heartbeat interval, in
    /// [`MIN_HEARTBEAT_MS`] ..= [`MAX_HEARTBEAT_MS`].
    pub heartbeat_ms: u64,
}

/// A resume outcome status.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ResumeStatus {
    /// Replaying from the requested `after`.
    Replaying,
    /// Starting at the head; the reason says why.
    Reset,
}

impl ResumeStatus {
    /// The status on the wire.
    pub fn as_str(self) -> &'static str {
        match self {
            ResumeStatus::Replaying => "replaying",
            ResumeStatus::Reset => "reset",
        }
    }

    /// Parses a wire status.
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "replaying" => Some(ResumeStatus::Replaying),
            "reset" => Some(ResumeStatus::Reset),
            _ => None,
        }
    }
}

/// The acceptor's answer to a resume request.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ResumeOutcome {
    /// Replaying or reset.
    pub status: ResumeStatus,
    /// Reset reason (see the `REASON_*` constants); empty when replaying.
    pub reason: String,
    /// The requested `after`.
    pub after: u64,
    /// The log head when the decision was made.
    pub head: u64,
}

/// An `auth_ok` reply as the dialer reads it.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct AuthOk {
    /// The acceptor's caps; `None` for a plain ack.
    pub caps: Option<StreamCaps>,
    /// The acceptor's origin id.
    pub origin: String,
    /// Present only when the negotiated version is at least 1.
    pub stream: Option<StreamInfo>,
    /// Present only when a resume outcome was sent.
    pub resume: Option<ResumeOutcome>,
}

impl AuthOk {
    /// The acceptor's advertised stream version, 0 without caps.
    pub fn streams(&self) -> u64 {
        self.caps.as_ref().map_or(0, |c| c.streams)
    }
}

/// The acceptor's reply.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Ack {
    /// The token and session were accepted.
    AuthOk(AuthOk),
    /// The session was refused; final for the dialer.
    SessionError(StreamError),
}

fn protocol(kind: ProtocolKind, msg: String) -> AckError {
    AckError::Protocol(ProtocolError::new(kind, msg))
}

/// Decodes the acceptor's reply. `mine` is the dialer's own streams
/// version: only when the negotiated version, `min(mine, caps.streams)`,
/// is at least 1 must the stream info and any resume outcome be well
/// formed; below that they are ignored.
pub fn parse_ack(data: &[u8], mine: u64) -> Result<Ack, AckError> {
    let m: Members = match serde_json::from_slice(data) {
        Ok(Value::Object(m)) => m,
        _ => return Err(AckError::Rejected),
    };
    match member_str(&m, "type").unwrap_or("") {
        "session_error" => {
            return Ok(Ack::SessionError(StreamError::new(
                member_str(&m, "code").unwrap_or(""),
                member_str(&m, "message").unwrap_or(""),
            )))
        }
        "auth_ok" => {}
        _ => return Err(AckError::Rejected),
    }
    let mut a = AuthOk {
        caps: parse_caps(m.get("caps")),
        origin: member_str(&m, "origin").unwrap_or("").to_string(),
        ..AuthOk::default()
    };
    if mine.min(a.streams()) < 1 {
        return Ok(Ack::AuthOk(a));
    }
    let empty = Members::new();
    let st = m.get("stream").and_then(Value::as_object).unwrap_or(&empty);
    let id = member_str(st, "id").filter(|s| valid_stream_id(s));
    let oldest = parse_offset_token(st.get("oldest"));
    let head = parse_offset_token(st.get("head"));
    let (Some(id), Some(oldest), Some(head)) = (id, oldest, head) else {
        return Err(protocol(
            ProtocolKind::StreamInfo,
            "ack: malformed stream info".into(),
        ));
    };
    if oldest > head && oldest - head > 1 {
        return Err(protocol(
            ProtocolKind::Oldest,
            format!("ack: oldest {oldest} beyond head {head}"),
        ));
    }
    // Go parses the raw member with ParseInt: a JSON integer literal only.
    let hb = st.get("heartbeat_ms").and_then(Value::as_i64);
    let heartbeat_ms = match hb {
        Some(v) if (MIN_HEARTBEAT_MS as i64..=MAX_HEARTBEAT_MS as i64).contains(&v) => v as u64,
        _ => {
            return Err(protocol(
                ProtocolKind::Heartbeat,
                format!("ack: heartbeat_ms outside {MIN_HEARTBEAT_MS}..{MAX_HEARTBEAT_MS}"),
            ))
        }
    };
    a.stream = Some(StreamInfo {
        id: id.to_string(),
        oldest,
        head,
        heartbeat_ms,
    });
    let sess = m.get("session").and_then(Value::as_object);
    let resume = sess.and_then(|s| s.get("resume"));
    if is_null(resume) {
        return Ok(Ack::AuthOk(a));
    }
    let r = resume.and_then(Value::as_object).unwrap_or(&empty);
    let status = member_str(r, "status");
    let reason = member_str(r, "reason").unwrap_or("");
    let after = parse_offset_token(r.get("after"));
    let rhead = parse_offset_token(r.get("head"));
    let (Some(status), Some(after), Some(rhead)) = (status, after, rhead) else {
        return Err(protocol(
            ProtocolKind::Resume,
            "ack: malformed resume outcome".into(),
        ));
    };
    if status == "reset" && reason.is_empty() {
        return Err(protocol(
            ProtocolKind::Resume,
            "ack: reset without a reason".into(),
        ));
    }
    let Some(status) = ResumeStatus::parse(status) else {
        return Err(protocol(
            ProtocolKind::Resume,
            format!("ack: unknown resume status {}", quote(status)),
        ));
    };
    a.resume = Some(ResumeOutcome {
        status,
        reason: reason.to_string(),
        after,
        head: rhead,
    });
    Ok(Ack::AuthOk(a))
}

/// Encodes the acceptor's reply. A dialer that sent no caps gets exactly
/// `{"type":"auth_ok"}`.
pub fn encode_ack(
    dialer_caps: bool,
    caps: &StreamCaps,
    origin: &str,
    si: Option<&StreamInfo>,
    ro: Option<&ResumeOutcome>,
) -> Vec<u8> {
    if !dialer_caps {
        return br#"{"type":"auth_ok"}"#.to_vec();
    }
    #[derive(Serialize)]
    struct Stream<'a> {
        head: String,
        heartbeat_ms: u64,
        id: &'a str,
        oldest: String,
    }
    #[derive(Serialize)]
    struct Resume<'a> {
        after: String,
        head: String,
        #[serde(skip_serializing_if = "str::is_empty")]
        reason: &'a str,
        status: &'static str,
    }
    #[derive(Serialize)]
    struct Session<'a> {
        resume: Resume<'a>,
    }
    #[derive(Serialize)]
    struct F<'a> {
        caps: &'a StreamCaps,
        origin: &'a str,
        #[serde(skip_serializing_if = "Option::is_none")]
        session: Option<Session<'a>>,
        #[serde(skip_serializing_if = "Option::is_none")]
        stream: Option<Stream<'a>>,
        #[serde(rename = "type")]
        typ: &'static str,
    }
    let f = F {
        caps,
        origin,
        session: ro.map(|r| Session {
            resume: Resume {
                after: format_offset(r.after),
                head: format_offset(r.head),
                reason: &r.reason,
                status: r.status.as_str(),
            },
        }),
        stream: si.map(|s| Stream {
            head: format_offset(s.head),
            heartbeat_ms: s.heartbeat_ms,
            id: &s.id,
            oldest: format_offset(s.oldest),
        }),
        typ: "auth_ok",
    };
    serde_json::to_vec(&f).expect("acks always encode")
}

/// Encodes a refused session.
pub fn encode_session_error(e: &StreamError) -> Vec<u8> {
    #[derive(Serialize)]
    struct F<'a> {
        code: &'a str,
        message: &'a str,
        #[serde(rename = "type")]
        typ: &'static str,
    }
    serde_json::to_vec(&F {
        code: &e.code,
        message: &e.message,
        typ: "session_error",
    })
    .expect("session errors always encode")
}
