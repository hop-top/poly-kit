//! Positions: the acceptor's resume decision, the dialer's cursor and its
//! rules from link-up through event, reset and position frames, and the
//! bounded inbound queue for dialers without backpressure.

use std::collections::VecDeque;

use serde_json::{json, Value};

use super::ack::{AuthOk, ResumeOutcome, ResumeStatus};
use super::control::{PositionInfo, ResetInfo};
use super::error::{ProtocolError, ProtocolKind};
use super::handshake::ResumeReq;
use super::wire::{format_offset, parse_offset_token};

/// Reset reason: the dialer's position names another stream.
pub const REASON_STREAM: &str = "stream";
/// Reset reason: the position is beyond the acceptor's head.
pub const REASON_AHEAD: &str = "ahead";
/// Reset reason: the log evicted entries after the position.
pub const REASON_EVICTED: &str = "evicted";
/// Reset reason: a slow link fell behind the log.
pub const REASON_LAGGED: &str = "lagged";
/// Reset reason: the acceptor lost events from its own upstream.
pub const REASON_UPSTREAM: &str = "upstream";
/// Invalidation reason: the peer cannot resume.
pub const REASON_UNSUPPORTED: &str = "unsupported";

/// The resume status a link reports in `kit.runtime.link.connected`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum LinkResume {
    /// A stream link that resumed nothing.
    None,
    /// Replaying from the held position.
    Replaying,
    /// Started at the head; an invalidation was published.
    Reset,
    /// A legacy link: no positions.
    Unsupported,
}

impl LinkResume {
    /// The status in the event payload.
    pub fn as_str(self) -> &'static str {
        match self {
            LinkResume::None => "none",
            LinkResume::Replaying => "replaying",
            LinkResume::Reset => "reset",
            LinkResume::Unsupported => "unsupported",
        }
    }
}

/// The acceptor's resume decision for a log (`id`, `oldest`, `head`) and
/// an optional request. Returns the ack's outcome (`None` when no resume
/// was asked for) and the link's starting cursor.
pub fn decide_resume(
    id: &str,
    oldest: u64,
    head: u64,
    r: Option<&ResumeReq>,
) -> (Option<ResumeOutcome>, u64) {
    let Some(r) = r else {
        return (None, head);
    };
    let reset = |reason: &str| {
        let o = ResumeOutcome {
            status: ResumeStatus::Reset,
            reason: reason.to_string(),
            after: r.after,
            head,
        };
        (Some(o), head)
    };
    if r.stream != id {
        return reset(REASON_STREAM);
    }
    if r.after > head {
        return reset(REASON_AHEAD);
    }
    // oldest >= 1, so no underflow.
    if r.after < oldest.saturating_sub(1) {
        return reset(REASON_EVICTED);
    }
    let o = ResumeOutcome {
        status: ResumeStatus::Replaying,
        reason: String::new(),
        after: r.after,
        head,
    };
    (Some(o), r.after)
}

/// One invalidation the dialer publishes on
/// `kit.runtime.stream.invalidated`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct GapInfo {
    /// One of the `REASON_*` constants, or a peer's reason.
    pub reason: String,
    /// The stream the gap is in; empty for `unsupported`.
    pub stream: String,
    /// The position before the gap.
    pub after: u64,
    /// The position after it.
    pub head: u64,
}

impl GapInfo {
    fn unsupported() -> Self {
        GapInfo {
            reason: REASON_UNSUPPORTED.to_string(),
            stream: String::new(),
            after: 0,
            head: 0,
        }
    }

    /// The event payload for `peer`. Offsets are decimal strings; stream
    /// and offsets are empty for `unsupported`.
    pub fn payload(&self, peer: &str) -> Value {
        let unsupported = self.reason == REASON_UNSUPPORTED;
        let text = |s: String| if unsupported { String::new() } else { s };
        json!({
            "peer": peer,
            "reason": self.reason,
            "stream": text(self.stream.clone()),
            "after": text(format_offset(self.after)),
            "head": text(format_offset(self.head)),
        })
    }
}

/// A dialer's position for one outbound address. It survives reconnects.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct DialerCursor {
    /// The acceptor's stream id.
    pub stream: String,
    /// The last offset handled.
    pub offset: u64,
    /// A position is held.
    pub held: bool,
}

/// Validates an ack's resume outcome against the request the dialer sent
/// (`None` when none) and the ack's stream info.
pub fn check_ack(a: Option<&AuthOk>, req: Option<&ResumeReq>) -> Result<(), ProtocolError> {
    let Some((ro, head)) = a.and_then(|a| Some((a.resume.as_ref()?, a.stream.as_ref()?.head)))
    else {
        return Ok(());
    };
    let Some(req) = req else {
        return Err(ProtocolError::new(
            ProtocolKind::Unrequested,
            "ack: resume outcome without a request",
        ));
    };
    match ro.status {
        ResumeStatus::Replaying if ro.after != req.after || ro.after > head => {
            Err(ProtocolError::new(
                ProtocolKind::ReplayAfter,
                format!(
                    "ack: replay after {}, asked {}, head {head}",
                    ro.after, req.after
                ),
            ))
        }
        ResumeStatus::Reset if ro.head > head => Err(ProtocolError::new(
            ProtocolKind::ResetHead,
            format!("ack: reset head {} beyond stream head {head}", ro.head),
        )),
        _ => Ok(()),
    }
}

impl DialerCursor {
    /// The resume request to send, `None` when no position is held.
    pub fn request(&self) -> Option<ResumeReq> {
        self.held.then(|| ResumeReq {
            stream: self.stream.clone(),
            after: self.offset,
        })
    }

