//! The send log, send cursors and the outbox.
//!
//! An adapter appends every event it forwards to one log. Each link has a
//! [`SendCursor`]; kept across disconnects it is the outbox. The
//! [`OutboundWriter`] is the dialer's writer over it: what to send next,
//! what was dropped, and the rewind after a failed write.

use crate::bus::Event;

use super::subs::{entry_delivers, SubState};

/// The ring size used when no log size is configured.
pub const DEFAULT_STREAM_LOG_SIZE: usize = 1024;

/// Entries the writer reads per batch.
pub const WRITER_BATCH: usize = 256;

/// One record of an adapter's stream: an event with its wire origin, or a
/// gap marker that tells downstream peers the adapter lost events from
/// upstream. `offset` is assigned by [`StreamLog::append`].
#[derive(Clone, Debug, PartialEq)]
pub struct LogEntry {
    /// Dense, from 1.
    pub offset: u64,
    /// The event; unused on a marker.
    pub event: Event,
    /// The origin the event arrived with, empty for local events.
    pub origin: String,
    /// A gap marker rather than an event.
    pub marker: bool,
}

/// The recent part of an adapter's stream. Offsets are dense (1, 2, 3,
/// ...), so a reader whose first entry is above `after + 1` knows the
/// entries in between were evicted.
pub trait StreamLog {
    /// The stream id: an opaque ASCII string of 1-128 bytes. A log that
    /// loses its contents must also change its id.
    fn id(&self) -> &str;
    /// Assigns the next offset to `e`, stores it and returns the offset.
    fn append(&mut self, e: LogEntry) -> u64;
    /// Returns up to `max` entries with offset > `after`, ascending.
    fn read_after(&self, after: u64, max: usize) -> Vec<LogEntry>;
    /// The first retained offset, `head() + 1` when empty.
    fn oldest(&self) -> u64;
    /// The last assigned offset, 0 before the first append.
    fn head(&self) -> u64;
}

/// An in-memory [`StreamLog`] holding the last `size` entries.
#[derive(Clone, Debug)]
pub struct RingLog {
    id: String,
    buf: Vec<Option<LogEntry>>,
    head: u64,
}

impl RingLog {
    /// Creates a ring of `size` entries (below 1 means
    /// [`DEFAULT_STREAM_LOG_SIZE`]) for stream `id`.
    ///
    /// Go mints a fresh `stm_` TypeID for an empty id; this core does no
    /// randomness, so the caller supplies the id (see
    /// [`valid_stream_id`](super::valid_stream_id)).
    pub fn new(id: impl Into<String>, size: usize) -> Self {
        let size = if size < 1 {
            DEFAULT_STREAM_LOG_SIZE
        } else {
            size
        };
        RingLog {
            id: id.into(),
            buf: vec![None; size],
            head: 0,
        }
    }

    fn slot(&self, off: u64) -> usize {
        ((off - 1) % self.buf.len() as u64) as usize
    }
}

impl StreamLog for RingLog {
    fn id(&self) -> &str {
        &self.id
    }

    fn append(&mut self, mut e: LogEntry) -> u64 {
        self.head += 1;
        e.offset = self.head;
        let i = self.slot(self.head);
        self.buf[i] = Some(e);
        self.head
    }

    fn read_after(&self, after: u64, max: usize) -> Vec<LogEntry> {
        let start = after.saturating_add(1).max(self.oldest());
        if start > self.head || max == 0 {
            return Vec::new();
        }
        let n = (self.head - start + 1).min(max as u64);
        (start..start + n)
            .filter_map(|off| self.buf[self.slot(off)].clone())
            .collect()
    }

    fn oldest(&self) -> u64 {
        let n = self.buf.len() as u64;
        if self.head <= n {
            1
        } else {
            self.head - n + 1
        }
    }

    fn head(&self) -> u64 {
        self.head
    }
}

/// How far one destination has read a log: the writer's cursor for a
/// link and, kept across disconnects, the outbox.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct SendCursor {
    /// The last offset handled.
    pub at: u64,
    /// Keep entries appended while the destination is down.
    pub outbox: bool,
}

impl SendCursor {
    /// Starts a cursor at the log's head: with the outbox on, everything
    /// appended from now on is kept for the destination.
    pub fn new(log: &dyn StreamLog, outbox: bool) -> Self {
        SendCursor {
            at: log.head(),
            outbox,
        }
    }

    /// Prepares the cursor for a new link: without the outbox, entries
    /// appended while the destination was down are skipped.
    pub fn link_up(&mut self, log: &dyn StreamLog) {
        if !self.outbox {
            self.at = log.head();
        }
    }

    /// Returns up to `max` entries after the cursor and advances past
    /// them. The gap counts entries evicted before the cursor reached
    /// them.
    pub fn next(&mut self, log: &dyn StreamLog, max: usize) -> (Vec<LogEntry>, u64) {
        let entries = log.read_after(self.at, max);
        let Some(last) = entries.last() else {
            return (entries, 0);
        };
        let first = entries[0].offset;
        let gap = first.saturating_sub(self.at.saturating_add(1));
        self.at = last.offset;
        (entries, gap)
    }
}

/// One step of an [`OutboundWriter`].
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Batch {
    /// Entries to encode and send, in order. Filtered entries and gap
    /// markers are already removed.
    pub send: Vec<LogEntry>,
    /// Entries the outbox lost to eviction while the address was down
    /// (counted on the first batch after link up).
    pub outbox_dropped: u64,
    /// Entries evicted before a connected writer reached them.
    pub skipped: u64,
}

/// A dialer's writer over the send log for one outbound address. It
/// survives reconnects when the outbox is on.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OutboundWriter {
    cursor: SendCursor,
    flushing: bool,
}

impl OutboundWriter {
    /// Registers the address: the cursor starts at the head, so with the
    /// outbox on, events published before the first connect are kept.
    pub fn new(log: &dyn StreamLog, outbox: bool) -> Self {
        OutboundWriter {
            cursor: SendCursor::new(log, outbox),
            flushing: true,
        }
    }

    /// The cursor.
    pub fn cursor(&self) -> SendCursor {
        self.cursor
    }

    /// A link came up: the next batch is the outbox flush.
    pub fn link_up(&mut self, log: &dyn StreamLog) {
        self.cursor.link_up(log);
        self.flushing = true;
    }

    /// Reads the next batch, or `None` once caught up. Call it until it
    /// returns `None`; a batch may have nothing to send. Entries from the
    /// peer's own origin and gap markers are not sent on outbound links.
    pub fn next_batch(&mut self, log: &dyn StreamLog, peer_origin: &str) -> Option<Batch> {
        let (entries, gap) = self.cursor.next(log, WRITER_BATCH);
        let flush = std::mem::replace(&mut self.flushing, false);
        if entries.is_empty() {
            return None;
        }
        let mut b = Batch::default();
        if gap > 0 {
            if flush && self.cursor.outbox {
                b.outbox_dropped = gap;
            } else {
                b.skipped = gap;
            }
        }
        let all = SubState::new();
        b.send = entries
            .into_iter()
            .filter(|e| !e.marker && entry_delivers(&all, peer_origin, e))
            .collect();
        Some(b)
    }

    /// Records a failed write of the entry at `offset`. The link must
    /// close. With the outbox on the cursor rewinds, so the next link
    /// sends that entry again (a possible duplicate if the bytes reached
    /// the peer before the error); without it the entry is lost.
    pub fn write_failed(&mut self, offset: u64) {
        if self.cursor.outbox {
            self.cursor.at = offset.saturating_sub(1);
        }
    }
}
