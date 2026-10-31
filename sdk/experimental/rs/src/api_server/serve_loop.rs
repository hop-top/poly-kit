//! The listener's accept loop: plain TCP or TLS, hyper's auto
//! (http/1.1 and h2) connection builder with upgrades, the server
//! timeouts, and a graceful drain.

use std::future::Future;
use std::io;
use std::net::SocketAddr;
use std::sync::Arc;

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
}

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
    builder.http2().timer(TokioTimer::new());
    // The result is the connection's end, clean or not: nothing to do
    // with either.
    let _ = watcher
        .watch(builder.serve_connection_with_upgrades(io, svc))
        .await;
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
    Ok(match deadline {
        Some(d) => bound_response(resp, d),
        None => resp,
    })
}
