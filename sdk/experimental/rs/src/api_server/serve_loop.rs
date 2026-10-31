//! The listener's accept loop: plain TCP or TLS, hyper's auto
//! (http/1.1 and h2) connection builder with upgrades, the server
//! timeouts, and a graceful drain.

use std::future::Future;
use std::io;
use std::net::SocketAddr;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use axum::body::Body;
use axum::extract::{ConnectInfo, Request};
use axum::response::Response;
use axum::Router;
use hyper::body::Incoming;
use hyper::service::service_fn;
use hyper_util::rt::{TokioExecutor, TokioIo, TokioTimer};
use hyper_util::server::conn::auto::Builder;
use hyper_util::server::graceful::GracefulShutdown;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::TcpListener;
use tokio::time::Instant;
use tower::ServiceExt;

use super::ctx::{PeerCertificate, TlsConnection};
use super::timeouts::{bound_request, bound_response, ServerTimeouts};

/// What a connection carries into every request it serves.
#[derive(Clone)]
struct ConnInfo {
    peer: SocketAddr,
    tls: bool,
    cert: Option<PeerCertificate>,
    live: Arc<Liveness>,
}

/// h2 has no header or idle timeout of hyper's own (hyper's header
/// deadline is HTTP/1 only), so the loop keeps one for every
/// connection: no complete request in the header deadline after accept
/// (a stalled preface or headers) or none in flight for that long
/// (idle) ends it.
struct Liveness {
    since: Mutex<tokio::time::Instant>,
    seen: std::sync::atomic::AtomicBool,
    active: AtomicUsize,
}

impl Liveness {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            since: Mutex::new(tokio::time::Instant::now()),
            seen: std::sync::atomic::AtomicBool::new(false),
            active: AtomicUsize::new(0),
        })
    }

    fn touch(&self) {
        *self.since.lock().unwrap_or_else(PoisonError::into_inner) = tokio::time::Instant::now();
    }

    fn expired(&self, d: Duration) -> bool {
        self.active.load(Ordering::SeqCst) == 0
            && self
                .since
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .elapsed()
                > d
    }
}

/// A request in flight, until its response (body included) is dropped.
struct InFlight(Arc<Liveness>);

impl InFlight {
    fn begin(l: &Arc<Liveness>) -> Self {
        l.seen.store(true, Ordering::SeqCst);
        l.active.fetch_add(1, Ordering::SeqCst);
        InFlight(l.clone())
    }
}

impl Drop for InFlight {
    fn drop(&mut self) {
        self.0.touch();
        self.0.active.fetch_sub(1, Ordering::SeqCst);
    }
}

/// A body that keeps its request counted in flight while it streams.
struct Hold {
    inner: Body,
    _in_flight: InFlight,
}

impl axum::body::HttpBody for Hold {
    type Data = axum::body::Bytes;
    type Error = axum::Error;

