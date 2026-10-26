//! The stream handshake: capabilities, the dialer's auth frame, the
//! heartbeat bounds and format negotiation (the ack is in `ack.rs`). It
//! rides the auth exchange; legacy peers ignore every key it adds.

use serde::Serialize;
use serde_json::Value;

use crate::bus::cloudevents::FrameFormat;

use super::error::{StreamError, CODE_BAD_REQUEST, CODE_INVALID_AUTH};
use super::wire::{
    format_offset, is_null, member_str, parse_offset_token, parse_patterns, parse_scope,
    valid_stream_id, Members, Scope,
};

/// The stream protocol version this core speaks.
pub const STREAMS_VERSION: u64 = 1;

/// Lower bound on a peer-advertised heartbeat: a hub cannot make its
/// dialers' watchdogs churn.
pub const MIN_HEARTBEAT_MS: u64 = 1_000;
/// Upper bound on a peer-advertised heartbeat: a day, so the interval
/// never overflows a duration.
pub const MAX_HEARTBEAT_MS: u64 = 86_400_000;
/// The acceptor's default heartbeat interval.
pub const DEFAULT_HEARTBEAT_MS: u64 = 15_000;

/// Clamps a configured heartbeat to [`MIN_HEARTBEAT_MS`] ..=
/// [`MAX_HEARTBEAT_MS`]; `None` for 0 (keep the default), as Go's
/// `WithHeartbeat` ignores a non-positive duration.
pub fn clamp_heartbeat_ms(ms: u64) -> Option<u64> {
    (ms > 0).then(|| ms.clamp(MIN_HEARTBEAT_MS, MAX_HEARTBEAT_MS))
}

/// The name a peer advertises for `f` in `caps.frames`.
pub(crate) fn format_name(f: FrameFormat) -> &'static str {
    match f {
        FrameFormat::Kit => "kit",
        FrameFormat::CloudEvents => "cloudevents",
    }
}

/// Parses an advertised frame format name; unknown names are `None`.
pub(crate) fn parse_format(s: &str) -> Option<FrameFormat> {
    match s {
        "kit" => Some(FrameFormat::Kit),
        "cloudevents" => Some(FrameFormat::CloudEvents),
        _ => None,
    }
}

fn format_names<S: serde::Serializer>(fs: &[FrameFormat], s: S) -> Result<S::Ok, S::Error> {
    s.collect_seq(fs.iter().map(|f| format_name(*f)))
}

/// The capability advertisement in the handshake.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct StreamCaps {
    /// Stream protocol version; 0 when the side cannot do streams.
    pub streams: u64,
    /// Frame formats this side decodes, in its order.
    #[serde(serialize_with = "format_names")]
    pub frames: Vec<FrameFormat>,
}

impl StreamCaps {
    /// Reports whether the peer decodes `f`.
    pub fn decodes(&self, f: FrameFormat) -> bool {
        self.frames.contains(&f)
    }
}

/// Decodes caps leniently: a non-object is absent, a bad `streams` is 0,
/// unknown frame names are dropped.
pub fn parse_caps(raw: Option<&Value>) -> Option<StreamCaps> {
    let m = raw?.as_object()?;
    // Go reads streams with Atoi on the raw JSON: an integer literal
    // without fraction or exponent, at most the platform int.
    let streams = m
        .get("streams")
        .and_then(Value::as_u64)
        .filter(|v| *v <= i64::MAX as u64)
        .unwrap_or(0);
    let frames = m
        .get("frames")
        .and_then(Value::as_array)
        .map(|fs| {
            fs.iter()
                .filter_map(|f| f.as_str().and_then(parse_format))
                .collect()
        })
        .unwrap_or_default();
    Some(StreamCaps { streams, frames })
}

/// A dialer's request to resume after `after` on stream `stream`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ResumeReq {
    /// The stream the position belongs to.
    pub stream: String,
    /// The last offset the dialer handled.
    pub after: u64,
}

/// The dialer's session request. `None` fields were absent.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct SessionReq {
    /// Initial explicit subscription; `None` keeps the implicit `#`.
    pub subscribe: Option<Vec<String>>,
    /// Workspace scope.
    pub scope: Option<Scope>,
    /// Where to resume.
    pub resume: Option<ResumeReq>,
}

/// The dialer's first frame as the acceptor reads it.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AuthFrame {
    /// The token, verified by the acceptor's authenticator.
    pub token: String,
    /// The dialer's caps; `None` for a legacy dialer.
    pub caps: Option<StreamCaps>,
    /// The dialer's origin id; empty when absent.
    pub origin: String,
    /// The session request, if any.
    pub session: Option<SessionReq>,
}

/// Why an auth frame is unusable.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum AuthFrameError {
    /// No usable token (code `invalid_auth`): close with 1008, no ack.
    InvalidAuth(StreamError),
    /// A malformed session, with a control-frame code. The frame (token,
    /// caps, origin) is returned so the acceptor can verify the token
    /// before refusing the session.
    Session {
        /// The frame without its session.
        frame: Box<AuthFrame>,
        /// The refusal.
        error: StreamError,
    },
}

