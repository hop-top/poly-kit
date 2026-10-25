//! The configuration, state and results of a [`StreamClient`].
//!
//! [`StreamClient`]: super::StreamClient

use std::fmt;

use serde_json::{json, Value};

use super::control::FrameKind;
use super::cursor::{GapInfo, LinkResume};
use super::error::{ProtocolError, StreamError};
use super::handshake::{FrameFormat, StreamCaps, STREAMS_VERSION};
use super::wire::{Members, Scope};

/// Caps the patterns one link may subscribe.
pub const DEFAULT_MAX_SUBSCRIPTIONS: usize = 1000;
/// A dialer closes a stream link after this many heartbeat intervals with
/// no frame of any kind.
pub const DEAD_AFTER_HEARTBEATS: u32 = 3;
/// WebSocket close status for a protocol error.
pub const CLOSE_PROTOCOL: u16 = 1002;
/// WebSocket close status for a refused handshake.
pub const CLOSE_POLICY: u16 = 1008;
/// WebSocket close status for a dead link.
pub const CLOSE_GOING_AWAY: u16 = 1001;

/// What a dialer asks of one outbound address.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ClientConfig {
    /// The outbound address, as the transport names it.
    pub addr: String,
    /// This adapter's origin id, sent in the auth frame.
    pub origin: String,
    /// The caps this side advertises; `streams` 0 when it cannot do
    /// streams (a bus without the publish hook).
    pub caps: StreamCaps,
    /// An authenticator is configured: the auth exchange runs and doubles
    /// as the stream handshake. Without it every link is legacy.
    pub handshake: bool,
    /// The configured send format.
    pub format: FrameFormat,
    /// Keep events published while the address is down.
    pub outbox: bool,
    /// Cap on this link's pattern set while the link is down.
    pub max_subscriptions: usize,
    /// Patterns asked for in every handshake; `None` keeps the implicit
    /// `#`.
    pub subscribe: Option<Vec<String>>,
    /// Workspace scope asked for in every handshake.
    pub scope: Option<Scope>,
    /// A persisted position: the first connect asks to resume after it.
    pub resume_from: Option<StreamPosition>,
}

impl ClientConfig {
    /// Defaults: streams v1 decoding kit and CloudEvents, a handshake,
    /// kit frames, no outbox, the implicit `#`, no scope, no position.
    pub fn new(addr: impl Into<String>, origin: impl Into<String>) -> Self {
        ClientConfig {
            addr: addr.into(),
            origin: origin.into(),
            caps: StreamCaps {
                streams: STREAMS_VERSION,
                frames: vec![FrameFormat::Kit, FrameFormat::CloudEvents],
            },
            handshake: true,
            format: FrameFormat::Kit,
            outbox: false,
            max_subscriptions: DEFAULT_MAX_SUBSCRIPTIONS,
            subscribe: None,
            scope: None,
            resume_from: None,
        }
    }
}

/// Where an event sits in the stream of the peer it came from. Delivery
/// metadata, not part of the event.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StreamPosition {
    /// The outbound address.
    pub peer: String,
    /// The acceptor's stream id.
    pub stream: String,
    /// The event's offset.
    pub offset: u64,
}

/// The state of an outbound address.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct LinkState {
    /// A link is up.
    pub connected: bool,
    /// The link negotiated streams.
    pub streams: bool,
    /// The negotiated send format.
    pub format: FrameFormat,
    /// The acceptor's stream id on a stream link.
    pub stream: String,
    /// How the last link came up.
    pub resume: Option<LinkResume>,
    /// The adapter will not reconnect.
    pub is_final: bool,
    /// Why the link last closed, if known.
    pub code: String,
}

/// A WebSocket close the transport must send.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct CloseFrame {
    /// Close status.
    pub status: u16,
    /// Close reason.
    pub reason: &'static str,
}

/// `kit.runtime.link.disconnected`, to publish.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Disconnected {
    /// The outbound address.
    pub peer: String,
    /// The adapter will not reconnect.
    pub is_final: bool,
    /// Why, when known.
    pub code: String,
}

impl Disconnected {
    /// The event payload: peer, final, and code when known.
    pub fn payload(&self) -> Value {
        let mut p = json!({"peer": self.peer, "final": self.is_final});
        if !self.code.is_empty() {
            p["code"] = json!(self.code);
        }
        p
    }
}

/// A link came up.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LinkUp {
    /// The new state; it also clears `is_final` from a refused session.
    pub state: LinkState,
    /// The invalidation to publish before anything else, if any.
    pub invalidated: Option<GapInfo>,
    /// The heartbeat interval to arm the watchdog with: the acceptor's on
    /// a stream link, none on a legacy one.
    pub heartbeat_ms: Option<u64>,
}

impl LinkUp {
    /// The `kit.runtime.link.connected` payload for `peer`.
    pub fn connected_payload(&self, peer: &str) -> Value {
        json!({
            "peer": peer,
            "stream": self.state.stream,
            "resume": self.state.resume.map_or("", LinkResume::as_str),
            "format": self.state.format.as_str(),
        })
    }
}

/// Why a handshake failed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum HandshakeError {
    /// The acceptor refused the session. Final.
    Session(StreamError),
    /// The ack broke the protocol. Final.
    Protocol(ProtocolError),
    /// The token was refused (an unusable reply, or a 1008 close).
    /// Retried: the authenticator may hand out a fresh token.
    AuthRejected,
    /// No ack before the transport's timeout. Retried.
    AckTimeout,
}

