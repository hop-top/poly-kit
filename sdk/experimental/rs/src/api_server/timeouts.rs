//! Server timeouts, a port of Go's `ServerTimeouts` and
//! `LiftWriteDeadline`.
//!
//! The header deadline is hyper's; the read and write deadlines are
//! total, not per chunk, so a client that trickles bytes does not hold
//! a connection (tower-http's body timeouts reset on every frame).

use std::io;
use std::time::Duration;

use axum::body::{Body, HttpBody};
use axum::extract::Request;
use axum::http::{header, HeaderValue};
use axum::middleware::Next;
use axum::response::Response;
use futures_util::StreamExt;
use tokio::time::Instant;

/// Default time to read a request's headers: 5 seconds.
pub const DEFAULT_READ_HEADER_TIMEOUT: Duration = Duration::from_secs(5);
/// Default time to read a whole request, body included: 5 seconds.
pub const DEFAULT_READ_TIMEOUT: Duration = Duration::from_secs(5);
/// Default time to write a response: 10 seconds.
pub const DEFAULT_WRITE_TIMEOUT: Duration = Duration::from_secs(10);

/// The server-side timeouts of one listener. A zero field means no
/// timeout, except that a zero `read_header` uses `read`, as in Go.
///
/// Keep-alive idle time shares the header deadline in hyper (the next
/// request's headers are what an idle connection waits for), so there
/// is no separate idle field; with the defaults it equals Go's idle
/// fallback to `read`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ServerTimeouts {
    /// Reading a request's headers.
    pub read_header: Duration,
    /// Reading a whole request, body included.
    pub read: Duration,
    /// Writing a response, from the end of the request's headers.
    /// Responses marked [`LiftWriteDeadline`] are exempt.
    pub write: Duration,
}

impl Default for ServerTimeouts {
    /// The kit defaults: read header 5s, read 5s, write 10s.
    fn default() -> Self {
        Self {
            read_header: DEFAULT_READ_HEADER_TIMEOUT,
            read: DEFAULT_READ_TIMEOUT,
            write: DEFAULT_WRITE_TIMEOUT,
        }
    }
}

impl ServerTimeouts {
    /// No timeout at all.
    pub fn none() -> Self {
        Self {
            read_header: Duration::ZERO,
            read: Duration::ZERO,
            write: Duration::ZERO,
        }
    }

    /// The header deadline in force: `read_header`, else `read`.
    pub(crate) fn header_deadline(&self) -> Option<Duration> {
        let d = if self.read_header.is_zero() {
            self.read
        } else {
            self.read_header
        };
        (!d.is_zero()).then_some(d)
    }
}

/// Marks a response whose body is a stream by design: the write
/// deadline does not cut it. In response extensions; [`sse`](super::sse)
/// sets it, and [`lift_write_deadline`] sets it on a whole route.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct LiftWriteDeadline;

/// Middleware exempting every response `next` writes from the write
/// deadline, for a route whose responses are streams. Use it as
/// `route_layer(axum::middleware::from_fn(lift_write_deadline))`.
pub async fn lift_write_deadline(req: Request, next: Next) -> Response {
    let mut resp = next.run(req).await;
    resp.extensions_mut().insert(LiftWriteDeadline);
    resp
}

fn timed_out(what: &str) -> io::Error {
    io::Error::new(io::ErrorKind::TimedOut, format!("{what} deadline exceeded"))
}

/// `body` that fails with a timeout error once `deadline` passes.
pub(crate) fn with_deadline(body: Body, deadline: Instant, what: &'static str) -> Body {
    let stream = futures_util::stream::unfold(
        (Box::pin(body.into_data_stream()), false),
        move |(mut s, done)| async move {
            if done {
                return None;
            }
            match tokio::time::timeout_at(deadline, s.next()).await {
                Ok(Some(Ok(b))) => Some((Ok(b), (s, false))),
                Ok(Some(Err(e))) => Some((Err(io::Error::other(e)), (s, true))),
                Ok(None) => None,
                Err(_) => Some((Err(timed_out(what)), (s, true))),
            }
        },
    );
    Body::from_stream(stream)
}

/// The request with its body bound to the read deadline.
pub(crate) fn bound_request(req: Request, read: Duration) -> Request {
    if read.is_zero() || req.body().is_end_stream() {
        return req;
    }
    let (parts, body) = req.into_parts();
    let body = with_deadline(body, Instant::now() + read, "read");
    Request::from_parts(parts, body)
}

/// The response with its body bound to the write deadline, unless the
/// handler lifted it. A sized body keeps its `Content-Length`.
pub(crate) fn bound_response(resp: Response, deadline: Instant) -> Response {
    if resp.extensions().get::<LiftWriteDeadline>().is_some() || resp.body().is_end_stream() {
        return resp;
    }
    let (mut parts, body) = resp.into_parts();
    if let Some(n) = body.size_hint().exact() {
        parts
            .headers
            .entry(header::CONTENT_LENGTH)
            .or_insert_with(|| HeaderValue::from(n));
    }
    Response::from_parts(parts, with_deadline(body, deadline, "write"))
}
