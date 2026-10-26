//! The dialer: Go's `connect` and `dialHandshake` on legacy links.

use std::sync::{Arc, OnceLock};
use std::time::Duration;

use futures_util::SinkExt;
use tokio::net::TcpStream;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::Message;
use tokio_tungstenite::{client_async_tls_with_config, Connector};

use super::adapter::{policy, Inner};
use super::conn::{
    close_raw, read_next, split, ws_config, Link, Read, WsSink, WsSource, STATUS_GOING_AWAY,
    STATUS_INTERNAL, STATUS_NORMAL, STATUS_POLICY,
};
use super::ctx::done;
use super::error::NetworkError;
use super::runtime;
use super::wire::{auth_frame, is_auth_ok};
use crate::bus::context::Context;
use crate::bus::stream::LinkState;
use crate::netpolicy::OfflineError;

/// How long the handshake waits for the peer's first frame: Go's
/// `handshakeTimeout`, on both sides.
pub(crate) const HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(10);

impl Inner {
    /// Dials `addr`. `from_loop` marks a reconnect, which never
    /// re-registers an address a disconnect forgot.
    pub(crate) async fn connect(
        self: &Arc<Self>,
        ctx: Context,
        addr: String,
        from_loop: bool,
    ) -> Result<(), NetworkError> {
        if self.closed.load(std::sync::atomic::Ordering::SeqCst) {
            return Err(NetworkError::Closed);
        }
        self.learn_policy(&ctx);
        {
            let st = self.state();
            if from_loop && !st.peers.contains_key(&addr) {
                return Ok(());
            }
            if st.conns.contains_key(&addr) {
                return Ok(());
            }
        }

        let (sink, source) = dial(&ctx, &addr).await?;
        if !from_loop {
            self.register(&addr);
        }
        let (sink, source) = match self.handshake(&ctx, sink, source).await {
            Ok(halves) => halves,
            Err(e) => return Err(self.handshake_failed(&addr, e)),
        };

        // Register in one critical section with the checks that decide
        // whether the link may exist at all.
        let mut sink = Some(sink);
        let registered = {
            let mut st = self.state();
            if self.closed.load(std::sync::atomic::Ordering::SeqCst) {
                Err((STATUS_GOING_AWAY, "closing", Err(NetworkError::Closed)))
            } else if !st.peers.contains_key(&addr) {
                // Disconnect forgot the address while we dialed.
                let r = if from_loop {
                    Ok(())
                } else {
                    Err(NetworkError::Disconnected)
                };
                Err((STATUS_NORMAL, "disconnect", r))
            } else if st.conns.contains_key(&addr) {
                // Lost a race with a reconnect.
                Err((STATUS_NORMAL, "duplicate", Ok(())))
            } else {
                let sink = sink.take().expect("sink is taken once");
                let link = Link::new(addr.clone(), true, sink, String::new());
                st.conns.insert(addr.clone(), Arc::clone(&link));
                st.peers.insert(
                    addr,
                    LinkState {
                        connected: true,
                        ..LinkState::default()
                    },
                );
                self.tasks.add();
                Ok(link)
            }
        };
        let link = match registered {
            Ok(link) => link,
            Err((code, reason, result)) => {
                if let Some(sink) = sink {
                    close_raw(sink, source, code, reason).await;
                }
                return result;
            }
        };
        runtime::handle().spawn(Arc::clone(self).read_loop(link, source));
        Ok(())
    }

    /// Sends the auth frame and waits for the ack, when an authenticator
    /// is set.
    async fn handshake(
        &self,
        ctx: &Context,
        mut sink: WsSink,
        mut source: WsSource,
    ) -> Result<(WsSink, WsSource), NetworkError> {
        let Some(auth) = &self.auth else {
            return Ok((sink, source));
        };
        let token = match auth.token() {
            Ok(t) => t,
            Err(e) => {
                close_raw(sink, source, STATUS_INTERNAL, "auth failed").await;
                return Err(NetworkError::Token(e));
            }
        };
        let sent = tokio::select! {
            r = sink.send(Message::text(auth_frame(&token))) => r.map_err(|e| e.to_string()),
            e = done(ctx) => Err(e.to_string()),
        };
        if let Err(e) = sent {
            close_raw(sink, source, STATUS_INTERNAL, "auth write failed").await;
            return Err(NetworkError::Write(e));
        }
        let timeout = self.handshake_timeout();
        let ack = tokio::select! {
            r = read_next(&mut source) => Some(r),
            () = tokio::time::sleep(timeout) => None,
            _ = done(ctx) => None,
        };
        match ack {
            Some(Read::Data(d)) if is_auth_ok(&d) => Ok((sink, source)),
            Some(Read::Data(_)) => {
                close_raw(sink, source, STATUS_POLICY, "auth rejected").await;
                Err(NetworkError::AuthFailed)
            }
            // The acceptor refused us.
            Some(Read::Closed(Some(STATUS_POLICY))) => Err(NetworkError::AuthFailed),
            _ => {
                close_raw(sink, source, STATUS_POLICY, "auth ack timeout").await;
                Err(NetworkError::AckTimeout)
            }
        }
    }

