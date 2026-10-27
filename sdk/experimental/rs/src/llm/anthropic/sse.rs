//! Incremental `text/event-stream` decoding, line for line what the Go
//! SDK's decoder does: lines end at `\n` (a trailing `\r` dropped), a
//! blank line dispatches, `field: value` takes one optional space,
//! `:` lines are comments, `data` lines join with `\n`. A body ending
//! mid-event drops that event. No dependency: the format is a few rules.

use std::collections::VecDeque;

/// Longest line accepted, the Go SDK's scanner cap.
const MAX_LINE: usize = 32 << 20;

/// One dispatched event.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct SseEvent {
    pub event: String,
    pub data: String,
}

#[derive(Debug, Default)]
pub(crate) struct SseDecoder {
    line: Vec<u8>,
    event: String,
    data: String,
    has_data: bool,
    ready: VecDeque<SseEvent>,
}

impl SseDecoder {
    /// Feed body bytes; complete events queue for [`SseDecoder::pop`].
    pub fn feed(&mut self, bytes: &[u8]) -> Result<(), String> {
        for &b in bytes {
            if b != b'\n' {
                if self.line.len() >= MAX_LINE {
                    return Err("anthropic: stream line exceeds 32 MiB".into());
                }
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
