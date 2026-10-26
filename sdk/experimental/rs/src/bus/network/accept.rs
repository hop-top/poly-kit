//! The acceptor: Go's `Handler` after the upgrade, plus a bare TCP
//! listener and the framework-agnostic entry point.

use std::collections::hash_map::Entry;
use std::io;
use std::net::{SocketAddr, TcpListener as StdListener};
use std::sync::atomic::Ordering;
use std::sync::Arc;

use futures_util::SinkExt;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::{TcpListener, TcpStream};
use tokio_tungstenite::tungstenite::handshake::server::{ErrorResponse, Request, Response};
use tokio_tungstenite::tungstenite::protocol::Role;
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::{accept_hdr_async_with_config, WebSocketStream};

use super::adapter::{Inner, NetworkAdapter};
use super::conn::{
    close_raw, read_next, split, ws_config, Link, Read, STATUS_GOING_AWAY, STATUS_INTERNAL,
    STATUS_NORMAL, STATUS_POLICY, WRITE_TIMEOUT,
};
use super::runtime;
use super::upgrade::check_upgrade;
use super::wire::{parse_auth_frame, AUTH_OK};

impl NetworkAdapter {
    /// Accepts peers on `addr` (`host:port`; port 0 picks one) and
    /// returns the bound address. Any request path upgrades, as Go's
    /// handler does wherever it is mounted; requests failing
    /// [`check_upgrade`] are refused, and so is an upgrade not done within
    /// the 10 s handshake bound. Links are keyed by the peer's socket
    /// address. The listener stops on [`close`](Self::close).
    ///
    /// The Origin check and the later checks answer with
    /// coder/websocket's status and text. A request tungstenite itself
    /// rejects first (not `GET`, a missing upgrade header, a version
    /// other than 13, a missing key) gets no HTTP response: the
    /// connection is dropped. Serve through [`check_upgrade`] and
    /// [`accept_upgraded`](Self::accept_upgraded), or the axum handler,
    /// when those clients need the exact response.
    ///
    /// # Panics
    ///
    /// When called on the network runtime.
    pub fn listen(&self, addr: &str) -> io::Result<SocketAddr> {
        let std = StdListener::bind(addr)?;
        std.set_nonblocking(true)?;
        let local = std.local_addr()?;
        let inner = Arc::clone(&self.0);
        let listener = runtime::block_on("listen", async move {
            TcpListener::from_std(std).map(|l| (l, inner))
        });
        let (listener, inner) = listener?;
        inner.tasks.add();
        runtime::handle().spawn(async move {
            inner.accept_loop(listener).await;
            inner.tasks.done();
        });
        Ok(local)
    }

    /// Takes over a connection a framework already upgraded: send
    /// [`Upgrade::response`](super::Upgrade::response) for a request that
    /// passed [`check_upgrade`], then hand the raw I/O here.
    /// `remote_addr` keys the link, as Go's `r.RemoteAddr` does. Runs the
    /// auth handshake and the reader on the network runtime; returns at
    /// once.
    ///
    /// A connection whose `remote_addr` is already linked is refused with
    /// 1000 `duplicate`, as a duplicate outbound link is, so every link
    /// stays reachable by close. Behind a proxy that reports one address
    /// for many clients (a rewritten `RemoteAddr`, a missing
    /// `ConnectInfo`), pass the client's own address and port, or only the
    /// first client from that address links.
    pub fn accept_upgraded<S>(&self, io: S, remote_addr: impl Into<String>)
    where
        S: AsyncRead + AsyncWrite + Unpin + Send + 'static,
    {
        let inner = Arc::clone(&self.0);
        let addr = remote_addr.into();
        self.0.spawn_tracked(async move {
            let ws = WebSocketStream::from_raw_socket(io, Role::Server, Some(ws_config())).await;
            inner.serve_ws(ws, addr).await;
        });
    }
}

impl Inner {
    /// Runs one accept-side session (upgrade, auth, registration) as an
    /// adapter task: close stops it at its next await and waits for it,
    /// so no handshake, verify or ack happens after close returns. Not
    /// started once the adapter is closed. The link it registers has its
    /// own task.
    pub(crate) fn spawn_tracked<F>(self: &Arc<Self>, session: F)
    where
        F: std::future::Future<Output = ()> + Send + 'static,
    {
        {
            let _st = self.state();
            if self.closed.load(Ordering::SeqCst) {
                return;
            }
            self.tasks.add();
        }
        let inner = Arc::clone(self);
        runtime::handle().spawn(async move {
            tokio::select! {
                () = session => {}
                () = inner.closing.wait() => {}
            }
            inner.tasks.done();
        });
    }

