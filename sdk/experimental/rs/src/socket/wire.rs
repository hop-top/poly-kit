//! The wire protocol: one JSON object per line each way, shapes and
//! codes as `go/transport/socket`.

use std::collections::BTreeMap;
use std::time::Duration;

use serde::{Deserialize, Serialize};
use serde_json::{Map, Value};

/// SocketMode is the permission the socket file is created with.
/// Owner-only: on a Unix domain socket the filesystem permission IS the
/// access control, so anything broader would let any local user invoke
/// the tool's commands.
pub const SOCKET_MODE: u32 = 0o600;

/// Bound on one request line. A longer line is answered with
/// [`CODE_INVALID`] and the connection closes, rather than the server
/// growing a buffer without limit on an unauthenticated local channel.
pub const MAX_LINE_BYTES: usize = 1 << 20;

/// How many request lines a connection may have read ahead of the one
/// being served. Reading ahead is what lets the transport notice a peer
/// that has gone away while a long command is still running; the bound
/// keeps a flooding client from growing memory without limit.
pub const PIPELINE_DEPTH: usize = 16;

/// Longest socket path, in bytes: the lower of the platform `sun_path`
/// bounds, so a configuration is portable.
pub const MAX_PATH_BYTES: usize = 103;

/// One request line.
///
/// `caller` and `tenant` are a claim: without an authenticator they are
/// provenance for the audit trail, not a credential, and grant nothing.
/// With one, its verdict replaces them.
#[derive(Debug, Clone, Default, PartialEq, Deserialize, Serialize)]
pub struct Request {
    /// Command path from the root to the leaf.
    pub path: Vec<String>,
    /// Positional arguments after the path.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub args: Vec<String>,
    /// Flags keyed by long name.
    #[serde(default, skip_serializing_if = "Map::is_empty")]
    pub flags: Map<String, Value>,
    /// Claimed principal identifier.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub caller: String,
    /// Claimed tenant identifier.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub tenant: String,
    /// Identifies the request in the audit trail; issued by the
    /// transport when the caller sends none.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub request_id: String,
    /// Propagates a trace identifier across surfaces.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub trace_id: String,
    /// Makes the request replayable: a repeat from the same caller with
    /// the same key and command is answered from the first's record,
    /// with `replayed` set.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub idempotency_key: String,
}

/// What a handler produced: the `result` of a response.
#[derive(Debug, Clone, Default, PartialEq, Deserialize, Serialize)]
pub struct CommandResult {
    /// Process-style exit status; zero on success.
    pub exit_code: i32,
    /// Captured standard output.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub stdout: String,
    /// Captured standard error.
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub stderr: String,
    /// Structured payload the command produced.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<Value>,
    /// Output exceeded the handler's cap and was cut.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub truncated: bool,
}

impl CommandResult {
    /// A successful result carrying structured data.
    pub fn data(data: Value) -> Self {
        Self {
            data: Some(data),
            ..Self::default()
        }
    }

    /// A successful result carrying standard output.
    pub fn stdout(text: impl Into<String>) -> Self {
        Self {
            stdout: text.into(),
            ..Self::default()
        }
    }
}

/// One response line. Exactly one of `result` and `error` is set, and
/// `ok` says which. `ok: true` with a non-zero `exit_code` means the
/// command ran and failed; `ok: false` means it never ran.
#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
pub struct Response {
    pub ok: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<CommandResult>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub error: Option<WireError>,
    /// `result` is the recorded answer to an earlier request with the
    /// same idempotency key: nothing ran.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub replayed: bool,
}

impl Response {
    pub(crate) fn error(code: &str, message: impl Into<String>) -> Self {
        Self {
            ok: false,
            result: None,
            error: Some(WireError {
                code: code.to_string(),
                message: message.into(),
                retry_after_ms: None,
            }),
            replayed: false,
        }
    }
}

/// The wire form of a refused or failed invocation.
#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
pub struct WireError {
    /// Stable symbol a client can branch on (`CODE_*`).
    pub code: String,
    /// Human-readable detail.
    pub message: String,
    /// Whole milliseconds to wait before retrying a retryable refusal
    /// (`RATE_LIMITED`, `QUOTA_EXCEEDED`, `OVERLOADED`); absent
    /// otherwise.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub retry_after_ms: Option<i64>,
}