impl AuthFrameError {
    /// The refusal.
    pub fn error(&self) -> &StreamError {
        match self {
            AuthFrameError::InvalidAuth(e) | AuthFrameError::Session { error: e, .. } => e,
        }
    }
}

/// Decodes the dialer's first frame.
pub fn parse_auth_frame(data: &[u8]) -> Result<AuthFrame, AuthFrameError> {
    let invalid = |msg: &str| AuthFrameError::InvalidAuth(StreamError::new(CODE_INVALID_AUTH, msg));
    let m: Members = match serde_json::from_slice(data) {
        Ok(Value::Object(m)) => m,
        _ => return Err(invalid("not a JSON object")),
    };
    let Some(token) = member_str(&m, "auth").filter(|t| !t.is_empty()) else {
        return Err(invalid("auth must be a non-empty string"));
    };
    let frame = AuthFrame {
        token: token.to_string(),
        caps: parse_caps(m.get("caps")),
        origin: member_str(&m, "origin").unwrap_or("").to_string(),
        session: None,
    };
    if is_null(m.get("session")) {
        return Ok(frame);
    }
    match parse_session(&m["session"]) {
        Ok(s) => Ok(AuthFrame {
            session: Some(s),
            ..frame
        }),
        Err(error) => Err(AuthFrameError::Session {
            frame: Box::new(frame),
            error,
        }),
    }
}

fn parse_session(raw: &Value) -> Result<SessionReq, StreamError> {
    let Some(s) = raw.as_object() else {
        return Err(StreamError::new(
            CODE_BAD_REQUEST,
            "session must be an object",
        ));
    };
    let mut sess = SessionReq::default();
    if !is_null(s.get("subscribe")) {
        sess.subscribe = Some(parse_patterns(&s["subscribe"], true)?);
    }
    if !is_null(s.get("scope")) {
        sess.scope = Some(parse_scope(&s["scope"])?);
    }
    if !is_null(s.get("resume")) {
        let r = s["resume"].as_object();
        let stream = r
            .and_then(|r| member_str(r, "stream"))
            .filter(|s| valid_stream_id(s));
        let after = r.and_then(|r| parse_offset_token(r.get("after")));
        let (Some(stream), Some(after)) = (stream, after) else {
            return Err(StreamError::new(
                CODE_BAD_REQUEST,
                "resume needs a stream id and an after offset",
            ));
        };
        sess.resume = Some(ResumeReq {
            stream: stream.to_string(),
            after,
        });
    }
    Ok(sess)
}

/// Encodes the dialer's first frame. Without caps it is the legacy
/// `{"auth": token}`.
pub fn encode_auth_frame(
    token: &str,
    caps: Option<&StreamCaps>,
    origin: &str,
    session: Option<&SessionReq>,
) -> Vec<u8> {
    #[derive(Serialize)]
    struct Resume<'a> {
        after: String,
        stream: &'a str,
    }
    #[derive(Serialize)]
    struct Session<'a> {
        #[serde(skip_serializing_if = "Option::is_none")]
        resume: Option<Resume<'a>>,
        #[serde(skip_serializing_if = "Option::is_none")]
        scope: Option<&'a Scope>,
        #[serde(skip_serializing_if = "Option::is_none")]
        subscribe: Option<&'a Vec<String>>,
    }
    #[derive(Serialize)]
    struct F<'a> {
        auth: &'a str,
        #[serde(skip_serializing_if = "Option::is_none")]
        caps: Option<&'a StreamCaps>,
        #[serde(skip_serializing_if = "Option::is_none")]
        origin: Option<&'a str>,
        #[serde(skip_serializing_if = "Option::is_none")]
        session: Option<Session<'a>>,
    }
    let session = caps.and(session).map(|s| Session {
        resume: s.resume.as_ref().map(|r| Resume {
            after: format_offset(r.after),
            stream: &r.stream,
        }),
        scope: s.scope.as_ref(),
        subscribe: s.subscribe.as_ref(),
    });
    let f = F {
        auth: token,
        caps,
        origin: caps.map(|_| origin),
        session,
    };
    serde_json::to_vec(&f).expect("auth frames always encode")
}

/// Returns the link's stream version and the format to send: the
/// configured one when there was no handshake or the peer lists it, else
/// kit. Without peer caps the version is 0.
pub fn negotiate(
    mine: &StreamCaps,
    peer: Option<&StreamCaps>,
    handshake: bool,
    configured: FrameFormat,
) -> (u64, FrameFormat) {
    let v = peer.map_or(0, |p| mine.streams.min(p.streams));
    if !handshake || peer.is_some_and(|p| p.decodes(configured)) {
        (v, configured)
    } else {
        (v, FrameFormat::Kit)
    }
}
