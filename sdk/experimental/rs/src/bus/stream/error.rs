//! Stream errors: refused requests ([`StreamError`], with contract codes)
//! and protocol violations ([`ProtocolError`], with contract kinds).

use std::fmt;

/// Error codes of control-frame replies and session errors. They are
/// contract; messages are not.
pub const CODE_BAD_REQUEST: &str = "bad_request";
/// A subscribe pattern failed [`validate_pattern`](super::validate_pattern).
pub const CODE_INVALID_PATTERN: &str = "invalid_pattern";
/// A scope member is not `{"workspaces": [string...]}`.
pub const CODE_INVALID_SCOPE: &str = "invalid_scope";
/// A subscription set would exceed the link's cap.
pub const CODE_LIMIT: &str = "limit";
/// The acceptor's authorizer refused the subscription.
pub const CODE_FORBIDDEN: &str = "forbidden";
/// A control frame of a type the acceptor does not know.
pub const CODE_UNKNOWN_TYPE: &str = "unknown_type";
/// An unusable auth frame. It never goes on the wire: the acceptor closes
/// with 1008, as before streams.
pub const CODE_INVALID_AUTH: &str = "invalid_auth";

/// A refused stream request: a control-frame error reply or a handshake
/// session error. Displays as Go's `*StreamError` does.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct StreamError {
    /// Contract code, one of the `CODE_*` constants or a peer's code.
    pub code: String,
    /// Human-readable detail; not contract.
    pub message: String,
}

impl StreamError {
    /// Builds an error with `code` and `message`.
    pub fn new(code: impl Into<String>, message: impl Into<String>) -> Self {
        StreamError {
            code: code.into(),
            message: message.into(),
        }
    }
}

impl fmt::Display for StreamError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "bus: stream: {}: {}", self.code, self.message)
    }
}

impl std::error::Error for StreamError {}

/// What kind of protocol violation a [`ProtocolError`] is. Vectors pin
/// the kind, so a case cannot fail for the wrong reason.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum ProtocolKind {
    /// Event frame offset missing or malformed.
    Offset,
    /// Ack stream id or offsets malformed.
    StreamInfo,
    /// Ack `oldest` beyond `head + 1`.
    Oldest,
    /// Ack `heartbeat_ms` missing or out of range.
    Heartbeat,
    /// Ack resume outcome malformed.
    Resume,
    /// Ack resume outcome without a request.
    Unrequested,
    /// Ack replay `after` differs from the request or exceeds the head.
    ReplayAfter,
    /// Reset head beyond the stream head, or below the position.
    ResetHead,
    /// Reset `after` beyond its head.
    ResetAfter,
    /// Reset frame members malformed.
    ResetMalformed,
    /// Position frame members malformed.
    PositionMalformed,
    /// Reset or position naming another stream.
    ForeignStream,
    /// Puback offset, status or code missing or malformed.
    PubackMalformed,
    /// Puback offset beyond the last frame sent on the link.
    PubackAhead,
    /// Puback id differs from the event sent at that offset.
    PubackId,
}

impl ProtocolKind {
    /// The kind as the vectors spell it.
    pub fn as_str(self) -> &'static str {
        match self {
            ProtocolKind::Offset => "offset",
            ProtocolKind::StreamInfo => "stream_info",
            ProtocolKind::Oldest => "oldest",
            ProtocolKind::Heartbeat => "heartbeat",
            ProtocolKind::Resume => "resume",
            ProtocolKind::Unrequested => "unrequested",
            ProtocolKind::ReplayAfter => "replay_after",
            ProtocolKind::ResetHead => "reset_head",
            ProtocolKind::ResetAfter => "reset_after",
            ProtocolKind::ResetMalformed => "reset_malformed",
            ProtocolKind::PositionMalformed => "position_malformed",
            ProtocolKind::ForeignStream => "foreign_stream",
            ProtocolKind::PubackMalformed => "puback_malformed",
            ProtocolKind::PubackAhead => "puback_ahead",
            ProtocolKind::PubackId => "puback_id",
        }
    }
}

/// A frame that breaks the stream protocol. The link closes with 1002
/// and, on a dialer, the handshake or link failure is final.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ProtocolError {
    /// The contract kind.
    pub kind: ProtocolKind,
    /// Detail; not contract.
    pub message: String,
}

impl ProtocolError {
    pub(crate) fn new(kind: ProtocolKind, message: impl Into<String>) -> Self {
        ProtocolError {
            kind,
            message: message.into(),
        }
    }
}

impl fmt::Display for ProtocolError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "bus: stream protocol error: {}", self.message)
    }
}

impl std::error::Error for ProtocolError {}

/// Why a dialer could not use the acceptor's handshake reply.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum AckError {
    /// Not an `auth_ok` or `session_error` reply: the token was refused.
    Rejected,
    /// A malformed extended ack; final for the dialer.
    Protocol(ProtocolError),
}

impl fmt::Display for AckError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            AckError::Rejected => f.write_str("bus: auth rejected"),
            AckError::Protocol(e) => e.fmt(f),
        }
    }
}

impl std::error::Error for AckError {}

impl From<ProtocolError> for AckError {
    fn from(e: ProtocolError) -> Self {
        AckError::Protocol(e)
    }
}