    /// Applies the acceptor's ack (`None`, or one without stream info,
    /// for a legacy link), answering `sent`, the resume request the
    /// dialer sent. Returns the resume status for link.connected and the
    /// invalidation to publish before anything else, if any. An ack that
    /// fails [`check_ack`] is a protocol error and changes nothing.
    pub fn link_up(
        &mut self,
        a: Option<&AuthOk>,
        sent: Option<&ResumeReq>,
        reconnect: bool,
    ) -> Result<(LinkResume, Option<GapInfo>), ProtocolError> {
        check_ack(a, sent)?;
        let wanted = self.held || reconnect;
        let Some((s, a)) = a.and_then(|a| Some((a.stream.as_ref()?, a))) else {
            self.held = false;
            let gap = wanted.then(GapInfo::unsupported);
            return Ok((LinkResume::Unsupported, gap));
        };
        self.stream = s.id.clone();
        self.held = true;
        let Some(ro) = &a.resume else {
            self.offset = s.head;
            return Ok((LinkResume::None, wanted.then(GapInfo::unsupported)));
        };
        if ro.status == ResumeStatus::Reset {
            self.offset = ro.head;
            let g = GapInfo {
                reason: ro.reason.clone(),
                stream: s.id.clone(),
                after: ro.after,
                head: ro.head,
            };
            return Ok((LinkResume::Reset, Some(g)));
        }
        if let Some(sent) = sent.filter(|r| r.stream != s.id) {
            // A replay of a stream we did not ask for: treat it as a reset.
            self.offset = s.head;
            let g = GapInfo {
                reason: REASON_STREAM.to_string(),
                stream: s.id.clone(),
                after: sent.after,
                head: s.head,
            };
            return Ok((LinkResume::Reset, Some(g)));
        }
        self.offset = ro.after;
        Ok((LinkResume::Replaying, None))
    }

    /// Applies an event frame's raw offset member. Returns whether to
    /// deliver the event (`false`: a duplicate), or a protocol error.
    pub fn on_event_offset(&mut self, raw: Option<&Value>) -> Result<bool, ProtocolError> {
        match parse_offset_token(raw) {
            Some(off) => Ok(self.advance(off)),
            None => Err(ProtocolError::new(
                ProtocolKind::Offset,
                "event frame without a valid offset",
            )),
        }
    }

    /// Moves the position to `off` when it is ahead; `false` for a
    /// duplicate.
    pub fn advance(&mut self, off: u64) -> bool {
        if off <= self.offset {
            return false;
        }
        self.offset = off;
        true
    }

    /// Applies a reset frame: the position moves to its head.
    pub fn on_reset(&mut self, r: &ResetInfo) -> Result<GapInfo, ProtocolError> {
        if r.stream != self.stream {
            return Err(foreign("reset", &r.stream, &self.stream));
        }
        if r.after > r.head {
            return Err(ProtocolError::new(
                ProtocolKind::ResetAfter,
                format!("reset after {} beyond head {}", r.after, r.head),
            ));
        }
        if r.head < self.offset {
            // Offsets only grow: a reset to below the position would make
            // the dialer re-deliver or drop events.
            return Err(ProtocolError::new(
                ProtocolKind::ResetHead,
                format!("reset head {} below position {}", r.head, self.offset),
            ));
        }
        self.offset = r.head;
        Ok(GapInfo {
            reason: r.reason.clone(),
            stream: r.stream.clone(),
            after: r.after,
            head: r.head,
        })
    }

    /// Applies a position frame: the max of the two offsets.
    pub fn on_position(&mut self, p: &PositionInfo) -> Result<(), ProtocolError> {
        if p.stream != self.stream {
            return Err(foreign("position", &p.stream, &self.stream));
        }
        self.offset = self.offset.max(p.offset);
        Ok(())
    }
}

fn foreign(what: &str, got: &str, mine: &str) -> ProtocolError {
    use crate::bus::quote::quote;
    ProtocolError::new(
        ProtocolKind::ForeignStream,
        format!(
            "{what} for stream {} on a link to {}",
            quote(got),
            quote(mine)
        ),
    )
}

/// The bounded inbound queue of a dialer that cannot apply backpressure
/// (a browser): frames queue up to capacity, and receiving into a full
/// queue overflows. On overflow the queue is discarded, the link must
/// close, and the reconnect resumes after [`InboundQueue::resume_after`].
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct InboundQueue {
    capacity: usize,
    q: VecDeque<u64>,
    delivered: u64,
    overflowed: bool,
}

impl InboundQueue {
    /// A queue of `capacity` starting after position `after`.
    pub fn new(capacity: usize, after: u64) -> Self {
        InboundQueue {
            capacity,
            q: VecDeque::new(),
            delivered: after,
            overflowed: false,
        }
    }

    /// Receives the frame at `off`; overflows when full.
    pub fn recv(&mut self, off: u64) {
        if self.overflowed {
            return;
        }
        if self.q.len() >= self.capacity {
            self.overflowed = true;
            self.q.clear();
            return;
        }
        self.q.push_back(off);
    }

    /// Hands the oldest `n` to the application and advances the position.
    pub fn deliver(&mut self, n: usize) -> Vec<u64> {
        if self.overflowed {
            return Vec::new();
        }
        let n = n.min(self.q.len());
        let out: Vec<u64> = self.q.drain(..n).collect();
        if let Some(last) = out.last() {
            self.delivered = *last;
        }
        out
    }

    /// The queue overflowed; the link must close.
    pub fn overflowed(&self) -> bool {
        self.overflowed
    }

    /// The position the reconnect resumes from.
    pub fn resume_after(&self) -> u64 {
        self.delivered
    }
}
