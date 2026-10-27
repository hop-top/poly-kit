//! Confirmed delivery of the audit log onto the bus: an outbox relay
//! with an ack cursor.

use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Condvar, Mutex};
use std::time::Duration;

use serde::{Deserialize, Serialize};

use super::audit::{AuditEvent, Delivery, Publisher, Topics, EVENT_SOURCE};
use super::file_audit::FileAuditor;
use super::paths::{write_file_atomic, CURSOR_FILE};
use crate::bus::Event;

/// The persisted ack position (`audit.cursor`).
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
struct Cursor {
    #[serde(default)]
    log: String,
    #[serde(default)]
    seq: u64,
}

/// What one [`Relay::flush`] did.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FlushResult {
    /// Sequence numbers acked by this flush.
    pub delivered: Vec<u64>,
    /// The last acked sequence number.
    pub cursor: u64,
    /// Records still waiting for an ack.
    pub pending: usize,
}

/// A flush that stopped early: the progress it made, and why.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct FlushError {
    pub progress: FlushResult,
    pub message: String,
}

impl std::fmt::Display for FlushError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for FlushError {}

fn stop(progress: FlushResult, message: String) -> FlushError {
    FlushError { progress, message }
}

#[derive(Default)]
struct Wake {
    notified: bool,
    stopped: bool,
}

/// Delivers a [`FileAuditor`] log onto a bus with confirmation.
///
/// Records after the ack cursor are published in `seq` order: topic
/// `kit.core.consent.<event>`, source `core.consent`, id `<log>:<seq>`
/// (the idempotency key), subject the capability, payload the record.
/// A record is acked when `publish` returns `Ok` (every sync subscriber
/// on an in-process bus succeeded); the cursor next to the log is then
/// written atomically. A failure stops the flush there; the record is
/// sent again with the same id on the next flush, also after a restart.
/// A cursor naming another log is ignored; a corrupt one fails the
/// flush.
pub struct Relay {
    log: Arc<FileAuditor>,
    publisher: Arc<dyn Publisher>,
    topics: Topics,
    cursor: PathBuf,
    retry: Duration,
    flush_lock: Mutex<()>,
    wake: Mutex<Wake>,
    cv: Condvar,
}

impl Relay {
    /// Relays `log` onto `publisher`; the cursor is `audit.cursor` next
    /// to the log.
    pub fn new(log: Arc<FileAuditor>, publisher: Arc<dyn Publisher>) -> Self {
        let dir = log.path().parent().unwrap_or(Path::new(".")).to_path_buf();
        Relay {
            log,
            publisher,
            topics: Topics::default(),
            cursor: dir.join(CURSOR_FILE),
            retry: Duration::from_secs(5),
            flush_lock: Mutex::new(()),
            wake: Mutex::new(Wake::default()),
            cv: Condvar::new(),
        }
    }

    /// Overrides topics; empty fields keep the default.
    pub fn with_topics(mut self, t: Topics) -> Self {
        self.topics = t.or_default();
        self
    }

    /// How long [`Relay::run`] waits before retrying a failed flush.
    /// Default 5s.
    pub fn with_retry_interval(mut self, d: Duration) -> Self {
        if !d.is_zero() {
            self.retry = d;
        }
        self
    }

    pub fn cursor_path(&self) -> &Path {
        &self.cursor
    }

    fn read_cursor(&self) -> Result<Cursor, String> {
        match fs::read_to_string(&self.cursor) {
            Ok(s) => serde_json::from_str::<Option<Cursor>>(&s)
                .map(Option::unwrap_or_default)
                .map_err(|e| format!("consent: cursor {} is corrupt: {e}", self.cursor.display())),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(Cursor::default()),
            Err(e) => Err(format!("consent: read cursor: {e}")),
        }
    }

    fn write_cursor(&self, c: &Cursor) -> Result<(), String> {
        let mut b = serde_json::to_vec(c).map_err(|e| e.to_string())?;
        b.push(b'\n');
        write_file_atomic(&self.cursor, &b).map_err(|e| e.to_string())
    }

    fn event(&self, e: &AuditEvent) -> Event {
        let mut ev = Event::new(self.topics.topic(e.event), EVENT_SOURCE, e.to_value())
            .with_subject(e.capability.clone());
        if let Some(k) = e.key() {
            ev.id = k;
        }
        ev
    }

    /// Publishes every record after the cursor, in order, and stops at
    /// the first failure, which it returns with the progress so far.
    pub fn flush(&self) -> Result<FlushResult, FlushError> {
        let _l = self.flush_lock.lock().unwrap_or_else(|e| e.into_inner());
        let log_id = self
            .log
            .log_id()
            .map_err(|e| stop(FlushResult::default(), e))?;
        let mut cur = match self.read_cursor() {
            Ok(c) => c,
            Err(e) => {
                let pending = self.log.read_after(0).map(|r| r.len()).unwrap_or(0);
                return Err(stop(
                    FlushResult {
                        pending,
                        ..FlushResult::default()
                    },
                    e,
                ));
            }
        };
        if cur.log != log_id {
            cur = Cursor {
                log: log_id.clone(),
                seq: 0,
            };
        }
        let recs = self.log.read_after(cur.seq).map_err(|e| {
            stop(
                FlushResult {
                    cursor: cur.seq,
                    ..FlushResult::default()
                },
                e,
            )
        })?;
        let mut res = FlushResult {
            delivered: Vec::new(),
            cursor: cur.seq,
            pending: recs.len(),
        };
        for e in recs {
            let key = e.key().unwrap_or_default();
            if let Err(err) = self.publisher.publish(&self.event(&e)) {
                return Err(stop(res, format!("consent: deliver {key}: {err}")));
            }
            let next = Cursor {
                log: log_id.clone(),
                seq: e.seq,
            };
            if let Err(err) = self.write_cursor(&next) {
                return Err(stop(res, format!("consent: ack {key}: {err}")));
            }
            res.delivered.push(e.seq);
            res.cursor = e.seq;
            res.pending -= 1;
        }
        Ok(res)
    }

    /// Flushes at start, on every notify, and after a failure every
    /// retry interval, until [`Relay::stop`]. Blocks; run it on a thread.
    pub fn run(&self) {
        loop {
            let failed = self.flush().is_err();
            let mut w = self.wake.lock().unwrap_or_else(|e| e.into_inner());
            while !w.notified && !w.stopped {
                if failed {
                    let (g, t) = self
                        .cv
                        .wait_timeout(w, self.retry)
                        .unwrap_or_else(|e| e.into_inner());
                    w = g;
                    if t.timed_out() {
                        break;
                    }
                } else {
                    w = self.cv.wait(w).unwrap_or_else(|e| e.into_inner());
                }
            }
            if w.stopped {
                return;
            }
            w.notified = false;
        }
    }

    /// Ends [`Relay::run`].
    pub fn stop(&self) {
        self.wake.lock().unwrap_or_else(|e| e.into_inner()).stopped = true;
        self.cv.notify_all();
    }
}

impl Delivery for Relay {
    fn notify(&self) {
        self.wake.lock().unwrap_or_else(|e| e.into_inner()).notified = true;
        self.cv.notify_all();
    }

    fn publish_unrecorded(&self, e: &AuditEvent) {
        let _ = self.publisher.publish(&self.event(e));
    }
}
