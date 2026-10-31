use axum::body::Body;
use axum::http::{header, HeaderValue, StatusCode};
use axum::response::Response;
use serde::Serialize;

/// Refusal code of a host the listener does not serve.
pub const CODE_HOST_REJECTED: &str = "host_rejected";
/// Refusal code of a cross-origin request.
pub const CODE_ORIGIN_REJECTED: &str = "origin_rejected";
/// Refusal code of a body over the limit.
pub const CODE_BODY_TOO_LARGE: &str = "body_too_large";
/// Refusal code of a missing or failed credential (401).
pub const CODE_UNAUTHENTICATED: &str = "unauthenticated";
/// Refusal code of the rate limit (429).
pub const CODE_RATE_LIMITED: &str = "rate_limited";
/// Refusal code of a method a route does not answer.
pub const CODE_METHOD_NOT_ALLOWED: &str = "method_not_allowed";
/// The WWW-Authenticate challenge a 401 carries unless another is set:
/// the bearer scheme of RFC 6750, which kit's verifiers accept.
pub const DEFAULT_AUTH_CHALLENGE: &str = "Bearer";

/// The error body every kit HTTP surface answers a refusal with.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct ApiError {
    /// The HTTP status.
    pub status: u16,
    /// A stable machine-readable code.
    pub code: String,
    /// A human-readable message.
    pub message: String,
}

impl ApiError {
    /// An error with `status`, `code` and `message`.
    pub fn new(status: u16, code: &str, message: impl Into<String>) -> Self {
        Self {
            status,
            code: code.to_string(),
            message: message.into(),
        }
    }

    /// The JSON response: the body is the error, newline-terminated as
    /// Go's encoder writes it.
    pub fn into_response(self) -> Response {
        json_response(self.status, &self)
    }
}

/// A JSON response with `status`: `Content-Type: application/json` and
/// the value serialised with a trailing newline, as Go's `api.JSON`
/// writes it.
pub fn json_response<T: Serialize>(status: u16, value: &T) -> Response {
    let mut body = serde_json::to_vec(value).unwrap_or_else(|_| b"null".to_vec());
    body.push(b'\n');
    let mut resp = Response::new(Body::from(body));
    *resp.status_mut() = StatusCode::from_u16(status).unwrap_or(StatusCode::INTERNAL_SERVER_ERROR);
    resp.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    resp
}

/// 401 with a `WWW-Authenticate` challenge and the
/// [`CODE_UNAUTHENTICATED`] body, as every kit HTTP surface answers the
/// unauthenticated class.
pub fn unauthenticated(challenge: &str, message: &str) -> Response {
    let mut resp = ApiError::new(401, CODE_UNAUTHENTICATED, message).into_response();
    let challenge = if challenge.is_empty() {
        DEFAULT_AUTH_CHALLENGE
    } else {
        challenge
    };
    if let Ok(v) = HeaderValue::from_str(challenge) {
        resp.headers_mut().insert(header::WWW_AUTHENTICATE, v);
    }
    resp
}

/// A refusal rewriter: the transport may answer a refusal in its own
/// envelope instead of [`ApiError`]'s JSON.
pub type RefusalWriter = std::sync::Arc<dyn Fn(ApiError) -> Response + Send + Sync>;

pub(crate) fn refuse(rw: &Option<RefusalWriter>, e: ApiError) -> Response {
    match rw {
        Some(w) => w(e),
        None => e.into_response(),
    }
}

/// Why a server could not be built.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ApiServerError(pub String);

impl std::fmt::Display for ApiServerError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for ApiServerError {}
