//! The four seams and the plain-data types they pass.
//!
//! Every type here is pure (no I/O, no clock), so the traits, the dryrun
//! and the testfake implementations build on wasm32-unknown-unknown.

use std::fmt;
use std::future::Future;
use std::io;
use std::path::{Path, PathBuf};
use std::pin::Pin;

use serde_json::Value;

use crate::bus::{BusError, Context};

/// The mutating-only file-system seam. Reads (open, metadata, read,
/// read_dir) intentionally bypass it and use `std::fs` directly.
///
/// Paths are interpreted by the implementation, not normalised by the
/// trait. `mode` is the Unix permission; the real implementation applies
/// it on Unix only.
pub trait Fs: Send + Sync {
    /// Writes `data` to `path`: creates the file if missing, truncates
    /// and overwrites it if present. Mirrors `std::fs::write`, with a
    /// creation mode.
    fn write_file(&self, path: &Path, data: &[u8], mode: u32) -> io::Result<()>;

    /// Creates `path` and any missing parents. Mirrors
    /// `std::fs::create_dir_all`, with a creation mode.
    fn mkdir_all(&self, path: &Path, mode: u32) -> io::Result<()>;

    /// Renames (moves) `from` to `to`. Mirrors `std::fs::rename`.
    fn rename(&self, from: &Path, to: &Path) -> io::Result<()>;

    /// Removes the named file or empty directory. Mirrors
    /// `std::fs::remove_file` for a file and `remove_dir` for a
    /// directory, as Go's `os.Remove` does.
    fn remove(&self, path: &Path) -> io::Result<()>;
}

/// A boxed future, so the HTTP seam stays object safe without an
/// async-trait dependency.
pub type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// An HTTP request, as plain data. Go passes `*http.Request`; this is the
/// part of it the seam and its implementations read.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Request {
    /// The verb. Empty means GET, as in `net/http`.
    pub method: String,
    pub url: String,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
}

impl Request {
    pub fn new(method: impl Into<String>, url: impl Into<String>) -> Self {
        Request {
            method: method.into(),
            url: url.into(),
            ..Request::default()
        }
    }

    pub fn get(url: impl Into<String>) -> Self {
        Request::new("GET", url)
    }

    pub fn with_header(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.push((name.into(), value.into()));
        self
    }

    pub fn with_body(mut self, body: impl Into<Vec<u8>>) -> Self {
        self.body = body.into();
        self
    }
}

/// An HTTP response, as plain data.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Response {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
}

impl Response {
    pub fn new(status: u16) -> Self {
        Response {
            status,
            ..Response::default()
        }
    }

    pub fn with_body(mut self, body: impl Into<Vec<u8>>) -> Self {
        self.body = body.into();
        self
    }

    pub fn with_header(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.push((name.into(), value.into()));
        self
    }

    /// The first header named `name`, ASCII case-insensitively.
    pub fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(k, _)| k.eq_ignore_ascii_case(name))
            .map(|(_, v)| v.as_str())
    }

    /// The synthetic answer dryrun and testfake give: `status` with an
    /// empty JSON object body, so a caller that decodes JSON
    /// unconditionally does not fail.
    pub(crate) fn empty_json(status: u16) -> Self {
        Response::new(status)
            .with_header("Content-Type", "application/json")
            .with_body(b"{}".to_vec())
    }
}

/// An HTTP failure: the request was not answered.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HttpError {
    message: String,
    offline: bool,
}

impl HttpError {
    pub fn new(message: impl Into<String>) -> Self {
        HttpError {
            message: message.into(),
            offline: false,
        }
    }

    /// A refusal by the `--offline` policy.
    pub fn offline(message: impl Into<String>) -> Self {
        HttpError {
            message: message.into(),
            offline: true,
        }
    }

    /// Whether the request was refused by the `--offline` policy.
    pub fn is_offline(&self) -> bool {
        self.offline
    }
}

impl fmt::Display for HttpError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for HttpError {}

/// Wraps HTTP. The implementation decides whether the request is sent on
/// the wire.
///
/// Safe verbs (GET, HEAD) always reach the underlying client even in
/// dry-run mode; the dryrun implementation forwards them. Mutating verbs
/// (POST, PUT, PATCH, DELETE) are intercepted.
///
/// Transport settings (timeouts, redirects, proxies) belong to the
/// implementation, not the trait.
pub trait Http: Send + Sync {
    /// Sends `req` and returns the response. Mirrors Go's
    /// `(*http.Client).Do`; as there, a non-2xx status is a response, not
    /// an error.
    fn send(&self, req: Request) -> BoxFuture<'_, Result<Response, HttpError>>;
}

