//! `/healthz` and `/readyz`, a port of Go's `HealthRoutes`. Answered in
//! front of the router, inside the request id, access log, recovery
//! and security header layers and before the Host and Origin checks,
//! the body limit and authentication: orchestrator probes address a pod
//! by IP and carry no credentials.

use std::sync::Arc;

use axum::body::Body;
use axum::extract::{Request, State};
use axum::http::{header, HeaderValue, Method, StatusCode};
use axum::middleware::Next;
use axum::response::Response;
use serde::Serialize;

use super::error::{json_response, ApiError, CODE_METHOD_NOT_ALLOWED};

/// The liveness path.
pub const LIVENESS_PATH: &str = "/healthz";
/// The readiness path.
pub const READINESS_PATH: &str = "/readyz";
/// A passing status.
pub const STATUS_OK: &str = "ok";
/// A failing status.
pub const STATUS_UNAVAILABLE: &str = "unavailable";

/// One thing readiness depends on.
#[derive(Clone)]
pub struct ReadinessCheck {
    /// Named in the failing list when `detail` is on.
    pub name: String,
    /// Whether the dependency is ready.
    pub ready: Arc<dyn Fn() -> bool + Send + Sync>,
}

impl ReadinessCheck {
    /// A check named `name`.
    pub fn new(name: impl Into<String>, ready: impl Fn() -> bool + Send + Sync + 'static) -> Self {
        Self {
            name: name.into(),
            ready: Arc::new(ready),
        }
    }
}

/// The body of a health answer.
#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
pub struct HealthStatus {
    /// `ok` or `unavailable`.
    pub status: String,
    /// The failing checks, only when detail is on.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub failing: Vec<String>,
}

#[derive(Clone)]
pub(crate) struct HealthState {
    pub(crate) prefix: String,
    pub(crate) checks: Vec<ReadinessCheck>,
    pub(crate) detail: bool,
}

impl HealthState {
    fn readiness(&self) -> HealthStatus {
        let failing: Vec<String> = self
            .checks
            .iter()
            .filter(|c| !(c.ready)())
            .map(|c| c.name.clone())
            .collect();
        if failing.is_empty() {
            return HealthStatus {
                status: STATUS_OK.into(),
                failing,
            };
        }
        HealthStatus {
            status: STATUS_UNAVAILABLE.into(),
            failing: if self.detail { failing } else { Vec::new() },
        }
    }
}

pub(crate) async fn health(State(st): State<HealthState>, req: Request, next: Next) -> Response {
    let prefix = st.prefix.trim_end_matches('/');
    let path = req.uri().path();
    let status = if path == format!("{prefix}{LIVENESS_PATH}") {
        HealthStatus {
            status: STATUS_OK.into(),
            failing: Vec::new(),
        }
    } else if path == format!("{prefix}{READINESS_PATH}") {
        st.readiness()
    } else {
        return next.run(req).await;
    };
    if req.method() != Method::GET && req.method() != Method::HEAD {
        let mut resp = ApiError::new(
            405,
            CODE_METHOD_NOT_ALLOWED,
            "health routes answer GET and HEAD",
        )
        .into_response();
        resp.headers_mut()
            .insert(header::ALLOW, HeaderValue::from_static("GET, HEAD"));
        return resp;
    }
    let code = if status.status == STATUS_OK { 200 } else { 503 };
    let mut resp = if req.method() == Method::HEAD {
        let mut r = Response::new(Body::empty());
        *r.status_mut() = StatusCode::from_u16(code).unwrap_or(StatusCode::OK);
        r.headers_mut().insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json"),
        );
        r
    } else {
        json_response(code, &status)
    };
    resp.headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    resp
}