    /// Records a refused handshake on the address and passes the error
    /// through: Go's `handshakeFailed`, without the link events.
    fn handshake_failed(&self, addr: &str, err: NetworkError) -> NetworkError {
        let code = match err {
            NetworkError::AckTimeout => "timeout",
            NetworkError::AuthFailed => "auth",
            _ => return err,
        };
        if let Some(ps) = self.state().peers.get_mut(addr) {
            ps.connected = false;
            ps.code = code.to_string();
        }
        err
    }
}

/// Dials `addr` and completes the WebSocket upgrade, bounded by `ctx`.
/// The policy check runs first, on the address alone.
async fn dial(ctx: &Context, addr: &str) -> Result<(WsSink, WsSource), NetworkError> {
    let url = ws_url(addr);
    let req = url
        .as_str()
        .into_client_request()
        .map_err(|e| NetworkError::Dial(e.to_string()))?;
    let uri = req.uri().clone();
    let secure = match uri.scheme_str() {
        Some("ws") => false,
        Some("wss") => true,
        _ => {
            return Err(NetworkError::Dial(format!(
                "unsupported URL scheme in {addr:?}"
            )))
        }
    };
    let host = uri.host().unwrap_or_default().to_string();
    let port = uri.port_u16().unwrap_or(if secure { 443 } else { 80 });
    let host_port = format!("{host}:{port}");
    if policy(ctx).check_dial("tcp", &host_port).is_err() {
        // Go refuses through its guarded HTTP transport, so the refusal
        // names the upgrade request.
        let scheme = if secure { "https" } else { "http" };
        return Err(NetworkError::Offline(OfflineError {
            method: "GET".to_string(),
            url: format!("{scheme}://{host_port}{}", uri.path()),
        }));
    }
    let connect = async {
        let bare = host.trim_start_matches('[').trim_end_matches(']');
        let tcp = TcpStream::connect((bare, port))
            .await
            .map_err(|e| NetworkError::Dial(e.to_string()))?;
        let connector = if secure {
            Connector::Rustls(tls_config()?)
        } else {
            Connector::Plain
        };
        let (ws, _) = client_async_tls_with_config(req, tcp, Some(ws_config()), Some(connector))
            .await
            .map_err(|e| NetworkError::Dial(e.to_string()))?;
        Ok(split(ws))
    };
    tokio::select! {
        r = connect => r,
        e = done(ctx) => Err(NetworkError::Dial(e.to_string())),
    }
}

/// Maps `http`/`https` to `ws`/`wss`, as coder/websocket's dial does.
fn ws_url(addr: &str) -> String {
    if let Some(rest) = addr.strip_prefix("http://") {
        format!("ws://{rest}")
    } else if let Some(rest) = addr.strip_prefix("https://") {
        format!("wss://{rest}")
    } else {
        addr.to_string()
    }
}

/// The TLS client config: rustls on the ring provider, trusting the
/// platform's roots (Go's default transport does the same).
fn tls_config() -> Result<Arc<rustls::ClientConfig>, NetworkError> {
    static CONFIG: OnceLock<Result<Arc<rustls::ClientConfig>, String>> = OnceLock::new();
    CONFIG
        .get_or_init(|| {
            let mut roots = rustls::RootCertStore::empty();
            for cert in rustls_native_certs::load_native_certs().certs {
                let _ = roots.add(cert);
            }
            let provider = Arc::new(rustls::crypto::ring::default_provider());
            rustls::ClientConfig::builder_with_provider(provider)
                .with_safe_default_protocol_versions()
                .map(|b| Arc::new(b.with_root_certificates(roots).with_no_client_auth()))
                .map_err(|e| e.to_string())
        })
        .clone()
        .map_err(NetworkError::Dial)
}
