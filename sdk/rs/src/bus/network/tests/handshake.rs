//! The handshake bounds and refusals, against raw peers.

use std::net::TcpListener;
use std::sync::{mpsc, PoisonError};
use std::thread;
use std::time::{Duration, Instant};

use super::{close, within};
use crate::bus::context::Context;
use crate::bus::network::{NetworkAdapter, NetworkError, NetworkOptions, StaticTokenAuth};
use crate::bus::DynBus;

/// The acceptor waits a bounded time for the auth frame, then closes
/// with 1008 `auth required`.
#[test]
fn acceptor_times_out_a_silent_dialer() {
    use futures_util::StreamExt;
    use tokio_tungstenite::tungstenite::Message;

    let hub = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().auth(StaticTokenAuth::new("s")),
    );
    *hub.0
        .handshake_timeout
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Duration::from_millis(200);
    let h = hub.clone();
    let addr = within(move || h.listen("127.0.0.1:0")).unwrap();
    let got = within(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            let (mut ws, _) = tokio_tungstenite::connect_async(format!("ws://{addr}/"))
                .await
                .unwrap();
            let started = Instant::now();
            loop {
                match ws.next().await {
                    Some(Ok(Message::Close(f))) => {
                        return (
                            f.map(|f| (u16::from(f.code), f.reason.to_string())),
                            started.elapsed(),
                        )
                    }
                    Some(Ok(_)) => continue,
                    _ => return (None, started.elapsed()),
                }
            }
        })
    });
    assert_eq!(got.0, Some((1008, "auth required".to_string())));
    assert!(
        got.1 < Duration::from_secs(5),
        "bounded by the handshake timeout"
    );
    close(&hub);
}

/// A raw acceptor that upgrades, reads the auth frame, sends `reply`
/// (if any) and records every frame and the close it gets.
pub(super) fn scripted_acceptor(
    reply: Option<&'static str>,
) -> (std::net::SocketAddr, mpsc::Receiver<Vec<String>>) {
    use futures_util::{SinkExt, StreamExt};
    use tokio_tungstenite::tungstenite::Message;

    let ln = TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = ln.local_addr().unwrap();
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            ln.set_nonblocking(true).unwrap();
            let ln = tokio::net::TcpListener::from_std(ln).unwrap();
            let (tcp, _) = ln.accept().await.unwrap();
            let mut ws = tokio_tungstenite::accept_async(tcp).await.unwrap();
            let mut frames = Vec::new();
            while let Some(Ok(m)) = ws.next().await {
                match m {
                    Message::Text(t) => {
                        frames.push(t.to_string());
                        if let Some(r) = reply {
                            ws.send(Message::text(r)).await.unwrap();
                        }
                    }
                    Message::Close(f) => {
                        frames.push(format!(
                            "close {:?}",
                            f.map(|f| (u16::from(f.code), f.reason.to_string()))
                        ));
                        break;
                    }
                    _ => {}
                }
            }
            let _ = tx.send(frames);
        });
    });
    (addr, rx)
}

/// An ack other than `auth_ok` is a refusal: 1008 `auth rejected`.
#[test]
fn dialer_refuses_an_ack_other_than_auth_ok() {
    let (addr, rx) = scripted_acceptor(Some(
        r#"{"type":"session_error","code":"bad_request","message":"x"}"#,
    ));
    let b = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().auth(StaticTokenAuth::new("tok")),
    );
    let bb = b.clone();
    let err = within(move || bb.connect(&Context::background(), &format!("ws://{addr}/")))
        .expect_err("refused");
    assert!(matches!(err, NetworkError::AuthFailed), "{err:?}");
    let frames = rx.recv_timeout(Duration::from_secs(10)).unwrap();
    assert_eq!(frames[1], r#"close Some((1008, "auth rejected"))"#);
    assert_eq!(
        b.link_state(&format!("ws://{addr}/")).map(|s| s.code),
        Some("auth".to_string())
    );
    close(&b);
}

/// The dialer waits a bounded time for the ack, then closes with 1008
/// and reports the timeout, which still counts as an auth failure.
#[test]
fn dialer_times_out_a_silent_acceptor() {
    let (addr, rx) = scripted_acceptor(None);
    let b = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().auth(StaticTokenAuth::new("tok")),
    );
    *b.0.handshake_timeout
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Duration::from_millis(200);
    let bb = b.clone();
    let err = within(move || bb.connect(&Context::background(), &format!("ws://{addr}/")))
        .expect_err("no ack");
    assert!(matches!(err, NetworkError::AckTimeout), "{err:?}");
    assert!(err.is_auth_failed());
    assert_eq!(
        err.to_string(),
        "bus: network auth failed: no ack before the timeout"
    );
    let frames = rx.recv_timeout(Duration::from_secs(10)).unwrap();
    assert_eq!(
        frames,
        vec![
            r#"{"auth":"tok"}"#.to_string(),
            r#"close Some((1008, "auth ack timeout"))"#.to_string()
        ]
    );
    assert_eq!(
        b.link_state(&format!("ws://{addr}/")).map(|s| s.code),
        Some("timeout".to_string())
    );
    close(&b);
}

/// A TCP client that never sends the upgrade is dropped once the
/// handshake bound passes.
#[test]
fn listen_drops_a_stalled_upgrade() {
    use std::io::Read;

    let hub = NetworkAdapter::new(DynBus::new(), NetworkOptions::new());
    *hub.0
        .handshake_timeout
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Duration::from_millis(200);
    let h = hub.clone();
    let addr = within(move || h.listen("127.0.0.1:0")).unwrap();
    let elapsed = within(move || {
        let mut s = std::net::TcpStream::connect(addr).unwrap();
        s.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
        let started = Instant::now();
        let mut buf = [0u8; 16];
        let n = s.read(&mut buf).unwrap_or(usize::MAX);
        (n, started.elapsed())
    });
    assert_eq!(elapsed.0, 0, "the server closes the stalled connection");
    assert!(elapsed.1 < Duration::from_secs(3), "{:?}", elapsed.1);
    close(&hub);
}
