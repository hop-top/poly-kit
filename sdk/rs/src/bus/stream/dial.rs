//! The dial lifecycle of a [`StreamClient`]: handshake ids, the ack,
//! failures and their repeat suppression, and disconnect.

use std::sync::atomic::{AtomicU64, Ordering};

use super::ack::{parse_ack, Ack};
use super::client::{Phase, StreamClient};
use super::handshake::{encode_auth_frame, ResumeReq, SessionReq};
use super::log::StreamLog;
use super::types::{
    CloseFrame, HandshakeError, HandshakeFailure, HandshakeId, LinkUp, CLOSE_NORMAL, CLOSE_POLICY,
    CLOSE_PROTOCOL,
};

/// Handshake ids are unique across every client in the process, so a
/// late ack from a replaced client's socket never matches its successor's
/// dial. Starts at 1; a counter, no clock and no randomness.
static NEXT_DIAL: AtomicU64 = AtomicU64::new(1);

impl StreamClient {
    /// Forgets the address, as Go's `Disconnect`: it is final, and from
    /// now on requests return
    /// [`RequestError::Disconnected`](super::RequestError::Disconnected)
    /// and no handshake brings it back (a reconnect timer already armed
    /// included). A live link stops at once: nothing more is written or
    /// delivered. Returns the close to send when a link or a handshake is
    /// in progress; the link's [`StreamClient::link_down`] then reports a
    /// final disconnect.
    ///
    /// To connect to the address again later (Go's next explicit
    /// `Connect`), build a new `StreamClient`. Its handshake ids never
    /// match this client's, so a late ack from this client's socket is
    /// refused as stale.
    pub fn disconnect(&mut self) -> Option<CloseFrame> {
        self.forgotten = true;
        self.state.is_final = true;
        if let Phase::Up(l) = &mut self.phase {
            l.closing = true;
        }
        (!matches!(self.phase, Phase::Down)).then_some(CloseFrame {
            status: CLOSE_NORMAL,
            reason: "disconnect",
        })
    }

    /// The address was forgotten by [`StreamClient::disconnect`].
    pub fn is_disconnected(&self) -> bool {
        self.forgotten
    }

    // Handshake.

    /// Opens a dial: a fresh [`HandshakeId`] for the attempt. Refused
    /// while a handshake or a link is in progress (the transport
    /// serialises dials of one address) and after
    /// [`StreamClient::disconnect`].
    fn open_dial(&mut self, sent: Option<ResumeReq>) -> Result<HandshakeId, HandshakeError> {
        if self.forgotten {
            return Err(HandshakeError::Disconnected(self.cfg.addr.clone()));
        }
        if !matches!(self.phase, Phase::Down) {
            return Err(HandshakeError::Busy);
        }
        let id = HandshakeId(NEXT_DIAL.fetch_add(1, Ordering::Relaxed));
        self.phase = Phase::Handshaking { id, sent };
        Ok(id)
    }

    /// Starts a handshake: returns its id and the auth frame (caps,
    /// origin and the session: subscription, scope and resume request).
    /// Pass the id to [`StreamClient::on_ack`] with the reply on that
    /// socket.
    pub fn begin_handshake(
        &mut self,
        token: &str,
    ) -> Result<(HandshakeId, Vec<u8>), HandshakeError> {
        let sess = SessionReq {
            subscribe: (!self.subs.implicit).then(|| self.subs.sorted_patterns()),
            scope: self.subs.scope.clone(),
            resume: self.cursor.request(),
        };
        let id = self.open_dial(sess.resume.clone())?;
        let frame = encode_auth_frame(
            token,
            Some(&self.cfg.caps),
            &self.cfg.origin,
            &self.cfg.stream,
            Some(&sess),
        );
        Ok((id, frame))
    }

    /// Opens a dial without a handshake (no authenticator); bring the
    /// link up with [`StreamClient::link_up_legacy`].
    pub fn begin_legacy(&mut self) -> Result<HandshakeId, HandshakeError> {
        self.open_dial(None)
    }

    /// Takes the handshake `id` out of the phase, or explains why the
    /// reply must not be applied: a stale or late reply (another
    /// handshake, or one already failed) changes nothing and publishes
    /// nothing; the transport just closes that socket.
    fn take_dial(&mut self, id: HandshakeId) -> Result<Option<ResumeReq>, HandshakeFailure> {
        let ignored = |error| HandshakeFailure {
            error,
            close: Some(CloseFrame {
                status: CLOSE_NORMAL,
                reason: "duplicate",
            }),
            publish: false,
        };
        match &self.phase {
            Phase::Handshaking { id: cur, .. } if *cur == id => {}
            _ => return Err(ignored(HandshakeError::Stale)),
        }
        let Phase::Handshaking { sent, .. } = std::mem::replace(&mut self.phase, Phase::Down)
        else {
            unreachable!("checked above");
        };
        if self.forgotten {
            return Err(HandshakeFailure {
                close: Some(CloseFrame {
                    status: CLOSE_NORMAL,
                    reason: "disconnect",
                }),
                ..ignored(HandshakeError::Disconnected(self.cfg.addr.clone()))
            });
        }
        Ok(sent)
    }

    /// Handles the acceptor's reply to the auth frame of handshake `id`.
    pub fn on_ack(
        &mut self,
        id: HandshakeId,
        data: &[u8],
        log: &dyn StreamLog,
    ) -> Result<LinkUp, HandshakeFailure> {
        let sent = self.take_dial(id)?;
        let close = |status, reason| Some(CloseFrame { status, reason });
        match parse_ack(data, self.cfg.caps.streams) {
            Ok(Ack::AuthOk(a)) => self.link_up(Some(a), sent, log),
            Ok(Ack::SessionError(e)) => Err(self.fail(
                HandshakeError::Session(e),
                close(CLOSE_POLICY, "invalid session"),
            )),
            Err(super::AckError::Rejected) => Err(self.fail(
                HandshakeError::AuthRejected,
                close(CLOSE_POLICY, "auth rejected"),
            )),
            Err(super::AckError::Protocol(p)) => Err(self.fail(
                HandshakeError::Protocol(p),
                close(CLOSE_PROTOCOL, "protocol error"),
            )),
        }
    }

    /// Brings up the legacy link of dial `id` (see
    /// [`StreamClient::begin_legacy`]).
    pub fn link_up_legacy(
        &mut self,
        id: HandshakeId,
        log: &dyn StreamLog,
    ) -> Result<LinkUp, HandshakeFailure> {
        self.take_dial(id)?;
        self.link_up(None, None, log)
    }

    /// Records that handshake `id` failed in the transport: no ack before
    /// its timeout ([`HandshakeError::AckTimeout`]) or a 1008 close
    /// ([`HandshakeError::AuthRejected`]). A stale id changes nothing.
    pub fn handshake_failed(&mut self, id: HandshakeId, error: HandshakeError) -> HandshakeFailure {
        if let Err(f) = self.take_dial(id) {
            return f;
        }
        self.fail(error, None)
    }

    /// Session errors and protocol errors are final; a refused token or
    /// a missing ack is not. A repeat of the same retrying failure is not
    /// published again.
    pub(super) fn fail(
        &mut self,
        error: HandshakeError,
        close: Option<CloseFrame>,
    ) -> HandshakeFailure {
        let (code, is_final) = (error.code().to_string(), error.is_final());
        let repeat =
            !is_final && !self.state.connected && self.state.code == code && !self.state.is_final;
        self.state.connected = false;
        self.state.code = code;
        self.state.is_final = is_final;
        HandshakeFailure {
            error,
            close,
            publish: !repeat,
        }
    }
}
