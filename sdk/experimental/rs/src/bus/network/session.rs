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
use crate::bus::stream::{
    LinkResume, LinkState, REASON_UNSUPPORTED, TOPIC_LINK_CONNECTED, TOPIC_STREAM_INVALIDATED,
};
use crate::netpolicy::OfflineError;
use serde_json::json;

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

        let (sink, source) = dial(&ctx, &addr, self.tls.clone()).await?;
        if !from_loop {
            self.register(&addr);
        }
        let (sink, source) = match self.handshake(&ctx, sink, source).await {
            Ok(halves) => halves,
            Err(e) => return Err(self.handshake_failed(&addr, e).await),
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
                // A fresh state also clears Final and the code of an
                // earlier refused handshake.
                let peer = st.peers.entry(addr).or_default();
                let reconnect = std::mem::replace(&mut peer.connected_once, true);
                peer.state = LinkState {
                    connected: true,
                    resume: Some(LinkResume::Unsupported),
                    ..LinkState::default()
                };
                self.tasks.add();
                Ok((link, reconnect))
            }
        };
        let (link, reconnect) = match registered {
            Ok(registered) => registered,
            Err((code, reason, result)) => {
                if let Some(sink) = sink {
                    close_raw(sink, source, code, reason).await;
                }
                return result;
            }
        };
        // Go's publishConnected, then the gap a link without positions
        // always leaves on a reconnect, then the reader. A task of its
        // own: the link is registered and counted, so this must run to
        // the end even if the caller's future is dropped (a reconnect
        // interrupted by close). Connect returns once the events are out.
        let inner = Arc::clone(self);
        let (published_tx, published) = tokio::sync::oneshot::channel();
        runtime::handle().spawn(async move {
            let addr = link.addr.clone();
            inner
                .publish_local(
                    TOPIC_LINK_CONNECTED,
                    json!({"peer": addr, "stream": "", "resume": LinkResume::Unsupported.as_str(), "format": "kit"}),
                )
                .await;
            if reconnect {
                inner
                    .publish_local(
                        TOPIC_STREAM_INVALIDATED,
                        json!({"peer": addr, "reason": REASON_UNSUPPORTED, "stream": "", "after": "", "head": ""}),
                    )
                    .await;
            }
            let _ = published_tx.send(());
            inner.read_loop(link, source).await;
        });
        let _ = published.await;
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
        // The authenticator is caller code and may block: keep it off the
        // network thread.
        let a = Arc::clone(auth);
        let token = tokio::task::spawn_blocking(move || a.token())
            .await
            .unwrap_or_else(|_| Err("bus: network: authenticator panicked".into()));
        let token = match token {
            Ok(t) => t,
            Err(e) => {
                close_raw(sink, source, STATUS_INTERNAL, "auth failed").await;
                return Err(NetworkError::Token(Arc::from(e)));
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

    /// Reports a failed handshake, records it on the address and passes
    /// the error through: Go's `handshakeFailed`. A refused token is
    /// `auth`, a missing ack `timeout`; neither is final on these links.
    /// `link.disconnected` goes out once per change of code, so a
    /// retrying address does not flood the bus.
    async fn handshake_failed(&self, addr: &str, err: NetworkError) -> NetworkError {
        self.report_err(NetworkError::Handshake {
            addr: addr.to_string(),
            source: Box::new(err.clone()),
        })
        .await;
        let code = match err {
            NetworkError::AckTimeout => "timeout",
            NetworkError::AuthFailed => "auth",
            _ => return err,
        };
        let publish = {
            let mut st = self.state();
            // A peer disconnect forgot meanwhile still gets its event, as
            // Go's handshake keeps its peer state.
            match st.peers.get_mut(addr) {
                None => true,
                Some(p) => {
                    let repeat = !p.state.connected && p.state.code == code && !p.state.is_final;
                    p.state.connected = false;
                    p.state.code = code.to_string();
                    p.state.is_final = false;
                    !repeat
                }
            }
        };
        if publish {
            self.publish_disconnected(addr, false, code).await;
        }
        err
    }
}

/// Dials `addr` and completes the WebSocket upgrade, bounded by `ctx`.
/// The policy check runs first, on the address alone.
async fn dial(
    ctx: &Context,
    addr: &str,
    tls: Option<Arc<rustls::ClientConfig>>,
) -> Result<(WsSink, WsSource), NetworkError> {
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
            Connector::Rustls(match tls {
                Some(c) => c,
                None => tls_config()?,
            })
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
/// platform's roots (Go's default transport does the same), plus
/// Mozilla's with feature `bus-network-webpki-roots`.
fn tls_config() -> Result<Arc<rustls::ClientConfig>, NetworkError> {
    static CONFIG: OnceLock<Result<Arc<rustls::ClientConfig>, String>> = OnceLock::new();
    CONFIG
        .get_or_init(|| {
            let provider = Arc::new(rustls::crypto::ring::default_provider());
            rustls::ClientConfig::builder_with_provider(provider)
                .with_safe_default_protocol_versions()
                .map(|b| {
                    Arc::new(
                        b.with_root_certificates(default_roots())
                            .with_no_client_auth(),
                    )
                })
                .map_err(|e| e.to_string())
        })
        .clone()
        .map_err(NetworkError::Dial)
}

/// The default root store.
pub(crate) fn default_roots() -> rustls::RootCertStore {
    let mut roots = rustls::RootCertStore::empty();
    for cert in rustls_native_certs::load_native_certs().certs {
        let _ = roots.add(cert);
    }
    #[cfg(feature = "bus-network-webpki-roots")]
    roots.extend(webpki_roots::TLS_SERVER_ROOTS.iter().cloned());
    roots
}
