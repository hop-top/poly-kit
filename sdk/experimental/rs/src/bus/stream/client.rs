//! [`StreamClient`]: a dialer's link state machine for one outbound
//! address, sans I/O.
//!
//! The transport owns the socket, the timers and the local bus; the
//! client owns every decision. One address, in order:
//!
//! 1. [`StreamClient::begin_handshake`] returns a handshake id and the
//!    auth frame to send; the reply on that socket goes to
//!    [`StreamClient::on_ack`] with the id (or
//!    [`StreamClient::handshake_failed`] on a timeout or a 1008 close). A
//!    late or stale reply is refused, never applied to a later dial.
//!    Without an authenticator, [`StreamClient::begin_legacy`] then
//!    [`StreamClient::link_up_legacy`].
//! 2. Every received frame goes to [`StreamClient::on_frame`]; a decoded
//!    event's offset to [`StreamClient::on_event`]. Every heartbeat
//!    interval, [`StreamClient::heartbeat_tick`].
//! 3. The writer loops [`StreamClient::next_batch`] over the send log;
//!    a write is reported by [`StreamClient::write_ok`] or
//!    [`StreamClient::write_failed`].
//! 4. When a live link's socket closes, [`StreamClient::link_down`].
//! 5. [`StreamClient::disconnect`] forgets the address for good; a later
//!    connect to it is a new `StreamClient`.
//!
//! It holds no lock and reads no clock; wrap it in the transport's own
//! synchronisation.

use std::collections::BTreeMap;

use serde_json::Value;

use super::ack::AuthOk;
use super::control::{dialer_control, CtlRequest, DialerCtl, FrameKind};
use super::cursor::{DialerCursor, LinkResume};
use super::error::{ProtocolError, StreamError};
use super::handshake::{negotiate, ResumeReq};
use super::log::{OutboundWriter, StreamLog};
use super::puback::{negotiate_acks, parse_puback, AckResolution, AckTracker};
use super::subs::SubState;
use super::types::{
    ClientConfig, CloseFrame, Delivery, DeliveryMarker, Disconnected, EventFrame, HandshakeError,
    HandshakeFailure, HandshakeId, Inbound, LinkDown, LinkFailure, LinkState, LinkUp, RequestError,
    StreamPosition, CLOSE_GOING_AWAY, CLOSE_PROTOCOL, DEAD_AFTER_HEARTBEATS,
};
use super::wire::member_str;
use super::{classify_frame, is_reserved_topic};
use crate::bus::Context;

/// One live link.
#[derive(Debug)]
pub(super) struct Link {
    pub(super) stream: bool,
    pub(super) acks: bool,
    pub(super) peer_origin: String,
    pub(super) pending: BTreeMap<String, CtlRequest>,
    pub(super) next_id: u64,
    frames: u64,
    seen: u64,
    misses: u32,
    close_code: String,
    pub(super) closing: bool,
    pub(super) write_failures: u32,
}

#[derive(Debug)]
pub(super) enum Phase {
    Down,
    Handshaking {
        id: HandshakeId,
        sent: Option<ResumeReq>,
    },
    Up(Link),
}

/// A dialer's state for one outbound address, kept across reconnects:
/// position, subscription set, outbox and link state.
#[derive(Debug)]
pub struct StreamClient {
    pub(super) cfg: ClientConfig,
    pub(super) cursor: DialerCursor,
    pub(super) subs: SubState,
    pub(super) writer: OutboundWriter,
    connected_once: bool,
    pub(super) state: LinkState,
    pub(super) phase: Phase,
    pub(super) outbox_dropped: u64,
    pub(super) skipped: u64,
    pub(super) forgotten: bool,
    pub(super) acks: AckTracker,
}

impl StreamClient {
    /// Registers the address. With the outbox on, events appended to
    /// `log` from now on are kept until a link sends them.
    pub fn new(cfg: ClientConfig, log: &dyn StreamLog) -> Self {
        let mut subs = SubState::new();
        if let Some(ps) = &cfg.subscribe {
            subs = subs.with_patterns(ps.iter().cloned());
        }
        subs.scope = cfg.scope.clone();
        let cursor = match &cfg.resume_from {
            Some(p) => DialerCursor {
                stream: p.stream.clone(),
                offset: p.offset,
                held: true,
            },
            None => DialerCursor::default(),
        };
        let writer = OutboundWriter::new(log, cfg.outbox);
        StreamClient {
            acks: AckTracker::new(writer.cursor().at),
            writer,
            cfg,
            cursor,
            subs,
            connected_once: false,
            state: LinkState::default(),
            phase: Phase::Down,
            outbox_dropped: 0,
            skipped: 0,
            forgotten: false,
        }
    }

