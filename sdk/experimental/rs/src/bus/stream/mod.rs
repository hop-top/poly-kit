//! Bus network streams: the I/O-free core.
//!
//! Ports the pure half of Go's stream semantics
//! (`hop.top/kit/go/runtime/bus`, `stream_*.go` and the link logic of
//! `network_*.go`), verified by the shared vectors in
//! `contracts/bus-v1/streams`. The design is
//! `docs/contributors/architecture/bus-streams.md`.
//!
//! Nothing here touches a socket, a clock or a random source, so the same
//! code drives a native WebSocket transport and a browser one on
//! `wasm32-unknown-unknown`. Bytes go in, decisions come out:
//!
//! - wire primitives: offset tokens, patterns, scopes ([`parse_offset`],
//!   [`validate_pattern`], [`Scope`])
//! - frames: classification and control frames for both sides
//!   ([`classify_frame`], [`acceptor_control`], [`dialer_control`])
//! - the handshake: caps, the auth frame, the ack, negotiation and the
//!   heartbeat bounds ([`parse_auth_frame`], [`parse_ack`], [`negotiate`])
//! - subscriptions and the per-link delivery predicate ([`SubState`],
//!   [`entry_delivers`])
//! - positions: the resume decision, the dialer cursor and the inbound
//!   queue ([`decide_resume`], [`DialerCursor`], [`InboundQueue`])
//! - the send log and the outbox ([`RingLog`], [`SendCursor`],
//!   [`OutboundWriter`])
//! - [`StreamClient`]: a dialer's link state machine over all of the
//!   above, for one outbound address
//!
//! Event frames themselves (kit and CloudEvents encoding, the `offset`
//! and `kitoffset` members) are the transport's: the core hands it the
//! classified members and takes back the raw offset member.

mod ack;
mod client;
mod control;
mod cursor;
mod error;
mod handshake;
mod log;
mod requests;
mod subs;
mod types;
mod wire;

pub use ack::{
    encode_ack, encode_session_error, parse_ack, Ack, AuthOk, ResumeOutcome, ResumeStatus,
    StreamInfo,
};
pub use client::StreamClient;
pub use control::{
    acceptor_control, classify_frame, dialer_control, encode_ack_reply, encode_error_reply,
    encode_position, encode_request, encode_reset, AcceptorControl, CtlError, CtlRequest,
    DialerCtl, FrameKind, PositionInfo, RequestType, ResetInfo,
};
pub use cursor::{
    check_ack, decide_resume, DialerCursor, GapInfo, InboundQueue, LinkResume, REASON_AHEAD,
    REASON_EVICTED, REASON_LAGGED, REASON_STREAM, REASON_UNSUPPORTED, REASON_UPSTREAM,
};
pub use error::{
    AckError, ProtocolError, ProtocolKind, StreamError, CODE_BAD_REQUEST, CODE_FORBIDDEN,
    CODE_INVALID_AUTH, CODE_INVALID_PATTERN, CODE_INVALID_SCOPE, CODE_LIMIT, CODE_UNKNOWN_TYPE,
};
pub use handshake::{
    clamp_heartbeat_ms, encode_auth_frame, negotiate, parse_auth_frame, parse_caps, AuthFrame,
    AuthFrameError, FrameFormat, ResumeReq, SessionReq, StreamCaps, DEFAULT_HEARTBEAT_MS,
    MAX_HEARTBEAT_MS, MIN_HEARTBEAT_MS, STREAMS_VERSION,
};
pub use log::{
    Batch, LogEntry, OutboundWriter, RingLog, SendCursor, StreamLog, DEFAULT_STREAM_LOG_SIZE,
    WRITER_BATCH,
};
pub use subs::{entry_delivers, SubState, Subscription};
pub use types::{
    ClientConfig, CloseFrame, Delivery, DeliveryMarker, Disconnected, EventFrame, HandshakeError,
    HandshakeFailure, Inbound, LinkDown, LinkFailure, LinkState, LinkUp, Request, RequestError,
    StreamPosition, CLOSE_GOING_AWAY, CLOSE_POLICY, CLOSE_PROTOCOL, DEAD_AFTER_HEARTBEATS,
    DEFAULT_MAX_SUBSCRIPTIONS,
};
pub use wire::{
    format_offset, parse_offset, parse_offset_token, valid_stream_id, validate_pattern, Members,
    Scope,
};

/// Topics the network adapter publishes on the local bus. It owns
/// `kit.runtime.stream.*` and `kit.runtime.link.*`: they never enter a
/// log, and inbound frames carrying them are dropped.
///
/// The dialer missed events from a peer; mark every live view fed by it
/// stale and refetch. Payload: peer, stream, reason, after, head.
pub const TOPIC_STREAM_INVALIDATED: &str = "kit.runtime.stream.invalidated";
/// An outbound link came up. Payload: peer, stream, resume, format.
pub const TOPIC_LINK_CONNECTED: &str = "kit.runtime.link.connected";
/// An outbound link went down. Payload: peer, final, code (when known).
pub const TOPIC_LINK_DISCONNECTED: &str = "kit.runtime.link.disconnected";
/// The `source` of events the adapter publishes.
pub const NETWORK_EVENT_SOURCE: &str = "kit.bus.network";

/// Reports whether `topic` is one the adapter owns; inbound frames
/// carrying it are dropped.
pub fn is_reserved_topic(topic: &str) -> bool {
    topic.starts_with("kit.runtime.stream.") || topic.starts_with("kit.runtime.link.")
}