/// A path that resolves to no command.
pub const CODE_NOT_FOUND: &str = "NOT_FOUND";
/// A command that exists but is not exposed on this surface.
pub const CODE_NOT_ENABLED: &str = "NOT_ENABLED";
/// A destructive command the policy refuses on this surface.
pub const CODE_BLOCKED: &str = "BLOCKED";
/// A command that can never run through a transport.
pub const CODE_NOT_INVOCABLE: &str = "NOT_INVOCABLE";
/// A command the permission gate refuses for this caller.
pub const CODE_DENIED: &str = "DENIED";
/// A request the authenticator refused.
pub const CODE_UNAUTHENTICATED: &str = "UNAUTHENTICATED";
/// A call the rate limit refused; carries `retry_after_ms`.
pub const CODE_RATE_LIMITED: &str = "RATE_LIMITED";
/// A call the capacity gate refused; carries `retry_after_ms`.
pub const CODE_OVERLOADED: &str = "OVERLOADED";
/// A call the quota refused; carries `retry_after_ms`.
pub const CODE_QUOTA_EXCEEDED: &str = "QUOTA_EXCEEDED";
/// A command that ran past its deadline and was canceled.
pub const CODE_DEADLINE_EXCEEDED: &str = "DEADLINE_EXCEEDED";
/// An idempotency key naming a request still running, or reused for a
/// different command.
pub const CODE_CONFLICT: &str = "CONFLICT";
/// A malformed request line.
pub const CODE_INVALID: &str = "INVALID";
/// Anything else.
pub const CODE_INTERNAL: &str = "INTERNAL";

/// Message prefix of an in-flight key refusal.
pub const IDEMPOTENCY_CONFLICT: &str = "idempotency_conflict";
/// Message prefix of a reused-key refusal.
pub const IDEMPOTENCY_KEY_REUSED: &str = "idempotency_key_reused";

/// How a caller's identity was established.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Established {
    /// The owner-only socket file vouched for the connection. The
    /// claim stays a claim; the caller counts as the server's owner.
    Transport,
    /// An [`Authenticator`](super::Authenticator) verified the caller.
    Verified,
}

/// Provenance a handler receives beside the command.
#[derive(Debug, Clone)]
pub struct Meta {
    /// Principal: the authenticator's verdict, else the claim.
    pub caller: String,
    /// Tenant: the authenticator's verdict, else the claim.
    pub tenant: String,
    /// Request id, issued when the caller sent none.
    pub request_id: String,
    pub trace_id: String,
    pub idempotency_key: String,
    pub established: Established,
    /// Entitlements a verified caller holds.
    pub scopes: Vec<String>,
    /// Provenance the authenticator recorded (peer uid/gid/pid).
    pub extra: BTreeMap<String, String>,
}

/// A decoded, admitted request as a handler sees it.
#[derive(Debug, Clone)]
pub struct Invocation {
    pub path: Vec<String>,
    pub args: Vec<String>,
    pub flags: Map<String, Value>,
    pub meta: Meta,
}

/// A handler's failure, mapped onto a wire error.
#[derive(Debug, Clone, PartialEq)]
pub struct HandlerError {
    /// A `CODE_*` symbol.
    pub code: String,
    pub message: String,
    /// Surfaced as `retry_after_ms` on a retryable refusal.
    pub retry_after: Option<Duration>,
}

impl HandlerError {
    /// An error with an explicit wire code.
    pub fn new(code: &str, message: impl Into<String>) -> Self {
        Self {
            code: code.to_string(),
            message: message.into(),
            retry_after: None,
        }
    }

    /// Anything the handler could not do: `INTERNAL`.
    pub fn internal(message: impl Into<String>) -> Self {
        Self::new(CODE_INTERNAL, message)
    }

    /// Ask the client to wait `after` before retrying.
    pub fn retry_after(mut self, after: Duration) -> Self {
        self.retry_after = Some(after);
        self
    }
}

impl std::fmt::Display for HandlerError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}", self.code, self.message)
    }
}

impl std::error::Error for HandlerError {}