    /// The outbound address.
    pub fn addr(&self) -> &str {
        &self.cfg.addr
    }

    /// The last offset handled from the peer's stream, if a position is
    /// held.
    pub fn position(&self) -> Option<StreamPosition> {
        self.cursor.held.then(|| StreamPosition {
            peer: self.cfg.addr.clone(),
            stream: self.cursor.stream.clone(),
            offset: self.cursor.offset,
        })
    }

    /// The address's link state.
    pub fn link_state(&self) -> &LinkState {
        &self.state
    }

    /// The subscription set sent in the next handshake.
    pub fn subscription(&self) -> &SubState {
        &self.subs
    }

    /// Events the outbox dropped (evicted while the address was down).
    pub fn outbox_dropped(&self) -> u64 {
        self.outbox_dropped
    }

    /// Log entries a connected link skipped because they were evicted
    /// before the writer reached them.
    pub fn skipped(&self) -> u64 {
        self.skipped
    }

    /// A context marked as a delivery from this address's link. Pass it
    /// to the handlers the link's reader runs inline (sync handlers and
    /// observers) for every event, invalidation and `link.connected`: a
    /// request made with it fails instead of waiting for a reply only the
    /// caller could read. Handlers that run off the delivering thread
    /// (async handlers, spawned tasks) must get
    /// [`StreamClient::clear_delivery`] of it instead, so their requests
    /// go through.
    pub fn delivery_context(&self, parent: &Context) -> Context {
        parent.with_value(DeliveryMarker(self.cfg.addr.clone()))
    }

    /// `ctx` without the delivery marker: the marker is shadowed by an
    /// empty one, which matches no address. Use it for async handlers.
    pub fn clear_delivery(ctx: &Context) -> Context {
        ctx.with_value(DeliveryMarker(String::new()))
    }

    pub(super) fn link_up(
        &mut self,
        ack: Option<AuthOk>,
        sent: Option<ResumeReq>,
        log: &dyn StreamLog,
    ) -> Result<LinkUp, HandshakeFailure> {
        let protocol = |me: &mut Self, p: ProtocolError| {
            let close = Some(CloseFrame {
                status: CLOSE_PROTOCOL,
                reason: "protocol error",
            });
            me.fail(HandshakeError::Protocol(p), close)
        };
        if let Err(p) = super::check_ack(ack.as_ref(), sent.as_ref()) {
            return Err(protocol(self, p));
        }
        let peer_caps = ack.as_ref().and_then(|a| a.caps.as_ref());
        let (v, format) = negotiate(
            &self.cfg.caps,
            peer_caps,
            self.cfg.handshake,
            self.cfg.format,
        );
        let ack = ack.filter(|a| v >= 1 && a.stream.is_some());
        let (resume, invalidated) =
            match self
                .cursor
                .link_up(ack.as_ref(), sent.as_ref(), self.connected_once)
            {
                Ok(r) => r,
                Err(p) => return Err(protocol(self, p)),
            };
        self.connected_once = true;
        self.writer.link_up(log);
        let stream = ack.as_ref().and_then(|a| a.stream.as_ref());
        let acks = negotiate_acks(
            self.cfg.caps.acks,
            ack.as_ref().and_then(|a| a.caps.as_ref()),
            stream.is_some(),
        );
        let (rewind, settled) = self.acks.link_up(acks, self.writer.cursor().at);
        self.writer.rewind(rewind);
        self.state = LinkState {
            connected: true,
            streams: stream.is_some(),
            format,
            stream: stream.map(|s| s.id.clone()).unwrap_or_default(),
            resume: Some(resume),
            is_final: false,
            code: String::new(),
        };
        self.phase = Phase::Up(Link {
            stream: stream.is_some(),
            acks,
            peer_origin: ack.as_ref().map(|a| a.origin.clone()).unwrap_or_default(),
            pending: BTreeMap::new(),
            next_id: 0,
            frames: 0,
            seen: 0,
            misses: 0,
            close_code: String::new(),
            closing: false,
            write_failures: 0,
        });
        Ok(LinkUp {
            state: self.state.clone(),
            invalidated,
            heartbeat_ms: stream.map(|s| s.heartbeat_ms),
            acks,
            settled,
        })
    }

