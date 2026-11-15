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
