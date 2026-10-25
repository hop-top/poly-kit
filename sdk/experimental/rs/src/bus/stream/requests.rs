//! Subscription requests and the writer of a [`StreamClient`].

use super::client::{Phase, StreamClient};
use super::control::{encode_request, CtlRequest, RequestType};
use super::error::{StreamError, CODE_BAD_REQUEST};
use super::log::{Batch, StreamLog};
use super::types::{
    CloseFrame, DeliveryMarker, Request, RequestError, CLOSE_GOING_AWAY, MAX_WRITE_FAILURES,
};
use super::wire::{validate_pattern, Scope};
use crate::bus::Context;

impl StreamClient {
    // Requests.

    /// Adds patterns to what the acceptor sends; see
    /// [`StreamClient::request`].
    pub fn subscribe(
        &mut self,
        ctx: &Context,
        patterns: Vec<String>,
    ) -> Result<Request, RequestError> {
        self.request(ctx, RequestType::Subscribe, Some(patterns), None)
    }

    /// Removes patterns; see [`StreamClient::request`].
    pub fn unsubscribe(
        &mut self,
        ctx: &Context,
        patterns: Vec<String>,
    ) -> Result<Request, RequestError> {
        self.request(ctx, RequestType::Unsubscribe, Some(patterns), None)
    }

    /// Replaces the workspace scope; see [`StreamClient::request`].
    pub fn set_scope(&mut self, ctx: &Context, scope: Scope) -> Result<Request, RequestError> {
        self.request(ctx, RequestType::Subscribe, None, Some(scope))
    }

    /// Starts a subscription request. With `ctx` marked as a delivery of
    /// this address's link it fails with [`RequestError::Reentrant`]
    /// instead of waiting for a reply only the caller could read. On a
    /// link that is down it updates the set for the next handshake. After
    /// [`StreamClient::disconnect`] it fails with
    /// [`RequestError::Disconnected`].
    pub fn request(
        &mut self,
        ctx: &Context,
        typ: RequestType,
        patterns: Option<Vec<String>>,
        scope: Option<Scope>,
    ) -> Result<Request, RequestError> {
        if ctx
            .value::<DeliveryMarker>()
            .is_some_and(|m| !m.0.is_empty() && m.0 == self.cfg.addr)
        {
            return Err(RequestError::Reentrant(self.cfg.addr.clone()));
        }
        if patterns.is_some() || typ == RequestType::Unsubscribe {
            let ps = patterns.as_deref().unwrap_or_default();
            if ps.is_empty() {
                let msg = format!("{} needs patterns", typ.as_str());
                return Err(StreamError::new(CODE_BAD_REQUEST, msg).into());
            }
            ps.iter().try_for_each(|p| validate_pattern(p))?;
        }
        if self.forgotten {
            // Never registered again: a reconnect still pending would
            // redial it.
            return Err(RequestError::Disconnected(self.cfg.addr.clone()));
        }
        let mut r = CtlRequest {
            typ,
            id: String::new(),
            patterns,
            scope,
        };
        let max = self.cfg.max_subscriptions;
        let Phase::Up(l) = &mut self.phase else {
            self.subs = self.subs.apply(&r, max)?;
            return Ok(Request::Applied);
        };
        if !l.stream {
            return Err(RequestError::StreamsUnsupported);
        }
        l.next_id += 1;
        r.id = format!("r{}", l.next_id);
        let frame = encode_request(&r);
        let id = r.id.clone();
        l.pending.insert(id.clone(), r);
        Ok(Request::Send { id, frame })
    }

    /// Forgets request `id`: the caller gave up, or the frame was not
    /// written.
    pub fn cancel_request(&mut self, id: &str) {
        if let Some(l) = self.link_mut() {
            l.pending.remove(id);
        }
    }

    // Writer.

    /// The next batch of the send log for this address, or `None` once
    /// caught up; only while a link is up and not closing after a failed
    /// write. Counts outbox drops and skips.
    pub fn next_batch(&mut self, log: &dyn StreamLog) -> Option<Batch> {
        let Phase::Up(l) = &self.phase else {
            return None;
        };
        if l.closing {
            return None;
        }
        let b = self.writer.next_batch(log, &l.peer_origin)?;
        self.outbox_dropped += b.outbox_dropped;
        self.skipped += b.skipped;
        Some(b)
    }

    /// A frame was written: the consecutive-failure count resets.
    pub fn write_ok(&mut self) {
        if let Some(l) = self.link_mut() {
            l.write_failures = 0;
        }
    }

    /// The write of the entry at `offset` failed. A stream link, a link
    /// with the outbox on, or a legacy link at its third failure in a row
    /// closes: the returned close is to send, and the writer stops until
    /// [`StreamClient::link_down`]. With the outbox on, the next link
    /// sends that entry again (a possible duplicate, if its bytes reached
    /// the peer before the error). Otherwise the entry is lost.
    pub fn write_failed(&mut self, offset: u64) -> Option<CloseFrame> {
        let outbox = self.cfg.outbox;
        let l = self.link_mut()?;
        l.write_failures += 1;
        if !(l.stream || outbox || l.write_failures >= MAX_WRITE_FAILURES) {
            return None;
        }
        l.closing = true;
        self.writer.write_failed(offset);
        Some(CloseFrame {
            status: CLOSE_GOING_AWAY,
            reason: "write failures",
        })
    }
}
