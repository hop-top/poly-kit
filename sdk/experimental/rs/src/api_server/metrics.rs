//! The metrics scrape endpoint, a port of Go's `MetricsRoute`.
//!
//! A scraper carries no credentials, so the endpoint is answered before
//! the router's own guards (body limit, compression, rate limit,
//! authentication) and ends the request there. It sits inside the Host
//! and Origin checks: the exposition discloses what the service does,
//! so a DNS-rebinding page must not read it, and a scraper already
//! sends a host the check allows. Because it skips authentication, a
//! non-loopback bind is refused unless [`MetricsConfig::allow_remote`].
//!
//! Like the health routes, it is answered ahead of the router: a route
//! of the adopter's at the same path is shadowed, so pick another path.
//! It answers `GET` and `HEAD`, never caches, and appears in no OpenAPI
//! document.

use axum::extract::{Request, State};
use axum::http::{header, HeaderValue, Method};
use axum::middleware::Next;
use axum::response::Response;
use axum::routing::MethodRouter;
use axum::Router;
use tower::ServiceExt;

use super::error::{ApiError, ApiServerError, CODE_METHOD_NOT_ALLOWED};

/// Where the endpoint answers when `MetricsConfig::path` is empty:
/// the path Prometheus scrapes by default.
pub const DEFAULT_METRICS_PATH: &str = "/metrics";

/// The scrape endpoint.
pub struct MetricsConfig {
    /// Where the endpoint answers; empty is [`DEFAULT_METRICS_PATH`].
    /// An absolute, clean URL path.
    pub path: String,
    /// Writes the exposition; sent `GET` and `HEAD` at `path` only.
    pub handler: MethodRouter,
    /// Serve the endpoint on a non-loopback bind. It answers without
    /// authentication, so this is the operator's decision.
    pub allow_remote: bool,
}

impl MetricsConfig {
    /// The endpoint at [`DEFAULT_METRICS_PATH`], loopback only.
    pub fn new(handler: MethodRouter) -> Self {
        Self {
            path: String::new(),
            handler,
            allow_remote: false,
        }
    }

    pub(crate) fn path(&self) -> &str {
        if self.path.is_empty() {
            DEFAULT_METRICS_PATH
        } else {
            &self.path
        }
    }

    pub(crate) fn validate(&self, loopback: bool, addr: &str) -> Result<(), ApiServerError> {
        let p = self.path();
        let clean = p.starts_with('/')
            && !p.contains("//")
            && !p.contains('?')
            && !p.contains('#')
            && !p.split('/').any(|s| s == "." || s == "..");
        if !clean {
            return Err(ApiServerError(format!(
                "metrics path: {p:?} is not an absolute, clean URL path"
            )));
        }
        if !loopback && !self.allow_remote {
            return Err(ApiServerError(format!(
                "metrics endpoint: {addr:?} is not a loopback address and the endpoint answers \
                 without authentication; listen on 127.0.0.1, or set allow_remote to serve it \
                 beyond loopback"
            )));
        }
        Ok(())
    }
}

#[derive(Clone)]
pub(crate) struct MetricsState {
    pub(crate) path: String,
    pub(crate) handler: Router,
}

pub(crate) async fn metrics(State(st): State<MetricsState>, req: Request, next: Next) -> Response {
    if req.uri().path() != st.path {
        return next.run(req).await;
    }
    if req.method() != Method::GET && req.method() != Method::HEAD {
        let mut resp = ApiError::new(
            405,
            CODE_METHOD_NOT_ALLOWED,
            "the metrics endpoint answers GET and HEAD",
        )
        .into_response();
        resp.headers_mut()
            .insert(header::ALLOW, HeaderValue::from_static("GET, HEAD"));
        return resp;
    }
    let mut resp = match st.handler.oneshot(req).await {
        Ok(r) => r,
        Err(e) => match e {},
    };
    resp.headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    resp
}

impl MetricsState {
    pub(crate) fn new(cfg: &MetricsConfig) -> Self {
        Self {
            path: cfg.path().to_string(),
            handler: Router::new().fallback_service(cfg.handler.clone()),
        }
    }
}