impl HandshakeError {
    /// The code reported in `link.disconnected`.
    pub fn code(&self) -> &str {
        match self {
            HandshakeError::Session(e) => &e.code,
            HandshakeError::Protocol(_) => "protocol",
            HandshakeError::AuthRejected => "auth",
            HandshakeError::AckTimeout => "timeout",
        }
    }

    /// No reconnect until the next explicit connect.
    pub fn is_final(&self) -> bool {
        matches!(
            self,
            HandshakeError::Session(_) | HandshakeError::Protocol(_)
        )
    }
}

impl fmt::Display for HandshakeError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            HandshakeError::Session(e) => e.fmt(f),
            HandshakeError::Protocol(e) => e.fmt(f),
            HandshakeError::AuthRejected => f.write_str("bus: network auth failed"),
            HandshakeError::AckTimeout => {
                f.write_str("bus: network auth failed: no ack before the timeout")
            }
        }
    }
}

impl std::error::Error for HandshakeError {}

/// A failed handshake, recorded on the address.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HandshakeFailure {
    /// Why.
    pub error: HandshakeError,
    /// The close to send, when the transport still holds the socket.
    pub close: Option<CloseFrame>,
    /// Publish `link.disconnected`; false for a repeat of a retrying
    /// failure, so a retrying address does not flood the bus.
    pub publish: bool,
}

impl HandshakeFailure {
    /// The `link.disconnected` event for `peer`, unless it is a repeat.
    pub fn disconnected(&self, peer: &str) -> Option<Disconnected> {
        self.publish.then(|| Disconnected {
            peer: peer.to_string(),
            is_final: self.error.is_final(),
            code: self.error.code().to_string(),
        })
    }
}

/// A live link broke the protocol: close it with
/// [`CLOSE_PROTOCOL`]; the dialer reconnects with resume.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LinkFailure {
    /// The violation.
    pub error: ProtocolError,
    /// The close to send.
    pub close: CloseFrame,
}

/// An event frame for the transport to decode.
#[derive(Clone, Debug, PartialEq)]
pub struct EventFrame {
    /// [`FrameKind::Kit`] or [`FrameKind::CloudEvents`].
    pub kind: FrameKind,
    /// The frame's members.
    pub members: Members,
}

impl EventFrame {
    /// The raw offset member: `offset` on kit frames, the `kitoffset`
    /// extension on CloudEvents frames.
    pub fn offset_member(&self) -> Option<&Value> {
        match self.kind {
            FrameKind::CloudEvents => self.members.get("kitoffset"),
            _ => self.members.get("offset"),
        }
    }
}

/// What a received frame means for the transport.
#[derive(Clone, Debug, PartialEq)]
pub enum Inbound {
    /// Nothing to do.
    Ignored,
    /// An event frame: decode it (dropping an unusable one), then call
    /// [`StreamClient::on_event`](super::StreamClient::on_event).
    Event(EventFrame),
    /// The reply to request `id`.
    Reply {
        /// The request id.
        id: String,
        /// The ack's offset, or the refusal.
        result: Result<u64, RequestError>,
    },
    /// Publish `kit.runtime.stream.invalidated` with this gap.
    Invalidated(GapInfo),
    /// The position moved (a position frame).
    Position,
}

/// What to do with a decoded event.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Delivery {
    /// Publish it locally; on a stream link with its position.
    Deliver(Option<StreamPosition>),
    /// Drop it: a duplicate, an echo of this adapter's own events, or a
    /// reserved topic.
    Drop,
}

/// A subscription request's next step.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Request {
    /// The link is down: the set was updated for the next handshake. The
    /// call returns offset 0.
    Applied,
    /// Send `frame`; the reply arrives as [`Inbound::Reply`] for `id`.
    Send {
        /// The request id.
        id: String,
        /// The control frame.
        frame: Vec<u8>,
    },
}

/// Why a subscription request failed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum RequestError {
    /// Called from a delivery of the link to the same address: the reply
    /// could only be read by the caller itself.
    Reentrant(String),
    /// Refused, locally or by the acceptor.
    Stream(StreamError),
    /// The link did not negotiate streams.
    StreamsUnsupported,
    /// The link closed before the reply.
    LinkClosed,
}

impl fmt::Display for RequestError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            RequestError::Reentrant(addr) => {
                write!(f, "bus: request from a delivery of the same link: {addr}")
            }
            RequestError::Stream(e) => e.fmt(f),
            RequestError::StreamsUnsupported => f.write_str("bus: peer does not support streams"),
            RequestError::LinkClosed => f.write_str("bus: link closed before the reply"),
        }
    }
}

impl std::error::Error for RequestError {}

impl From<StreamError> for RequestError {
    fn from(e: StreamError) -> Self {
        RequestError::Stream(e)
    }
}

/// A link went down.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct LinkDown {
    /// Requests waiting for a reply; they fail with
    /// [`RequestError::LinkClosed`].
    pub failed: Vec<String>,
    /// The event to publish.
    pub disconnected: Disconnected,
    /// Start the reconnect loop.
    pub reconnect: bool,
}

/// Marks a [`Context`](crate::bus::Context) as a delivery from the link
/// to an address. A request to that address carrying it fails with
/// [`RequestError::Reentrant`].
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct DeliveryMarker(pub String);
