//! Acknowledged publishes, dialer side: the `puback` frame, negotiation
//! and the per-address [`AckTracker`].
//!
//! An acceptor answers every event frame of an acks link with one
//! `puback`, in offset order, once its handlers are done. A puback is
//! cumulative: it reports its own offset and acknowledges `ok` every
//! earlier frame on the link no puback of its own reported.

use std::collections::BTreeSet;

use serde_json::{json, Map, Value};

use super::error::{ProtocolError, ProtocolKind};
use super::handshake::StreamCaps;
use super::wire::{format_offset, is_null, member_str, parse_offset_token, Members};

/// The acknowledged-publish protocol version this core speaks
/// (`caps.acks`).
pub const ACKS_VERSION: u64 = 1;

/// A peer's sync handler returned an error.
pub const ACK_REJECTED: &str = "rejected";
/// The peer could not use the frame.
pub const ACK_INVALID: &str = "invalid";
/// No acknowledgement before the deadline: delivery unknown, not failed.
pub const ACK_TIMEOUT: &str = "timeout";
/// The send log evicted the event before it was sent.
pub const ACK_EVICTED: &str = "evicted";
/// The peer saw the frame before but no longer holds its result:
/// delivery unknown, as with a timeout.
pub const ACK_UNKNOWN: &str = "unknown";
/// The address was disconnected or the adapter closed.
pub const ACK_UNAVAILABLE: &str = "unavailable";
/// The event was not sent to the peer (a filter, or the peer's own
/// origin).
pub const ACK_FILTERED: &str = "filtered";
/// The link did not negotiate acks.
pub const ACK_UNSUPPORTED: &str = "unsupported";

/// Reports whether a link carries pubacks: a stream link on which this
/// side speaks acks (`mine`) and the peer advertised them.
pub fn negotiate_acks(mine: u64, peer: Option<&StreamCaps>, stream_link: bool) -> bool {
    stream_link && mine >= ACKS_VERSION && peer.is_some_and(|p| p.acks >= ACKS_VERSION)
}

/// A puback frame. `status` is `ok` or `err`; `code` and `message` are
/// set only with `err`.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Puback {
    /// The frame's offset; every earlier unreported frame is `ok`.
    pub offset: u64,
    /// The frame's event id, empty when absent.
    pub id: String,
    /// `ok` or `err`.
    pub status: String,
    /// With `err`: the contract code.
    pub code: String,
    /// With `err`: detail, not contract.
    pub message: String,
}

impl Puback {
    /// The frame acknowledged the event.
    pub fn is_ok(&self) -> bool {
        self.status == "ok"
    }
}

fn malformed(msg: &str) -> ProtocolError {
    ProtocolError::new(ProtocolKind::PubackMalformed, msg)
}

/// Decodes a puback control frame. A malformed one breaks the link: a
/// lost acknowledgement cannot be told from a lost frame.
pub fn parse_puback(m: &Members) -> Result<Puback, ProtocolError> {
    let offset = parse_offset_token(m.get("offset"));
    let status = member_str(m, "status").filter(|s| *s == "ok" || *s == "err");
    let (Some(offset), Some(status)) = (offset, status) else {
        return Err(malformed("malformed puback"));
    };
    let mut p = Puback {
        offset,
        status: status.to_string(),
        ..Puback::default()
    };
    if !is_null(m.get("id")) {
        p.id = member_str(m, "id")
            .ok_or_else(|| malformed("puback id must be a string"))?
            .to_string();
    }
    if status == "err" {
        p.code = member_str(m, "code")
            .filter(|c| !c.is_empty())
            .ok_or_else(|| malformed("puback err without a code"))?
            .to_string();
        p.message = member_str(m, "message").unwrap_or("").to_string();
    }
    Ok(p)
}

/// Encodes a puback frame.
pub fn encode_puback(p: &Puback) -> Vec<u8> {
    let mut f = Map::new();
    f.insert("type".into(), json!("puback"));
    f.insert("offset".into(), json!(format_offset(p.offset)));
    f.insert("status".into(), json!(p.status));
    if !p.id.is_empty() {
        f.insert("id".into(), json!(p.id));
    }
    if p.status == "err" {
        f.insert("code".into(), json!(p.code));
        if !p.message.is_empty() {
            f.insert("message".into(), json!(p.message));
        }
    }
    serde_json::to_vec(&Value::Object(f)).expect("pubacks always encode")
}

/// The outcome of one acknowledged publish, by its offset in the send
/// log. An empty `code` means acknowledged.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct AckResolution {
    /// The publish's offset.
    pub offset: u64,
    /// Empty: acknowledged; otherwise an `ACK_*` code or the peer's.
    pub code: String,
    /// Detail from the peer; not contract.
    pub message: String,
}

impl AckResolution {
    fn new(offset: u64, code: &str) -> Self {
        AckResolution {
            offset,
            code: code.to_string(),
            message: String::new(),
        }
    }
}