/// Why a [`Bus`] publish failed.
#[derive(Debug)]
#[non_exhaustive]
pub enum PublishError {
    /// [`DryRunPublisher`](super::DryRunPublisher) has no inner bus.
    NilInner,
    /// The real bus has no adapter.
    NilPublisher,
    /// The wrapped bus refused the event.
    Bus(BusError),
}

impl fmt::Display for PublishError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            PublishError::NilInner => f.write_str("sideeffect: DryRunPublisher with nil Inner"),
            PublishError::NilPublisher => f.write_str("sideeffect/real: nil publisher"),
            PublishError::Bus(e) => e.fmt(f),
        }
    }
}

impl std::error::Error for PublishError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            PublishError::Bus(e) => Some(e),
            _ => None,
        }
    }
}

impl From<BusError> for PublishError {
    fn from(e: BusError) -> Self {
        PublishError::Bus(e)
    }
}

/// The event-publication seam.
///
/// The payload is a JSON value, taken by value. The dryrun implementation
/// tags `mechanism: "dry_run"` on a payload that carries
/// [`Qualifiers`](crate::bus::Qualifiers) (see
/// [`tag_mechanism`](super::tag_mechanism)); other payloads are published
/// unchanged.
pub trait Bus: Send + Sync {
    /// Publishes an event. Mirrors Go's `domain.EventPublisher.Publish`.
    fn publish(
        &self,
        ctx: &Context,
        topic: &str,
        source: &str,
        payload: Value,
    ) -> Result<(), PublishError>;
}

/// A subprocess to run: the part of Go's `*exec.Cmd` the seam reads.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Command {
    pub program: String,
    pub args: Vec<String>,
    pub current_dir: Option<PathBuf>,
    /// Variables added to the inherited environment.
    pub env: Vec<(String, String)>,
}

impl Command {
    pub fn new(program: impl Into<String>) -> Self {
        Command {
            program: program.into(),
            ..Command::default()
        }
    }

    pub fn args<I, S>(mut self, args: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: Into<String>,
    {
        self.args.extend(args.into_iter().map(Into::into));
        self
    }

    pub fn env(mut self, key: impl Into<String>, value: impl Into<String>) -> Self {
        self.env.push((key.into(), value.into()));
        self
    }

    pub fn current_dir(mut self, dir: impl Into<PathBuf>) -> Self {
        self.current_dir = Some(dir.into());
        self
    }

    /// Program then arguments: Go's `cmd.Args`.
    pub(crate) fn argv(&self) -> Vec<String> {
        std::iter::once(self.program.clone())
            .chain(self.args.iter().cloned())
            .collect()
    }
}

/// Why a subprocess did not succeed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ExecError {
    /// The process could not be started.
    Spawn {
        kind: io::ErrorKind,
        message: String,
    },
    /// The process ran and exited unsuccessfully (`code` is `None` when a
    /// signal ended it). `stderr` is filled by `output` only.
    Exit { code: Option<i32>, stderr: Vec<u8> },
}

impl fmt::Display for ExecError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            ExecError::Spawn { message, .. } => write!(f, "exec: {message}"),
            ExecError::Exit { code: Some(c), .. } => write!(f, "exit status {c}"),
            ExecError::Exit { code: None, .. } => f.write_str("exit by signal"),
        }
    }
}

impl std::error::Error for ExecError {}

/// The subprocess seam: fire-and-forget (`run`) and capture-stdout
/// (`output`).
///
/// Subprocess containment in dry-run is hopeless, as the child has its
/// own decisions to make. The dryrun implementation prints the argv and
/// skips the call; this is a documented limitation.
pub trait Exec: Send + Sync {
    /// Starts the command and waits for it. Mirrors `(*exec.Cmd).Run`.
    fn run(&self, cmd: &Command) -> Result<(), ExecError>;

    /// Runs the command and returns its standard output. Mirrors
    /// `(*exec.Cmd).Output`.
    fn output(&self, cmd: &Command) -> Result<Vec<u8>, ExecError>;
}
