//! Security headers, a port of Go's `SecurityHeaders`.

use std::time::Duration;

use axum::extract::{Request, State};
use axum::http::{HeaderName, HeaderValue};
use axum::middleware::Next;
use axum::response::Response;
use tower_http::set_header::SetResponseHeaderLayer;

use super::ctx::is_https;

/// The default `Content-Security-Policy`: nothing loads, nothing frames.
pub const DEFAULT_CSP: &str =
    "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'";
/// The default `Strict-Transport-Security` max-age, one year.
pub const DEFAULT_HSTS_MAX_AGE: Duration = Duration::from_secs(365 * 24 * 3600);

/// What the security headers are configured with.
#[derive(Debug, Clone, Default)]
pub struct SecurityHeadersConfig {
    /// Replaces [`DEFAULT_CSP`] when non-empty.
    pub content_security_policy: String,
    /// HSTS max-age: `None` means [`DEFAULT_HSTS_MAX_AGE`],
    /// `Some(Duration::ZERO)` omits the header. Sent only to requests
    /// that arrived over TLS (as a trusted proxy reported it).
    pub hsts_max_age: Option<Duration>,
}

/// The three static headers, set unless the handler already did.
pub(crate) fn static_layers(
    cfg: &SecurityHeadersConfig,
) -> [SetResponseHeaderLayer<HeaderValue>; 3] {
    let csp = if cfg.content_security_policy.is_empty() {
        DEFAULT_CSP
    } else {
        &cfg.content_security_policy
    };
    let csp = HeaderValue::from_str(csp).unwrap_or(HeaderValue::from_static(DEFAULT_CSP));
    [
        SetResponseHeaderLayer::if_not_present(
            HeaderName::from_static("x-content-type-options"),
            HeaderValue::from_static("nosniff"),
        ),
        SetResponseHeaderLayer::if_not_present(
            HeaderName::from_static("referrer-policy"),
            HeaderValue::from_static("no-referrer"),
        ),
        SetResponseHeaderLayer::if_not_present(
            HeaderName::from_static("content-security-policy"),
            csp,
        ),
    ]
}

/// The HSTS value, if the header is on.
pub(crate) fn hsts_value(cfg: &SecurityHeadersConfig) -> Option<HeaderValue> {
    let age = cfg.hsts_max_age.unwrap_or(DEFAULT_HSTS_MAX_AGE);
    if age.is_zero() {
        return None;
    }
    HeaderValue::from_str(&format!("max-age={}", age.as_secs())).ok()
}

pub(crate) async fn hsts(State(v): State<HeaderValue>, req: Request, next: Next) -> Response {
    let https = is_https(&req);
    let mut resp = next.run(req).await;
    if https {
        resp.headers_mut()
            .entry(HeaderName::from_static("strict-transport-security"))
            .or_insert(v);
    }
    resp
}
