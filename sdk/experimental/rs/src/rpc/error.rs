//! Kit-owned error vocabulary: [`Code`] and [`RpcError`]. The adapter
//! maps them onto connectrpc's; neither names it.

use std::fmt;

use connectrpc::{ConnectError, ErrorCode};

/// An RPC status code: Connect's sixteen failure codes, by their wire
/// names (`not_found`, `unauthenticated`, ...), as in the Go port.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Code {
    Canceled,
    Unknown,
    InvalidArgument,
    DeadlineExceeded,
    NotFound,
    AlreadyExists,
    PermissionDenied,
    ResourceExhausted,
    FailedPrecondition,
    Aborted,
    OutOfRange,
    Unimplemented,
    Internal,
    Unavailable,
    DataLoss,
    Unauthenticated,
}

impl Code {
    /// The wire name: `not_found`, `invalid_argument`, ...
    pub fn as_str(self) -> &'static str {
        to_connect_code(self).as_str()
    }
}

impl fmt::Display for Code {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// A failed call: a [`Code`], a message the caller sees, and response
/// metadata (headers in Connect, trailers in gRPC) such as a
/// `WWW-Authenticate` challenge.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RpcError {
    pub code: Code,
    pub message: String,
    /// Metadata sent with the refusal, as `(name, value)`.
    pub meta: Vec<(String, String)>,
}

impl RpcError {
    pub fn new(code: Code, message: impl Into<String>) -> Self {
        Self {
            code,
            message: message.into(),
            meta: Vec::new(),
        }
    }

    pub fn unauthenticated(message: impl Into<String>) -> Self {
        Self::new(Code::Unauthenticated, message)
    }

    pub fn not_found(message: impl Into<String>) -> Self {
        Self::new(Code::NotFound, message)
    }

    pub fn invalid_argument(message: impl Into<String>) -> Self {
        Self::new(Code::InvalidArgument, message)
    }

    pub fn internal(message: impl Into<String>) -> Self {
        Self::new(Code::Internal, message)
    }

    /// Attach response metadata.
    pub fn with_meta(mut self, name: &str, value: &str) -> Self {
        self.meta.push((name.to_string(), value.to_string()));
        self
    }
}

impl fmt::Display for RpcError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}: {}", self.code, self.message)
    }
}

impl std::error::Error for RpcError {}

pub(crate) fn to_connect_code(c: Code) -> ErrorCode {
    match c {
        Code::Canceled => ErrorCode::Canceled,
        Code::Unknown => ErrorCode::Unknown,
        Code::InvalidArgument => ErrorCode::InvalidArgument,
        Code::DeadlineExceeded => ErrorCode::DeadlineExceeded,
        Code::NotFound => ErrorCode::NotFound,
        Code::AlreadyExists => ErrorCode::AlreadyExists,
        Code::PermissionDenied => ErrorCode::PermissionDenied,
        Code::ResourceExhausted => ErrorCode::ResourceExhausted,
        Code::FailedPrecondition => ErrorCode::FailedPrecondition,
        Code::Aborted => ErrorCode::Aborted,
        Code::OutOfRange => ErrorCode::OutOfRange,
        Code::Unimplemented => ErrorCode::Unimplemented,
        Code::Internal => ErrorCode::Internal,
        Code::Unavailable => ErrorCode::Unavailable,
        Code::DataLoss => ErrorCode::DataLoss,
        Code::Unauthenticated => ErrorCode::Unauthenticated,
    }
}

fn from_connect_code(c: ErrorCode) -> Code {
    match c {
        ErrorCode::Canceled => Code::Canceled,
        ErrorCode::Unknown => Code::Unknown,
        ErrorCode::InvalidArgument => Code::InvalidArgument,
        ErrorCode::DeadlineExceeded => Code::DeadlineExceeded,
        ErrorCode::NotFound => Code::NotFound,
        ErrorCode::AlreadyExists => Code::AlreadyExists,
        ErrorCode::PermissionDenied => Code::PermissionDenied,
        ErrorCode::ResourceExhausted => Code::ResourceExhausted,
        ErrorCode::FailedPrecondition => Code::FailedPrecondition,
        ErrorCode::Aborted => Code::Aborted,
        ErrorCode::OutOfRange => Code::OutOfRange,
        ErrorCode::Unimplemented => Code::Unimplemented,
        ErrorCode::Internal => Code::Internal,
        ErrorCode::Unavailable => Code::Unavailable,
        ErrorCode::DataLoss => Code::DataLoss,
        ErrorCode::Unauthenticated => Code::Unauthenticated,
        // ErrorCode is non-exhaustive: a code a later connectrpc adds is
        // unknown to this port.
        _ => Code::Unknown,
    }
}

impl From<RpcError> for ConnectError {
    fn from(e: RpcError) -> Self {
        let mut err = ConnectError::new(to_connect_code(e.code), e.message);
        if !e.meta.is_empty() {
            let mut h = axum::http::HeaderMap::new();
            for (k, v) in &e.meta {
                if let (Ok(k), Ok(v)) = (
                    axum::http::HeaderName::from_bytes(k.as_bytes()),
                    axum::http::HeaderValue::from_str(v),
                ) {
                    h.append(k, v);
                }
            }
            err = err.with_headers(h);
        }
        err
    }
}

impl From<&ConnectError> for RpcError {
    fn from(e: &ConnectError) -> Self {
        RpcError::new(
            from_connect_code(e.code),
            e.message.clone().unwrap_or_default(),
        )
    }
}
