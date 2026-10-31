//! Server-sent events: axum's built-in `Sse`, with the headers a
//! streaming response wants behind proxies.

use std::convert::Infallible;
use std::time::Duration;

use axum::http::{header, HeaderValue};
use axum::response::sse::{Event, KeepAlive, Sse};
use axum::response::{IntoResponse, Response};
use futures_util::Stream;

/// Keep-alive comment interval for [`sse`].
pub const KEEP_ALIVE: Duration = Duration::from_secs(15);

/// A `text/event-stream` response over `events`: a keep-alive comment
/// every [`KEEP_ALIVE`], `Cache-Control: no-store`, and
/// `X-Accel-Buffering: no` so a buffering proxy does not hold the
/// stream. Compression is skipped for event streams, and so is the server's write deadline.
pub fn sse<S>(events: S) -> Response
where
    S: Stream<Item = Result<Event, Infallible>> + Send + 'static,
{
    let mut resp = Sse::new(events)
        .keep_alive(KeepAlive::new().interval(KEEP_ALIVE))
        .into_response();
    let h = resp.headers_mut();
    h.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    h.insert("x-accel-buffering", HeaderValue::from_static("no"));
    resp.extensions_mut().insert(super::LiftWriteDeadline);
    resp
}