    // Live link.

    pub(super) fn link_mut(&mut self) -> Option<&mut Link> {
        match &mut self.phase {
            Phase::Up(l) => Some(l),
            _ => None,
        }
    }

    fn broke(&mut self, error: ProtocolError) -> LinkFailure {
        if let Some(l) = self.link_mut() {
            l.close_code = "protocol".into();
        }
        LinkFailure {
            error,
            close: CloseFrame {
                status: CLOSE_PROTOCOL,
                reason: "protocol error",
            },
        }
    }

    /// Handles one frame received on the live link. A protocol error
    /// closes the link; it is not final, so the dialer reconnects and
    /// resumes.
    /// Once the link is closing (a failed write, or
    /// [`StreamClient::disconnect`]) every frame is ignored.
    pub fn on_frame(&mut self, data: &[u8]) -> Result<Inbound, LinkFailure> {
        let Some(l) = self.link_mut().filter(|l| !l.closing) else {
            return Ok(Inbound::Ignored);
        };
        l.frames += 1;
        let (kind, members) = classify_frame(data, l.stream);
        let members = match (kind, members) {
            (FrameKind::Drop, _) | (_, None) => return Ok(Inbound::Ignored),
            (FrameKind::Control, Some(m)) => m,
            (kind, Some(members)) => return Ok(Inbound::Event(EventFrame { kind, members })),
        };
        if l.acks && member_str(&members, "type") == Some("puback") {
            return match parse_puback(&members).and_then(|p| self.acks.on_puback(&p)) {
                Ok(rs) => Ok(Inbound::Acked(rs)),
                Err(p) => Err(self.broke(p)),
            };
        }
        let dc = match dialer_control(&members) {
            Ok(dc) => dc,
            Err(p) => return Err(self.broke(p)),
        };
        match dc {
            DialerCtl::Ack { id, at } => Ok(self.reply(id, Ok(at))),
            DialerCtl::Error { id, code, message } => Ok(self.reply(
                id,
                Err(RequestError::Stream(StreamError::new(code, message))),
            )),
            DialerCtl::Reset(r) => match self.cursor.on_reset(&r) {
                Ok(g) => Ok(Inbound::Invalidated(g)),
                Err(p) => Err(self.broke(p)),
            },
            DialerCtl::Position(p) => match self.cursor.on_position(&p) {
                Ok(()) => Ok(Inbound::Position),
                Err(p) => Err(self.broke(p)),
            },
            DialerCtl::Ignore => Ok(Inbound::Ignored),
        }
    }

    /// Resolves a pending request; an acked one updates the set.
    fn reply(&mut self, id: String, result: Result<u64, RequestError>) -> Inbound {
        let max = self.cfg.max_subscriptions;
        let Some(req) = self.link_mut().and_then(|l| l.pending.remove(&id)) else {
            return Inbound::Ignored;
        };
        if result.is_ok() {
            if let Ok(next) = self.subs.apply(&req, max) {
                self.subs = next;
            }
        }
        Inbound::Reply { id, result }
    }

    /// Decides on an event the transport decoded from an
    /// [`Inbound::Event`]: `offset` is
    /// [`EventFrame::offset_member`], `origin` and `topic` come from the
    /// decoded event. An unusable frame is dropped by the transport
    /// before this call, which wins over a bad offset.
    pub fn on_event(
        &mut self,
        offset: Option<&Value>,
        origin: &str,
        topic: &str,
    ) -> Result<Delivery, LinkFailure> {
        let own = self.cfg.origin.clone();
        let Some(l) = self.link_mut().filter(|l| !l.closing) else {
            return Ok(Delivery::Drop);
        };
        let stream = l.stream;
        let off = if stream {
            match super::parse_offset_token(offset) {
                Some(off) => Some(off),
                None => {
                    let p = ProtocolError::new(
                        super::ProtocolKind::Offset,
                        "event frame without a valid offset",
                    );
                    return Err(self.broke(p));
                }
            }
        } else {
            None
        };
        if l.peer_origin.is_empty() && !origin.is_empty() {
            l.peer_origin = origin.to_string();
        }
        if origin == own || is_reserved_topic(topic) {
            return Ok(Delivery::Drop);
        }
        let Some(off) = off else {
            return Ok(Delivery::Deliver(None));
        };
        if !self.cursor.advance(off) {
            return Ok(Delivery::Drop);
        }
        Ok(Delivery::Deliver(self.position()))
    }

