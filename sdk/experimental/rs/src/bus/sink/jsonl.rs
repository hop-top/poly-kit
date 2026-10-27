//! [`JsonlSink`]: Go's `JSONLSink`.

use std::io::{BufWriter, Write};
use std::sync::Mutex;

use super::gofmt::{json_string, write_json};
use super::Sink;
use crate::bus::context::Context;
use crate::bus::event::{canonical_timestamp, Event};
use crate::bus::mem::BusError;

/// Go's `bufio.Writer` default size.
const BUF_SIZE: usize = 4096;

/// Writes each event as one line of JSON: Go's `JSONLSink`.
///
/// A line is `{"topic":…,"source":…,"timestamp":…,"payload":…}` with
/// Go's encoding, byte for byte: the timestamp in Go's RFC 3339 form,
/// `payload` left out when it is `null`, object keys sorted and `<`,
/// `>`, `&` escaped. Writes are buffered; [`Sink::close`] flushes and
/// releases the writer, after which a drain fails.
pub struct JsonlSink {
    w: Mutex<Option<BufWriter<Box<dyn Write + Send>>>>,
}

impl JsonlSink {
    /// Writes to `w`: Go's `NewJSONLSink`. Close flushes, then drops
    /// `w`, which for a borrowed handle such as stdout closes nothing.
    ///
    /// Unlike Go, the sink owns `w`: an owned `File` passed here is
    /// closed when the sink closes. Pass a `&File` or a clone handle to
    /// keep yours open.
    pub fn new(w: impl Write + Send + 'static) -> Self {
        let w: Box<dyn Write + Send> = Box::new(w);
        JsonlSink {
            w: Mutex::new(Some(BufWriter::with_capacity(BUF_SIZE, w))),
        }
    }

    /// Opens or creates the file at `path` in append mode, never
    /// truncating, with mode 0600 on Unix: Go's `NewJSONLSinkFile`. The
    /// sink owns the file; close flushes and closes it. Not on wasm32.
    #[cfg(not(target_arch = "wasm32"))]
    pub fn open(path: impl AsRef<std::path::Path>) -> std::io::Result<Self> {
        let mut o = std::fs::OpenOptions::new();
        o.create(true).append(true);
        #[cfg(unix)]
        std::os::unix::fs::OpenOptionsExt::mode(&mut o, 0o600);
        Ok(Self::new(o.open(path)?))
    }
}

/// The JSON line for `e`, newline included.
pub(super) fn line(e: &Event) -> String {
    let ts = canonical_timestamp(&e.timestamp).unwrap_or_else(|_| e.timestamp.clone());
    let mut out = String::from("{\"topic\":");
    json_string(&mut out, e.topic.as_str());
    out.push_str(",\"source\":");
    json_string(&mut out, &e.source);
    out.push_str(",\"timestamp\":");
    json_string(&mut out, &ts);
    if !e.payload.is_null() {
        out.push_str(",\"payload\":");
        write_json(&mut out, &e.payload);
    }
    out.push_str("}\n");
    out
}

impl Sink for JsonlSink {
    /// Appends `event` as one line. Fails with Go's `os.ErrClosed` text,
    /// `file already closed`, once the sink is closed.
    fn drain(&self, _ctx: &Context, event: &Event) -> Result<(), BusError> {
        let data = line(event);
        let mut w = self.w.lock().unwrap_or_else(|p| p.into_inner());
        let w = w
            .as_mut()
            .ok_or_else(|| BusError::handler("file already closed"))?;
        w.write_all(data.as_bytes())
            .map_err(|e| BusError::handler(e.to_string()))
    }

    /// Flushes and releases the writer. Idempotent: a second close is
    /// `Ok`. Returns the flush error; the file itself closes on drop,
    /// where std reports no error (Go returns `File.Close`'s).
    fn close(&self) -> Result<(), BusError> {
        let mut w = self.w.lock().unwrap_or_else(|p| p.into_inner());
        match w.take() {
            None => Ok(()),
            Some(mut w) => w.flush().map_err(|e| BusError::handler(e.to_string())),
        }
    }
}
