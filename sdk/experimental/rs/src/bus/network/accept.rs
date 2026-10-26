//! The acceptor: Go's `Handler` after the upgrade, plus a bare TCP
//! listener and the framework-agnostic entry point.

use std::io;
use std::net::{SocketAddr, TcpListener as StdListener};
use std::sync::atomic::Ordering;
use std::sync::Arc;

use futures_util::SinkExt;
use tokio::io::{AsyncRead, AsyncWrite};
use tokio::net::{TcpListener, TcpStream};
use tokio_tungstenite::tungstenite::protocol::Role;
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::{accept_async_with_config, WebSocketStream};

use super::adapter::{Inner, NetworkAdapter};
use super::conn::{
    close_raw, read_next, split, ws_config, Link, Read, STATUS_GOING_AWAY, STATUS_INTERNAL,
    STATUS_POLICY,
};
use super::runtime;
use super::wire::{parse_auth_frame, AUTH_OK};

impl NetworkAdapter {
    /// Accepts peers on `addr` (`host:port`; port 0 picks one) and
    /// returns the bound address. Any request path upgrades, as Go's
    /// handler does wherever it is mounted. The listener stops on
    /// [`close`](Self::close).
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

    /// Takes over a connection a framework already upgraded: hand the
    /// raw I/O here after the `101` response.
    /// `remote_addr` keys the link, as Go's `r.RemoteAddr` does. Runs the
    /// auth handshake and the reader on the network runtime; returns at
    /// once.
    pub fn accept_upgraded<S>(&self, io: S, remote_addr: impl Into<String>)
    where
        S: AsyncRead + AsyncWrite + Unpin + Send + 'static,
    {
        let inner = Arc::clone(&self.0);
        let addr = remote_addr.into();
        runtime::handle().spawn(async move {
            let ws = WebSocketStream::from_raw_socket(io, Role::Server, Some(ws_config())).await;
            inner.serve_ws(ws, addr).await;
        });
    }
}

impl Inner {
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
            runtime::handle().spawn(async move {
                tokio::select! {
                    () = inner.accept_tcp(tcp, peer) => {}
                    () = inner.closing.wait() => {}
                }
            });
        }
    }

    async fn accept_tcp(self: &Arc<Self>, tcp: TcpStream, peer: SocketAddr) {
        if let Ok(ws) = accept_async_with_config(tcp, Some(ws_config())).await {
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
            if auth.verify(&hello.token).is_err() {
                close_raw(sink, source, STATUS_POLICY, "auth failed").await;
                return;
            }
            origin = hello.origin;
            if sink.send(Message::text(AUTH_OK)).await.is_err() {
                close_raw(sink, source, STATUS_INTERNAL, "ack write failed").await;
                return;
            }
        }
        let mut sink = Some(sink);
        let link = {
            let mut st = self.state();
            if self.closed.load(Ordering::SeqCst) {
                None
            } else {
                let sink = sink.take().expect("sink is taken once");
                let link = Link::new(addr.clone(), false, sink, origin);
                st.conns.insert(addr, Arc::clone(&link));
                self.tasks.add();
                Some(link)
            }
        };
        let Some(link) = link else {
            if let Some(sink) = sink {
                close_raw(sink, source, STATUS_GOING_AWAY, "closing").await;
            }
            return;
        };
        runtime::handle().spawn(Arc::clone(self).read_loop(link, source));
    }
}
