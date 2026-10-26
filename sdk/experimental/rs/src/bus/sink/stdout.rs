//! [`StdoutSink`]: Go's `StdoutSink`.

use std::io::Write;
use std::sync::Mutex;

use super::gofmt::verb_v;
use super::Sink;
use crate::bus::context::Context;
use crate::bus::event::{canonical_timestamp, Event};
use crate::bus::mem::BusError;

/// Characters of the payload summary kept before `...`.
const PAYLOAD_MAX_LEN: usize = 120;

/// Writes one human-readable line per event: Go's `StdoutSink`.
///
/// The line is `[2006-01-02T15:04:05] topic source: payload`: the
/// timestamp to the second in the event's own offset, then the payload
/// as Go's `%v` prints it, cut to 120 characters plus `...` when
/// longer.
pub struct StdoutSink {
    w: Mutex<Box<dyn Write + Send>>,
}

impl Default for StdoutSink {
    fn default() -> Self {
        Self::new()
    }
}

impl StdoutSink {
    /// Writes to the process's standard output: Go's `NewStdoutSink`.
    pub fn new() -> Self {
        Self::with_writer(std::io::stdout())
    }

    /// Writes to `w`: Go's `NewStdoutSinkWriter`.
    pub fn with_writer(w: impl Write + Send + 'static) -> Self {
        StdoutSink {
            w: Mutex::new(Box::new(w)),
        }
    }
}

/// The line for `e`, newline included.
pub(super) fn line(e: &Event) -> String {
    let ts = canonical_timestamp(&e.timestamp).unwrap_or_else(|_| e.timestamp.clone());
    // Canonical timestamps start with the 19-byte `YYYY-MM-DDThh:mm:ss`.
    let ts = ts.get(..19).unwrap_or(&ts);
    let summary = truncate(&verb_v(&e.payload), PAYLOAD_MAX_LEN);
    format!("[{ts}] {} {}: {summary}\n", e.topic, e.source)
}

/// `s` if it has at most `max` characters, else its first `max`
/// followed by `...`.
fn truncate(s: &str, max: usize) -> String {
    match s.char_indices().nth(max) {
        None => s.to_string(),
        Some((i, _)) => format!("{}...", &s[..i]),
    }
}

impl Sink for StdoutSink {
    /// Writes the event's line and flushes it.
    fn drain(&self, _ctx: &Context, event: &Event) -> Result<(), BusError> {
        let data = line(event);
        let mut w = self.w.lock().unwrap_or_else(|p| p.into_inner());
        w.write_all(data.as_bytes())
            .and_then(|()| w.flush())
            .map_err(|e| BusError::handler(e.to_string()))
    }

    /// Does nothing: the sink does not own its writer.
    fn close(&self) -> Result<(), BusError> {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::truncate;

    // Go's TestTruncateBehavior.
    #[test]
    fn truncate_behavior() {
        let long = "x".repeat(130);
        let r = truncate(&long, 120);
        assert_eq!(r.chars().count(), 123, "120 + '...'");
        assert!(r.ends_with("..."));
        let short = "y".repeat(50);
        assert_eq!(truncate(&short, 120), short);
        assert_eq!(truncate("", 120), "");
        assert_eq!(
            truncate(&"é".repeat(121), 120),
            format!("{}...", "é".repeat(120))
        );
        assert_eq!(truncate(&"é".repeat(120), 120), "é".repeat(120));
    }
}
