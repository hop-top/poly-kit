//! The access log, through `redact`: one line per request, the whole
//! line passed through the shared redactor before it is written.

use std::sync::Arc;
use std::time::Instant;

use axum::extract::{Request, State};
use axum::middleware::Next;
use axum::response::Response;
use tower_http::request_id::RequestId;

/// Where log lines go. The line is already redacted.
pub type LogSink = Arc<dyn Fn(&str) + Send + Sync>;

/// A sink that writes to stderr.
pub fn stderr_sink() -> LogSink {
    Arc::new(|line| eprintln!("{line}"))
}

fn request_id(req: &Request) -> String {
    req.extensions()
        .get::<RequestId>()
        .and_then(|r| r.header_value().to_str().ok())
        .unwrap_or("")
        .to_string()
}

/// The access-log line for one request: Go's `http request` message
/// and fields, plus the request id.
pub fn access_line(method: &str, path: &str, status: u16, ms: u128, request_id: &str) -> String {
    let mut line = format!(
        "msg=\"http request\" method={method} path={path:?} status={status} duration={ms}ms"
    );
    if !request_id.is_empty() {
        line.push_str(&format!(" request_id={request_id}"));
    }
    crate::redact::shared().apply(&line).into_owned()
}

pub(crate) async fn access_log(State(sink): State<LogSink>, req: Request, next: Next) -> Response {
    let start = Instant::now();
    let method = req.method().to_string();
    let path = req.uri().path().to_string();
    let id = request_id(&req);
    let resp = next.run(req).await;
    sink(&access_line(
        &method,
        &path,
        resp.status().as_u16(),
        start.elapsed().as_millis(),
        &id,
    ));
    resp
}

/// Logs a recovered panic through the same sink and redactor.
pub(crate) fn panic_line(what: &str) -> String {
    crate::redact::shared()
        .apply(&format!("msg=\"panic recovered\" error={what:?}"))
        .into_owned()
}