/// A dialer's acknowledgement state for one outbound address, kept across
/// reconnects: the cumulative acknowledged offset, the highest offset
/// sent on the current link, and the offsets of acknowledged publishes
/// still waiting. Pure; the transport maps offsets to its waiters and
/// applies deadlines itself.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct AckTracker {
    acked: u64,
    sent: u64,
    waits: BTreeSet<u64>,
    ids: std::collections::BTreeMap<u64, String>,
}

impl AckTracker {
    /// Starts at the send cursor's offset.
    pub fn new(start: u64) -> Self {
        AckTracker {
            acked: start,
            sent: start,
            ..AckTracker::default()
        }
    }

    /// The last acknowledged offset: the peer handled every frame up to
    /// it.
    pub fn acked(&self) -> u64 {
        self.acked
    }

    /// Offsets still waiting, ascending.
    pub fn waiting(&self) -> impl Iterator<Item = u64> + '_ {
        self.waits.iter().copied()
    }

    /// Records an acknowledged publish logged at `offset` with event `id`
    /// (empty when none). Register it before the writer can send it.
    pub fn register(&mut self, offset: u64, id: &str) {
        self.waits.insert(offset);
        if !id.is_empty() {
            self.ids.insert(offset, id.to_string());
        }
    }

    /// A link came up. On an acks link returns the offset to rewind the
    /// send cursor to (the last acknowledged one, when below `cursor_at`),
    /// so frames lost with the previous link go out again. On any other
    /// link every wait fails `unsupported` and the acknowledged offset
    /// follows the cursor.
    pub fn link_up(&mut self, acks: bool, cursor_at: u64) -> (u64, Vec<AckResolution>) {
        if acks {
            let at = cursor_at.min(self.acked);
            self.sent = at;
            return (at, Vec::new());
        }
        self.acked = cursor_at;
        self.sent = cursor_at;
        (cursor_at, self.drain(|_| true, ACK_UNSUPPORTED))
    }

    /// The frame at `offset` is being written.
    pub fn on_send(&mut self, offset: u64, acks: bool) -> Option<AckResolution> {
        if !acks {
            self.acked = offset;
            self.sent = offset;
            return self
                .take(offset)
                .then(|| AckResolution::new(offset, ACK_UNSUPPORTED));
        }
        self.sent = self.sent.max(offset);
        None
    }

    /// The writer did not send the entry at `offset`.
    pub fn on_skip(&mut self, offset: u64) -> Option<AckResolution> {
        self.take(offset)
            .then(|| AckResolution::new(offset, ACK_FILTERED))
    }

    /// Entries after `before` and below `first` were evicted before the
    /// writer reached them.
    pub fn on_gap(&mut self, before: u64, first: u64) -> Vec<AckResolution> {
        self.drain(|off| off > before && off < first, ACK_EVICTED)
    }

    /// Applies a cumulative puback.
    pub fn on_puback(&mut self, p: &Puback) -> Result<Vec<AckResolution>, ProtocolError> {
        if p.offset > self.sent {
            return Err(ProtocolError::new(
                ProtocolKind::PubackAhead,
                format!("puback for {} beyond last sent {}", p.offset, self.sent),
            ));
        }
        if let Some(id) = self.ids.get(&p.offset) {
            if !p.id.is_empty() && p.id != *id {
                return Err(ProtocolError::new(
                    ProtocolKind::PubackId,
                    format!("puback id {:?} for {}, sent {id:?}", p.id, p.offset),
                ));
            }
        }
        if p.offset <= self.acked {
            return Ok(Vec::new());
        }
        self.acked = p.offset;
        let done: Vec<u64> = self.waits.range(..=p.offset).copied().collect();
        Ok(done
            .into_iter()
            .map(|off| {
                self.waits.remove(&off);
                self.ids.remove(&off);
                let mut r = AckResolution::new(off, "");
                if off == p.offset && !p.is_ok() {
                    r.code = p.code.clone();
                    r.message = p.message.clone();
                }
                r
            })
            .collect())
    }

    /// Fails every wait with `code` (`unavailable` on disconnect).
    pub fn fail_all(&mut self, code: &str) -> Vec<AckResolution> {
        self.drain(|_| true, code)
    }

    /// Drops the wait at `offset` (the transport gave up, a timeout),
    /// without changing the acknowledged offset.
    pub fn forget(&mut self, offset: u64) {
        self.take(offset);
    }

    fn take(&mut self, offset: u64) -> bool {
        self.ids.remove(&offset);
        self.waits.remove(&offset)
    }

    fn drain(&mut self, keep: impl Fn(u64) -> bool, code: &str) -> Vec<AckResolution> {
        let offs: Vec<u64> = self.waits.iter().copied().filter(|o| keep(*o)).collect();
        offs.into_iter()
            .map(|off| {
                self.take(off);
                AckResolution::new(off, code)
            })
            .collect()
    }
}
