/// Failure talking to, or starting, the engine.
#[derive(Debug, thiserror::Error)]
pub enum EngineError {
    /// Transport failure (connect, timeout, body decode).
    #[error("http: {0}")]
    Http(#[from] reqwest::Error),
    /// Spawning the engine or reading its output failed.
    #[error("io: {0}")]
    Io(#[from] std::io::Error),
    /// A body failed to (de)serialize.
    #[error("json: {0}")]
    Json(#[from] serde_json::Error),
    /// The engine did not come up: bad startup line, early exit, failed
    /// health check.
    #[error("startup: {0}")]
    Startup(String),
    /// Non-2xx reply.
    #[error("engine returned {status}: {message}")]
    Status { status: u16, message: String },
    /// 409 from `revert`: the requested version does not exist.
    #[error("version conflict: {0}")]
    VersionConflict(String),
}

/// Builds `base/seg0/seg1/...` with each segment percent-encoded. An empty
/// final segment yields a trailing slash (significant on collection routes).
pub(crate) fn url(base: &str, segs: &[&str]) -> Result<reqwest::Url, EngineError> {
    let mut u = reqwest::Url::parse(base)
        .map_err(|e| EngineError::Startup(format!("bad base url {base}: {e}")))?;
    u.path_segments_mut()
        .map_err(|()| EngineError::Startup(format!("base url {base} cannot be a base")))?
        .clear()
        .extend(segs);
    Ok(u)
}

/// Passes 2xx replies through; maps anything else to `EngineError::Status`,
/// preferring the error envelope's `message` over the raw body.
pub(crate) async fn check(resp: reqwest::Response) -> Result<reqwest::Response, EngineError> {
    let status = resp.status();
    if status.is_success() {
        return Ok(resp);
    }
    let body = resp.text().await.unwrap_or_default();
    let message = serde_json::from_str::<serde_json::Value>(&body)
        .ok()
        .and_then(|v| v.get("message").and_then(|m| m.as_str()).map(str::to_owned))
        .unwrap_or(body);
    Err(EngineError::Status {
        status: status.as_u16(),
        message,
    })
}

/// Adds `Authorization: Bearer` when a token is set.
pub(crate) fn bearer(rb: reqwest::RequestBuilder, token: Option<&str>) -> reqwest::RequestBuilder {
    match token {
        Some(t) if !t.is_empty() => rb.bearer_auth(t),
        _ => rb,
    }
}
