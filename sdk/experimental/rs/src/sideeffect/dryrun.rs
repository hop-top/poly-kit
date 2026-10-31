//! Describing implementations of the seams. Each call writes a
//! human-readable line to a writer (default stderr) and returns a
//! synthetic but plausibly-shaped answer.
//!
//! They never block and never fail on a would-be call: a preview must
//! complete even when the real call would have errored. Adopters that
//! want stricter semantics (refuse rather than synthesise success)
//! compose their own wrapper over the real implementation.
//!
//! HTTP: safe verbs (GET, HEAD, and the empty method) are forwarded to
//! the wrapped client, because read-only requests are part of the preview
//! and the call site needs the real response shape. Mutating verbs are
//! intercepted and answered with a synthetic 201 Created. The printed
//! URL drops userinfo and fragment and replaces every query value with
//! `REDACTED`, as the output lands in terminals and CI logs.
//!
//! Bus: a payload that carries qualifiers is described with
//! `mechanism=dry_run`; others are described plain, and the gap is
//! logged once per bus.
//!
//! Exec: the argv is printed and the call returns success with empty
//! output.

use std::io::{self, Write};
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex, PoisonError};

use serde_json::Value;

use super::publisher::{tag_mechanism, DRY_RUN_MECHANISM};
use super::seams::{
    self as seams, BoxFuture, Command, ExecError, HttpError, PublishError, Request, Response,
};
use crate::bus::Context;

/// Where the description lines go. Cloning shares the writer.
#[derive(Clone)]
struct Out(Arc<Mutex<Box<dyn Write + Send>>>);

impl Out {
    fn stderr() -> Self {
        Out::new(io::stderr())
    }

    fn new(w: impl Write + Send + 'static) -> Self {
        Out(Arc::new(Mutex::new(Box::new(w))))
    }

    /// Writes one line. A failing writer must not fail the preview.
    fn line(&self, line: std::fmt::Arguments<'_>) {
        let mut w = self.0.lock().unwrap_or_else(PoisonError::into_inner);
        let _ = writeln!(w, "{line}");
    }
}

/// An in-memory writer that can be cloned and read back, for capturing
/// what a dryrun implementation printed.
#[derive(Clone, Default)]
pub struct SharedBuf(Arc<Mutex<Vec<u8>>>);

impl SharedBuf {
    /// Everything written so far, as text.
    pub fn contents(&self) -> String {
        let b = self.0.lock().unwrap_or_else(PoisonError::into_inner);
        String::from_utf8_lossy(&b).into_owned()
    }
}