    async fn accept_loop(self: &Arc<Self>, listener: TcpListener) {
        loop {
            let accepted = tokio::select! {
                () = self.closing.wait() => return,
                r = listener.accept() => r,
            };
            let Ok((tcp, peer)) = accepted else {
                continue;
            };
            let inner = Arc::clone(self);
            self.spawn_tracked(async move { inner.accept_tcp(tcp, peer).await });
        }
    }

    async fn accept_tcp(self: &Arc<Self>, tcp: TcpStream, peer: SocketAddr) {
        // Bound the HTTP upgrade as the auth read is: a client that never
        // finishes it is dropped.
        let upgrade = accept_hdr_async_with_config(tcp, upgrade_callback, Some(ws_config()));
        if let Ok(Ok(ws)) = tokio::time::timeout(self.handshake_timeout(), upgrade).await {
            self.serve_ws(ws, peer.to_string()).await;
        }
    }

    /// Go's handler after `websocket.Accept`: the auth handshake, then
    /// registration and the reader.
    pub(crate) async fn serve_ws<S>(self: &Arc<Self>, ws: WebSocketStream<S>, addr: String)
    where
        S: AsyncRead + AsyncWrite + Unpin + Send + 'static,
    {
        let (mut sink, mut source) = split(ws);
        let mut origin = String::new();
        if let Some(auth) = &self.auth {
            let first = tokio::select! {
                r = read_next(&mut source) => Some(r),
                () = tokio::time::sleep(self.handshake_timeout()) => None,
            };
            let Some(Read::Data(msg)) = first else {
                close_raw(sink, source, STATUS_POLICY, "auth required").await;
                return;
            };
            let Some(hello) = parse_auth_frame(&msg) else {
                close_raw(sink, source, STATUS_POLICY, "invalid auth").await;
                return;
            };
            // The authenticator is caller code and may block: keep it off
            // the network thread.
            let (a, token) = (Arc::clone(auth), hello.token.clone());
            let verified = tokio::task::spawn_blocking(move || a.verify(&token).is_ok())
                .await
                .unwrap_or(false);
            if !verified {
                close_raw(sink, source, STATUS_POLICY, "auth failed").await;
                return;
            }
            origin = hello.origin;
            let ack = tokio::time::timeout(WRITE_TIMEOUT, sink.send(Message::text(AUTH_OK)));
            if !matches!(ack.await, Ok(Ok(()))) {
                close_raw(sink, source, STATUS_INTERNAL, "ack write failed").await;
                return;
            }
        }
        let mut sink = Some(sink);
        let link = {
            let mut st = self.state();
            match st.conns.entry(addr) {
                _ if self.closed.load(Ordering::SeqCst) => Err((STATUS_GOING_AWAY, "closing")),
                // Another link holds this address (a caller-supplied or
                // rewritten one): refuse, as a duplicate outbound link is,
                // so no link escapes close.
                Entry::Occupied(_) => Err((STATUS_NORMAL, "duplicate")),
                Entry::Vacant(slot) => {
                    let sink = sink.take().expect("sink is taken once");
                    let link = Link::new(slot.key().clone(), false, sink, origin);
                    slot.insert(Arc::clone(&link));
                    self.tasks.add();
                    Ok(link)
                }
            }
        };
        let link = match link {
            Ok(link) => link,
            Err((code, reason)) => {
                if let Some(sink) = sink {
                    close_raw(sink, source, code, reason).await;
                }
                return;
            }
        };
        runtime::handle().spawn(Arc::clone(self).read_loop(link, source));
    }
}

/// The upgrade checks as tungstenite's handshake callback.
// The callback's signature is tungstenite's; its error is a response.
#[allow(clippy::result_large_err)]
fn upgrade_callback(req: &Request, resp: Response) -> Result<Response, ErrorResponse> {
    match check_upgrade(req) {
        Ok(_) => Ok(resp),
        Err(r) => Err(r.response().map(Some)),
    }
}