    fn poll_frame(
        mut self: std::pin::Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<Result<hyper::body::Frame<Self::Data>, Self::Error>>> {
        std::pin::Pin::new(&mut self.inner).poll_frame(cx)
    }

    fn is_end_stream(&self) -> bool {
        self.inner.is_end_stream()
    }

    fn size_hint(&self) -> hyper::body::SizeHint {
        self.inner.size_hint()
    }
}

/// h2 limits: concurrent streams (Go's default), header list size, and
/// pending reset streams against rapid-reset floods.
const H2_MAX_CONCURRENT_STREAMS: u32 = 250;
const H2_MAX_HEADER_LIST_SIZE: u32 = 64 * 1024;
const H2_MAX_PENDING_ACCEPT_RESET: usize = 20;

#[cfg(feature = "api-server-tls")]
pub(crate) type Acceptor = tokio_rustls::TlsAcceptor;
/// Without TLS support there is no acceptor to hold.
#[cfg(not(feature = "api-server-tls"))]
#[derive(Clone)]
pub(crate) enum Acceptor {}

/// Serves `app` on `listener` until `shutdown` resolves, then stops
/// accepting and drains the connections in flight.
pub(crate) async fn serve(
    listener: TcpListener,
    app: Router,
    timeouts: ServerTimeouts,
    tls: Option<Acceptor>,
    shutdown: impl Future<Output = ()>,
) -> Result<(), String> {
    let graceful = GracefulShutdown::new();
    let tls = tls.map(Arc::new);
    tokio::pin!(shutdown);
    loop {
        let (stream, peer) = tokio::select! {
            () = &mut shutdown => break,
            r = listener.accept() => match r {
                Ok(c) => c,
                Err(e) if is_transient(&e) => {
                    tokio::time::sleep(std::time::Duration::from_millis(50)).await;
                    continue;
                }
                Err(e) => return Err(format!("accept: {e}")),
            },
        };
        let _ = stream.set_nodelay(true);
        let app = app.clone();
        let watcher = graceful.watcher();
        let tls = tls.clone();
        tokio::spawn(async move {
            match tls {
                None => {
                    let info = ConnInfo {
                        peer,
                        tls: false,
                        cert: None,
                        live: Liveness::new(),
                    };
                    run_conn(TokioIo::new(stream), info, app, timeouts, watcher).await;
                }
                #[cfg(feature = "api-server-tls")]
                Some(acceptor) => {
                    let hs = acceptor.accept(stream);
                    let done = match timeouts.header_deadline() {
                        Some(d) => tokio::time::timeout(d, hs).await.ok(),
                        None => Some(hs.await),
                    };
                    // A failed handshake has no request to answer or
                    // attribute; the connection is just dropped.
                    let Some(Ok(tls_stream)) = done else { return };
                    let cert = tls_stream
                        .get_ref()
                        .1
                        .peer_certificates()
                        .and_then(|c| c.first())
                        .map(|c| PeerCertificate(Arc::from(c.as_ref())));
                    let info = ConnInfo {
                        peer,
                        tls: true,
                        cert,
                        live: Liveness::new(),
                    };
                    run_conn(TokioIo::new(tls_stream), info, app, timeouts, watcher).await;
                }
                #[cfg(not(feature = "api-server-tls"))]
                Some(never) => match *never {},
            }
        });
    }
    drop(listener);
    graceful.shutdown().await;
    Ok(())
}

/// Errors accept reports for one connection, not the listener.
fn is_transient(e: &io::Error) -> bool {
    use io::ErrorKind::*;
    matches!(
        e.kind(),
        ConnectionAborted | ConnectionReset | Interrupted | WouldBlock | TimedOut
    ) || matches!(e.raw_os_error(), Some(23 | 24 | 105))
}

async fn run_conn<I>(
    io: TokioIo<I>,
    info: ConnInfo,
    app: Router,
    timeouts: ServerTimeouts,
    watcher: hyper_util::server::graceful::Watcher,
) where
    I: AsyncRead + AsyncWrite + Unpin + Send + 'static,
{
    let live = info.live.clone();
    let svc = service_fn(move |req: hyper::Request<Incoming>| {
        let app = app.clone();
        let info = info.clone();
        async move { handle(app, info, timeouts, req).await }
    });
    let mut builder = Builder::new(TokioExecutor::new());
    builder
        .http1()
        .timer(TokioTimer::new())
        .header_read_timeout(timeouts.header_deadline());
    let probe = timeouts.write.max(Duration::from_secs(1));
    builder
        .http2()
        .timer(TokioTimer::new())
        .max_concurrent_streams(H2_MAX_CONCURRENT_STREAMS)
        .max_header_list_size(H2_MAX_HEADER_LIST_SIZE)
        .max_pending_accept_reset_streams(H2_MAX_PENDING_ACCEPT_RESET)
        .keep_alive_interval(probe * 2)
        .keep_alive_timeout(probe);
    // The result is the connection's end, clean or not: nothing to do
    // with either.
    let conn = watcher.watch(builder.serve_connection_with_upgrades(io, svc));
    let Some(d) = timeouts.header_deadline() else {
        let _ = conn.await;
        return;
    };
    tokio::pin!(conn);
    let mut tick = tokio::time::interval((d / 4).max(Duration::from_millis(10)));
    loop {
        tokio::select! {
            _ = &mut conn => return,
            _ = tick.tick() => if live.expired(d) { return },
        }
    }
}

/// One request: the connection's facts in extensions, the read
/// deadline on the body, the write deadline on the answer. A write
/// deadline that passes aborts the connection, as Go's does.
async fn handle(
    app: Router,
    info: ConnInfo,
    timeouts: ServerTimeouts,
    req: hyper::Request<Incoming>,
) -> Result<Response, io::Error> {
    let in_flight = InFlight::begin(&info.live);
    let mut req: Request = req.map(Body::new);
    let ext = req.extensions_mut();
    ext.insert(ConnectInfo(info.peer));
    if info.tls {
        ext.insert(TlsConnection);
    }
    if let Some(c) = info.cert {
        ext.insert(c);
    }
    let req = bound_request(req, timeouts.read);

    let deadline = (!timeouts.write.is_zero()).then(|| Instant::now() + timeouts.write);
    let fut = app.oneshot(req);
    let resp = match deadline {
        None => fut.await,
        Some(d) => tokio::time::timeout_at(d, fut)
            .await
            .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "write deadline exceeded"))?,
    };
    let Ok(resp) = resp;
    let resp = match deadline {
        Some(d) => bound_response(resp, d),
        None => resp,
    };
    // Held until hyper drops the body: after it is streamed, not when
    // the headers go out.
    let (parts, body) = resp.into_parts();
    Ok(Response::from_parts(
        parts,
        Body::new(Hold {
            inner: body,
            _in_flight: in_flight,
        }),
    ))
}