    /// Counts a heartbeat interval. Returns the close to send once
    /// [`DEAD_AFTER_HEARTBEATS`] intervals passed with no frame of any
    /// kind; the link is then down with code `timeout`, not final.
    pub fn heartbeat_tick(&mut self) -> Option<CloseFrame> {
        let l = self.link_mut().filter(|l| l.stream)?;
        if l.frames != l.seen {
            l.seen = l.frames;
            l.misses = 0;
            return None;
        }
        l.misses += 1;
        if l.misses < DEAD_AFTER_HEARTBEATS {
            return None;
        }
        l.close_code = "timeout".into();
        Some(CloseFrame {
            status: CLOSE_GOING_AWAY,
            reason: "heartbeat timeout",
        })
    }

    /// The socket of a live link closed. Fails pending requests and
    /// reports whether to reconnect. `None` unless a link was up: a failed
    /// handshake was already reported by its [`HandshakeFailure`], so
    /// nothing is published twice.
    pub fn link_down(&mut self) -> Option<LinkDown> {
        let Phase::Up(l) = std::mem::replace(&mut self.phase, Phase::Down) else {
            return None;
        };
        let code = l.close_code;
        self.state.connected = false;
        if !code.is_empty() {
            self.state.code = code.clone();
        }
        let is_final = self.state.is_final;
        Some(LinkDown {
            failed: l.pending.into_keys().collect(),
            disconnected: Disconnected {
                peer: self.cfg.addr.clone(),
                is_final,
                code,
            },
            reconnect: !is_final,
        })
    }

    /// Whether a link is up.
    pub fn is_up(&self) -> bool {
        matches!(self.phase, Phase::Up(_))
    }

    /// The link negotiated streams.
    pub fn is_stream(&self) -> bool {
        matches!(&self.phase, Phase::Up(l) if l.stream)
    }

    // Acknowledged publishes.

    /// Registers an acknowledged publish appended to the send log at
    /// `offset` with event `id` (empty when none). Call it before the
    /// writer's next batch can send the entry. Its outcome arrives as an
    /// [`AckResolution`] for `offset`: in [`Inbound::Acked`],
    /// [`LinkUp::settled`](super::LinkUp::settled), a
    /// [`Batch`](super::Batch)'s `acks`, or [`StreamClient::fail_acks`].
    /// Deadlines are the transport's: on one, call
    /// [`StreamClient::forget_ack`].
    pub fn expect_ack(&mut self, offset: u64, id: &str) {
        self.acks.register(offset, id);
    }

    /// Drops the wait at `offset` (its deadline passed): the frame may
    /// still be acknowledged later, which settles nothing.
    pub fn forget_ack(&mut self, offset: u64) {
        self.acks.forget(offset);
    }

    /// Fails every wait with `code`: `unavailable` after
    /// [`StreamClient::disconnect`] or when the adapter closes.
    pub fn fail_acks(&mut self, code: &str) -> Vec<AckResolution> {
        self.acks.fail_all(code)
    }

    /// The last offset the peer acknowledged.
    pub fn acked(&self) -> u64 {
        self.acks.acked()
    }

    /// Whether the live link negotiated pubacks.
    pub fn is_acks(&self) -> bool {
        matches!(&self.phase, Phase::Up(l) if l.acks)
    }

    /// The resume request the next handshake sends.
    pub fn resume_request(&self) -> Option<ResumeReq> {
        self.cursor.request()
    }

    /// How the last link came up.
    pub fn last_resume(&self) -> Option<LinkResume> {
        self.state.resume
    }
}