impl Write for SharedBuf {
    fn write(&mut self, buf: &[u8]) -> io::Result<usize> {
        self.0
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .extend_from_slice(buf);
        Ok(buf.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

/// `url` safe to print: userinfo and fragment dropped, each query value
/// replaced by `REDACTED` (keys kept); scheme, host, port and path as
/// given. Works on the text, so an unparseable URL is still never echoed
/// with a value.
fn preview_url(url: &str) -> String {
    let url = url.split('#').next().unwrap_or("");
    let (base, query) = match url.split_once('?') {
        Some((b, q)) => (b, Some(q)),
        None => (url, None),
    };
    let mut out = match base.split_once("://") {
        Some((scheme, rest)) => {
            let (authority, path) = rest.split_at(rest.find('/').unwrap_or(rest.len()));
            let host = authority.rsplit_once('@').map_or(authority, |(_, h)| h);
            format!("{scheme}://{host}{path}")
        }
        None => base.to_string(),
    };
    if let Some(q) = query {
        let pairs: Vec<_> = q
            .split('&')
            .map(|kv| match kv.split_once('=') {
                Some((k, _)) => format!("{k}=REDACTED"),
                None => kv.to_string(),
            })
            .collect();
        out.push('?');
        out.push_str(&pairs.join("&"));
    }
    out
}

/// A Unix mode as Go's `%#o` prints it: a leading 0 (`0644`).
fn octal(mode: u32) -> String {
    format!("0{mode:o}")
}

/// The dryrun [`Fs`](seams::Fs).
#[derive(Clone)]
pub struct Fs(Out);

impl Fs {
    pub fn new() -> Self {
        Fs(Out::stderr())
    }

    /// Redirects the description lines.
    pub fn with_writer(self, w: impl Write + Send + 'static) -> Self {
        Fs(Out::new(w))
    }
}

impl Default for Fs {
    fn default() -> Self {
        Self::new()
    }
}

impl seams::Fs for Fs {
    fn write_file(&self, path: &Path, data: &[u8], mode: u32) -> io::Result<()> {
        self.0.line(format_args!(
            "[dry-run] would write {} ({} bytes, mode {})",
            path.display(),
            data.len(),
            octal(mode)
        ));
        Ok(())
    }

    fn mkdir_all(&self, path: &Path, mode: u32) -> io::Result<()> {
        self.0.line(format_args!(
            "[dry-run] would mkdir -p {} (mode {})",
            path.display(),
            octal(mode)
        ));
        Ok(())
    }

    fn rename(&self, from: &Path, to: &Path) -> io::Result<()> {
        self.0.line(format_args!(
            "[dry-run] would rename {} -> {}",
            from.display(),
            to.display()
        ));
        Ok(())
    }

    fn remove(&self, path: &Path) -> io::Result<()> {
        self.0
            .line(format_args!("[dry-run] would remove {}", path.display()));
        Ok(())
    }
}

/// The dryrun [`Http`](seams::Http): safe verbs go to `inner`, mutating
/// verbs get a synthetic 201.
#[derive(Clone)]
pub struct Http {
    out: Out,
    inner: Arc<dyn seams::Http>,
}

impl Http {
    /// `inner` serves the safe verbs.
    pub fn new(inner: Arc<dyn seams::Http>) -> Self {
        Http {
            out: Out::stderr(),
            inner,
        }
    }

    /// Redirects the description lines.
    pub fn with_writer(self, w: impl Write + Send + 'static) -> Self {
        Http {
            out: Out::new(w),
            ..self
        }
    }
}

impl seams::Http for Http {
    fn send(&self, req: Request) -> BoxFuture<'_, Result<Response, HttpError>> {
        // An empty method is GET, as in net/http.
        if matches!(req.method.as_str(), "GET" | "HEAD" | "") {
            return self.inner.send(req);
        }
        self.out.line(format_args!(
            "[dry-run] would {} {} ({}-byte body)",
            req.method,
            preview_url(&req.url),
            req.body.len()
        ));
        Box::pin(std::future::ready(Ok(Response::empty_json(201))))
    }
}

/// The dryrun [`Bus`](seams::Bus).
pub struct Bus {
    out: Out,
    gap_logged: AtomicBool,
}

impl Bus {
    pub fn new() -> Self {
        Bus {
            out: Out::stderr(),
            gap_logged: AtomicBool::new(false),
        }
    }

    /// Redirects the description lines.
    pub fn with_writer(self, w: impl Write + Send + 'static) -> Self {
        Bus {
            out: Out::new(w),
            ..self
        }
    }
}

impl Default for Bus {
    fn default() -> Self {
        Self::new()
    }
}

/// A printable name for a payload's JSON type, for the gap notice.
fn payload_kind(v: &Value) -> &'static str {
    match v {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    }
}

impl seams::Bus for Bus {
    fn publish(
        &self,
        _: &Context,
        topic: &str,
        source: &str,
        mut payload: Value,
    ) -> Result<(), PublishError> {
        if tag_mechanism(&mut payload, DRY_RUN_MECHANISM) {
            self.out.line(format_args!(
                "[dry-run] would publish {topic} from {source} (mechanism={DRY_RUN_MECHANISM})"
            ));
            return Ok(());
        }
        // No qualifiers: say so once per bus, so adopters notice.
        if !self.gap_logged.swap(true, Ordering::Relaxed) {
            self.out.line(format_args!(
                "[dry-run] note: {} payload does not embed bus.Qualifiers; \
                 mechanism tag will not appear on this event",
                payload_kind(&payload)
            ));
        }
        self.out.line(format_args!(
            "[dry-run] would publish {topic} from {source}"
        ));
        Ok(())
    }
}

/// The dryrun [`Exec`](seams::Exec).
#[derive(Clone)]
pub struct Exec(Out);

impl Exec {
    pub fn new() -> Self {
        Exec(Out::stderr())
    }

    /// Redirects the description lines.
    pub fn with_writer(self, w: impl Write + Send + 'static) -> Self {
        Exec(Out::new(w))
    }
}

impl Default for Exec {
    fn default() -> Self {
        Self::new()
    }
}

/// The argv on one line, tokens with a space, tab, newline or quote
/// wrapped in quotes. Informational, not round-trippable.
fn argv(cmd: &Command) -> String {
    cmd.argv()
        .iter()
        .map(|a| {
            if a.contains([' ', '\t', '\n', '"']) {
                format!("\"{}\"", a.replace('"', "\\\""))
            } else {
                a.clone()
            }
        })
        .collect::<Vec<_>>()
        .join(" ")
}

impl seams::Exec for Exec {
    fn run(&self, cmd: &Command) -> Result<(), ExecError> {
        self.0
            .line(format_args!("[dry-run] would exec: {}", argv(cmd)));
        Ok(())
    }

    fn output(&self, cmd: &Command) -> Result<Vec<u8>, ExecError> {
        self.0.line(format_args!(
            "[dry-run] would exec (capture): {}",
            argv(cmd)
        ));
        Ok(Vec::new())
    }
}
