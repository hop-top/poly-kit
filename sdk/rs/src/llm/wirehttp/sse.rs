//! Incremental `text/event-stream` decoding, line for line what the Go
//! SDK's decoder does: lines end at `\n` (a trailing `\r` dropped), a
//! blank line dispatches, `field: value` takes one optional space,
//! `:` lines are comments, `data` lines join with `\n`. A body ending
//! mid-event drops that event. No dependency: the format is a few rules.

use std::collections::VecDeque;

/// Default cap on one event: bytes, `\r` aside, since the last blank
/// line. Provider param `max_event_bytes` overrides it.
pub(crate) const DEFAULT_MAX_EVENT: u64 = 8 << 20;

/// One dispatched event.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct SseEvent {
    pub event: String,
    pub data: String,
}

#[derive(Debug)]
pub(crate) struct SseDecoder {
    max_event: u64,
    event_bytes: u64,
    prev: u8,
    line: Vec<u8>,
    event: String,
    data: String,
    has_data: bool,
    ready: VecDeque<SseEvent>,
}

impl Default for SseDecoder {
    fn default() -> Self {
        Self::with_limit(DEFAULT_MAX_EVENT)
    }
}

impl SseDecoder {
    pub fn with_limit(max_event: u64) -> Self {
        SseDecoder {
            max_event,
            event_bytes: 0,
            prev: 0,
            line: Vec::new(),
            event: String::new(),
            data: String::new(),
            has_data: false,
            ready: VecDeque::new(),
        }
    }

    /// Feed body bytes; complete events queue for [`SseDecoder::pop`].
    /// `Err` carries the limit when one event outgrows it.
    pub fn feed(&mut self, bytes: &[u8]) -> Result<(), u64> {
        for &b in bytes {
            if b != b'\r' {
                if b == b'\n' && self.prev == b'\n' {
                    self.event_bytes = 0;
                } else {
                    self.event_bytes += 1;
                }
                self.prev = b;
                if self.event_bytes > self.max_event {
                    return Err(self.max_event);
                }
            }
            if b != b'\n' {
                self.line.push(b);
                continue;
            }
            if self.line.last() == Some(&b'\r') {
                self.line.pop();
            }
            let line = std::mem::take(&mut self.line);
            self.line_done(&String::from_utf8_lossy(&line));
        }
        Ok(())
    }

    pub fn pop(&mut self) -> Option<SseEvent> {
        self.ready.pop_front()
    }

    fn line_done(&mut self, line: &str) {
        if line.is_empty() {
            if self.event.is_empty() && !self.has_data {
                return;
            }
            self.ready.push_back(SseEvent {
                event: std::mem::take(&mut self.event),
                data: std::mem::take(&mut self.data),
            });
            self.has_data = false;
            return;
        }
        let (name, value) = line.split_once(':').unwrap_or((line, ""));
        let value = value.strip_prefix(' ').unwrap_or(value);
        match name {
            "event" => self.event = value.to_string(),
            "data" => {
                self.data.push_str(value);
                self.data.push('\n');
                self.has_data = true;
            }
            _ => {} // comments ("") and unknown fields
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn decode(chunks: &[&str]) -> Vec<SseEvent> {
        let mut d = SseDecoder::default();
        let mut out = Vec::new();
        for c in chunks {
            d.feed(c.as_bytes()).unwrap();
            while let Some(e) = d.pop() {
                out.push(e);
            }
        }
        out
    }

    #[test]
    fn events_split_across_chunks() {
        let got = decode(&[
            "event: a\nda",
            "ta: {\"x\"",
            ":1}\n\nevent: b\r\ndata:2\r\n\r\n",
        ]);
        assert_eq!(
            got,
            [
                SseEvent {
                    event: "a".into(),
                    data: "{\"x\":1}\n".into()
                },
                SseEvent {
                    event: "b".into(),
                    data: "2\n".into()
                }
            ]
        );
    }

    #[test]
    fn comments_blank_runs_and_unfinished_events() {
        let got = decode(&[
            ": ping\n\n\n",
            "event: c\ndata: 1\ndata: 2\n\nevent: d\ndata: 3\n",
        ]);
        assert_eq!(
            got,
            [SseEvent {
                event: "c".into(),
                data: "1\n2\n".into()
            }]
        );
    }
}
