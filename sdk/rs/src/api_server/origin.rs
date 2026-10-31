//! Cross-origin protection, a port of Go's `OriginCheck`, which wraps
//! `net/http.CrossOriginProtection`: an unsafe request from another
//! origin is refused, judged by `Sec-Fetch-Site`, else by `Origin`
//! against `Host`. Requests with neither header are not browsers.

use std::collections::HashSet;

use axum::extract::Request;
use axum::extract::State;
use axum::http::Method;
use axum::middleware::Next;
use axum::response::Response;

use super::ctx::host_of;
use super::error::{refuse, ApiError, ApiServerError, RefusalWriter, CODE_ORIGIN_REJECTED};

/// What the origin check is configured with.
#[derive(Clone, Default)]
pub struct OriginCheckConfig {
    /// Trusted origins (`scheme://host[:port]`), or `*` to trust all.
    pub allow: Vec<String>,
    /// Answers a refusal in the transport's own envelope.
    pub refuse: Option<RefusalWriter>,
}

#[derive(Clone)]
pub(crate) struct OriginPolicy {
    trusted: HashSet<String>,
    refuse: Option<RefusalWriter>,
}

/// `scheme://host[:port]` and nothing else; returns the host part.
fn parse_origin(origin: &str) -> Result<&str, String> {
    let bad = |why: &str| format!("invalid origin {origin:?}: {why}");
    let (scheme, rest) = origin
        .split_once("://")
        .ok_or_else(|| bad("scheme is required"))?;
    if scheme.is_empty()
        || !scheme
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'+' | b'-' | b'.'))
    {
        return Err(bad("scheme is required"));
    }
    if rest.is_empty() {
        return Err(bad("host is required"));
    }
    if rest.contains(['/', '?', '#']) {
        return Err(bad("path, query, and fragment are not allowed"));
    }
    Ok(rest)
}

impl OriginPolicy {
    /// `Ok(None)`: `*`, the check is off.
    pub(crate) fn new(cfg: &OriginCheckConfig) -> Result<Option<Self>, ApiServerError> {
        let mut trusted = HashSet::new();
        for o in &cfg.allow {
            let o = o.trim();
            if o == "*" {
                return Ok(None);
            }
            parse_origin(o).map_err(|e| ApiServerError(format!("allowed origin {o:?}: {e}")))?;
            trusted.insert(o.to_string());
        }
        Ok(Some(Self {
            trusted,
            refuse: cfg.refuse.clone(),
        }))
    }

    fn exempt(&self, req: &Request) -> bool {
        req.headers()
            .get("origin")
            .and_then(|v| v.to_str().ok())
            .is_some_and(|o| !o.is_empty() && self.trusted.contains(o))
    }

    fn permits(&self, req: &Request) -> bool {
        if matches!(*req.method(), Method::GET | Method::HEAD | Method::OPTIONS) {
            return true;
        }
        let sec_fetch = req
            .headers()
            .get("sec-fetch-site")
            .and_then(|v| v.to_str().ok())
            .unwrap_or("");
        match sec_fetch {
            "" => {}
            "same-origin" | "none" => return true,
            _ => return self.exempt(req),
        }
        let origin = req
            .headers()
            .get("origin")
            .and_then(|v| v.to_str().ok())
            .unwrap_or("");
        if origin.is_empty() {
            return true;
        }
        if parse_origin(origin).is_ok_and(|h| h == host_of(req)) {
            return true;
        }
        self.exempt(req)
    }
}

pub(crate) async fn origin_check(
    State(policy): State<OriginPolicy>,
    req: Request,
    next: Next,
) -> Response {
    if policy.permits(&req) {
        return next.run(req).await;
    }
    let origin = match req.headers().get("origin").and_then(|v| v.to_str().ok()) {
        Some(o) if !o.is_empty() => o.to_string(),
        _ => format!(
            "Sec-Fetch-Site: {}",
            req.headers()
                .get("sec-fetch-site")
                .and_then(|v| v.to_str().ok())
                .unwrap_or("")
        ),
    };
    refuse(
        &policy.refuse,
        ApiError::new(
            403,
            CODE_ORIGIN_REJECTED,
            format!("cross-origin request from {origin:?} refused"),
        ),
    )
}
