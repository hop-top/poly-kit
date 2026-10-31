//! The acceptor: coder/websocket's upgrade and Origin checks, the
//! handshake's close codes as a raw peer sees them, 1001 on close, the
//! read limit, and the axum adapter.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::io::{Read, Write};
use std::net::TcpStream;
use std::time::Duration;

use bus_network_support::*;
use futures_util::{SinkExt, StreamExt};
use hop_top_kit::bus::network::{check_upgrade, NetworkOptions, StaticTokenAuth};
use tokio_tungstenite::tungstenite::http::{Request, StatusCode};
use tokio_tungstenite::tungstenite::Message;

const KEY: &str = "dGhlIHNhbXBsZSBub25jZQ==";

fn upgrade_req() -> tokio_tungstenite::tungstenite::http::request::Builder {
    Request::get("/bus")
        .header("Host", "hub.example:8080")
        .header("Connection", "keep-alive, Upgrade")
        .header("Upgrade", "websocket")
        .header("Sec-WebSocket-Version", "13")
        .header("Sec-WebSocket-Key", KEY)
}

#[test]
fn a_valid_upgrade_passes_with_the_rfc_accept_key() {
    let up = check_upgrade(&upgrade_req().body(()).unwrap()).expect("valid upgrade");
    let r = up.response();
    assert_eq!(r.status(), StatusCode::SWITCHING_PROTOCOLS);
    // RFC 6455 section 1.3's worked example.
    assert_eq!(
        r.headers()["Sec-WebSocket-Accept"],
        "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
    );
    assert_eq!(r.headers()["Upgrade"], "websocket");
}

#[test]
fn origin_check_matches_coder_websocket_default() {
    let check =
        |origin: &str| check_upgrade(&upgrade_req().header("Origin", origin).body(()).unwrap());
    assert!(check("http://hub.example:8080").is_ok(), "same host");
    assert!(
        check("https://HUB.example:8080").is_ok(),
        "host compared ignoring case"
    );

    let r = check("https://evil.example").unwrap_err();
    assert_eq!(r.status, StatusCode::FORBIDDEN);
    assert_eq!(
        r.message,
        r#"request Origin "evil.example" is not authorized for Host "hub.example:8080""#
    );
    let r = check("null").unwrap_err();
    assert_eq!(
        r.message,
        r#"request Origin "null" is not a valid URL with a host"#
    );
    let r = check("http://hub.example:x").unwrap_err();
    assert_eq!(r.status, StatusCode::FORBIDDEN);
    assert!(
        r.message.starts_with("failed to parse Origin header"),
        "{}",
        r.message
    );
    let resp = r.response();
    assert_eq!(resp.headers()["Content-Type"], "text/plain; charset=utf-8");
    assert!(resp.body().ends_with('\n'));
}

#[test]
fn upgrade_checks_follow_coder_websocket_order_and_texts() {
    let r = check_upgrade(
        &upgrade_req()
            .header("Connection", "close")
            .body(())
            .unwrap(),
    );
    // A second Connection header still holds Upgrade: passes.
    assert!(r.is_ok());

    let base = || {
        Request::get("/")
            .header("Host", "h")
            .header("Sec-WebSocket-Version", "13")
            .header("Sec-WebSocket-Key", KEY)
    };
    let r = check_upgrade(&base().header("Upgrade", "websocket").body(()).unwrap()).unwrap_err();
    assert_eq!(r.status, StatusCode::UPGRADE_REQUIRED);
    assert_eq!(
        r.message,
        r#"WebSocket protocol violation: Connection header "" does not contain Upgrade"#
    );
    let r = check_upgrade(&base().header("Connection", "upgrade").body(()).unwrap()).unwrap_err();
    assert_eq!(
        r.message,
        r#"WebSocket protocol violation: Upgrade header "" does not contain websocket"#
    );
    let post = Request::post("/")
        .header("Connection", "Upgrade")
        .header("Upgrade", "websocket")
        .body(())
        .unwrap();
    let r = check_upgrade(&post).unwrap_err();
    assert_eq!(r.status, StatusCode::METHOD_NOT_ALLOWED);
    assert_eq!(
        r.message,
        r#"WebSocket protocol violation: handshake request method is not GET but "POST""#
    );
    let r = check_upgrade(
        &upgrade_req()
            .header("Sec-WebSocket-Version", "8")
            .body(())
            .unwrap(),
    );
    // Two version headers: the first, 13, is read.
    assert!(r.is_ok());
    let v8 = Request::get("/")
        .header("Connection", "Upgrade")
        .header("Upgrade", "websocket")
        .header("Sec-WebSocket-Version", "8")
        .body(())
        .unwrap();
    let r = check_upgrade(&v8).unwrap_err();
    assert_eq!(r.status, StatusCode::BAD_REQUEST);
    assert_eq!(
        r.message,
        r#"unsupported WebSocket protocol version (only 13 is supported): "8""#
    );
    let nokey = Request::get("/")
        .header("Connection", "Upgrade")
        .header("Upgrade", "websocket")
        .header("Sec-WebSocket-Version", "13")
        .body(())
        .unwrap();
    assert_eq!(
        check_upgrade(&nokey).unwrap_err().message,
        "WebSocket protocol violation: missing Sec-WebSocket-Key"
    );
    let r = check_upgrade(
        &upgrade_req()
            .header("Sec-WebSocket-Key", KEY)
            .body(())
            .unwrap(),
    );
    assert_eq!(
        r.unwrap_err().message,
        "WebSocket protocol violation: multiple Sec-WebSocket-Key headers"
    );
    let bad = Request::get("/")
        .header("Connection", "Upgrade")
        .header("Upgrade", "websocket")
        .header("Sec-WebSocket-Version", "13")
        .header("Sec-WebSocket-Key", "short")
        .body(())
        .unwrap();
    assert_eq!(
        check_upgrade(&bad).unwrap_err().message,
        r#"WebSocket protocol violation: invalid Sec-WebSocket-Key "short", must be a 16 byte base64 encoded string"#
    );
}

/// A browser on another site is refused by `listen` with 403 before any
/// WebSocket exists.
#[test]
fn listen_refuses_a_cross_origin_browser() {
    let hub = Hub::start(NetworkOptions::new());
    let addr = hub
        .url
        .trim_start_matches("ws://")
        .trim_end_matches('/')
        .to_string();
    let reply = within(move || {
        let mut s = TcpStream::connect(&addr).unwrap();
        s.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
        write!(
            s,
            "GET /bus HTTP/1.1\r\nHost: {addr}\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\
             Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: {KEY}\r\nOrigin: https://evil.example\r\n\r\n"
        )
        .unwrap();
        let mut buf = vec![0; 4096];
        let n = s.read(&mut buf).unwrap_or(0);
        String::from_utf8_lossy(&buf[..n]).into_owned()
    });
    assert!(reply.starts_with("HTTP/1.1 403"), "{reply}");
    assert!(
        reply.contains(r#"request Origin "evil.example" is not authorized for Host"#),
        "{reply}"
    );
}

/// A raw client: connects to `url`, sends `frames`, and returns every
/// text frame and the close it got.
fn raw_session(url: String, frames: Vec<String>) -> (Vec<String>, Option<(u16, String)>) {
    within(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            let (mut ws, _) = tokio_tungstenite::connect_async(url).await.unwrap();
            for f in frames {
                ws.send(Message::text(f)).await.unwrap();
            }
            let mut got = Vec::new();
            let read = async {
                while let Some(Ok(m)) = ws.next().await {
                    match m {
                        Message::Text(t) => got.push(t.to_string()),
                        Message::Close(f) => {
                            return f.map(|f| (u16::from(f.code), f.reason.to_string()))
                        }
                        _ => {}
                    }
                }
                None
            };
            let close = tokio::time::timeout(Duration::from_secs(3), read)
                .await
                .unwrap_or(None);
            (got, close)
        })
    })
}

fn secret_hub() -> Hub {
    Hub::start(
        NetworkOptions::new()
            .origin_id("hub")
            .auth(StaticTokenAuth::new("s3cret")),
    )
}

#[test]
fn acceptor_closes_1008_invalid_auth() {
    let hub = secret_hub();
    for first in [
        r#"{"auth":""}"#,
        r#"{"auth":7}"#,
        "[]",
        "not json",
        r#"{"token":"s3cret"}"#,
    ] {
        let (got, close) = raw_session(hub.url.clone(), vec![first.to_string()]);
        assert!(got.is_empty(), "{first}: no ack");
        assert_eq!(close, Some((1008, "invalid auth".into())), "{first}");
    }
}

#[test]
fn acceptor_closes_1008_auth_failed() {
    let hub = secret_hub();
    let (got, close) = raw_session(hub.url.clone(), vec![r#"{"auth":"wrong"}"#.into()]);
    assert!(got.is_empty());
    assert_eq!(close, Some((1008, "auth failed".into())));
}

#[test]
fn acceptor_acks_with_the_exact_plain_ack_and_ignores_stream_caps() {
    let hub = secret_hub();
    let hello = r#"{"auth":"s3cret","caps":{"streams":1,"frames":["kit","cloudevents"]},"origin":"dialer","session":{"subscribe":["a.#"]}}"#;
    let url = hub.url.clone();
    let (got, _) = raw_session(url, vec![hello.into()]);
    assert_eq!(
        got.first().map(String::as_str),
        Some(r#"{"type":"auth_ok"}"#)
    );
}

#[test]
fn close_sends_1001_going_away() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let up = flag_on(&hub.bus, "link.up");
    let url = hub.url.clone();
    let n = hub.net.clone();
    let hello = r#"{"topic":"link.up","source":"raw","origin":"raw"}"#.to_string();
    let session = std::thread::spawn(move || raw_session(url, vec![hello]));
    wait_for(&up, "the raw peer's event proves the link is up");
    within(move || n.close()).unwrap();
    let (_, close) = session.join().unwrap();
    assert_eq!(close, Some((1001, "closing".into())));
}

#[test]
fn a_message_past_the_read_limit_closes_1009() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let big = format!(
        r#"{{"topic":"big.blob","payload":"{}"}}"#,
        "x".repeat(40_000)
    );
    let (_, close) = raw_session(hub.url.clone(), vec![big]);
    assert_eq!(close, Some((1009, "read limited at 32769 bytes".into())));
}

#[cfg(feature = "bus-network-axum")]
#[test]
fn the_axum_handler_accepts_peers_and_checks_origin() {
    use axum::routing::any;
    use hop_top_kit::bus::network::NetworkAdapter;
    use hop_top_kit::bus::DynBus;
    use serde_json::Value;
    use std::sync::atomic::Ordering;

    let hub_bus = DynBus::new();
    let hub = NetworkAdapter::new(hub_bus.clone(), NetworkOptions::new().origin_id("hub"));
    let handler = hub.axum_handler();
    // Mounts on a router like any handler.
    let _app: axum::Router = axum::Router::new().route("/bus", any(handler.clone()));
    let rt = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(1)
        .enable_all()
        .build()
        .unwrap();
    let listener = rt
        .block_on(tokio::net::TcpListener::bind("127.0.0.1:0"))
        .unwrap();
    let addr = listener.local_addr().unwrap();
    rt.spawn(async move {
        hyper_serve(listener, handler).await;
    });

    // A peer connects through axum and events flow both ways.
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&format!("ws://{addr}/bus"))
        .expect("connect through axum");
    let got = flag_on(&hub_bus, "via.axum");
    sleep_ms(50);
    publish(&b.bus, ev("via.axum", "B", Value::Null));
    wait_for(&got, "event through the axum-accepted link");
    let back = flag_on(&b.bus, "from.hub");
    publish(&hub_bus, ev("from.hub", "hub", Value::Null));
    assert!(eventually(DELIVERY, || back.load(Ordering::SeqCst)));

    // A cross-origin browser is refused with coder/websocket's 403.
    let reply = within(move || {
        let mut s = TcpStream::connect(addr).unwrap();
        s.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
        write!(
            s,
            "GET /bus HTTP/1.1\r\nHost: {addr}\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\
             Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: {KEY}\r\nOrigin: https://evil.example\r\n\r\n"
        )
        .unwrap();
        let mut buf = vec![0; 4096];
        let n = s.read(&mut buf).unwrap_or(0);
        String::from_utf8_lossy(&buf[..n]).into_owned()
    });
    assert!(reply.starts_with("HTTP/1.1 403"), "{reply}");
    let h = hub.clone();
    within(move || h.close()).unwrap();
    drop(b);
    rt.shutdown_timeout(Duration::from_secs(1));
}

/// Serves `handler` with hyper, recording each peer's address as
/// axum's `into_make_service_with_connect_info` does.
#[cfg(feature = "bus-network-axum")]
async fn hyper_serve<H, T>(listener: tokio::net::TcpListener, handler: H)
where
    H: axum::handler::Handler<T, ()>,
    T: 'static,
{
    use hyper_util::rt::TokioIo;
    loop {
        let Ok((tcp, peer)) = listener.accept().await else {
            return;
        };
        let handler = handler.clone();
        tokio::spawn(async move {
            let svc = hyper::service::service_fn(
                move |mut req: hyper::Request<hyper::body::Incoming>| {
                    req.extensions_mut()
                        .insert(axum::extract::ConnectInfo(peer));
                    let fut = handler.clone().call(req.map(axum::body::Body::new), ());
                    async move { Ok::<_, std::convert::Infallible>(fut.await) }
                },
            );
            let _ = hyper::server::conn::http1::Builder::new()
                .serve_connection(TokioIo::new(tcp), svc)
                .with_upgrades()
                .await;
        });
    }
}

/// Close stops a session still in its handshake and waits for it: no
/// verify and no ack after close returns.
#[test]
fn accept_upgraded_session_does_not_outlive_close() {
    use hop_top_kit::bus::network::{AuthError, Authenticator, NetworkAdapter};
    use hop_top_kit::bus::DynBus;
    use std::sync::atomic::{AtomicBool, Ordering};
    use std::sync::Arc;
    use tokio_tungstenite::tungstenite::protocol::Role;

    struct Spy(Arc<AtomicBool>);
    impl Authenticator for Spy {
        fn token(&self) -> Result<String, AuthError> {
            Ok("t".into())
        }
        fn verify(&self, _: &str) -> Result<(), AuthError> {
            self.0.store(true, Ordering::SeqCst);
            Ok(())
        }
    }
    let verified = Arc::new(AtomicBool::new(false));
    let hub = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().auth(Spy(Arc::clone(&verified))),
    );
    let h = hub.clone();
    let reply = within(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            let (client, server) = tokio::io::duplex(1 << 16);
            h.accept_upgraded(server, "10.0.0.9:1");
            let closer = h.clone();
            let started = std::time::Instant::now();
            tokio::task::spawn_blocking(move || closer.close())
                .await
                .unwrap()
                .unwrap();
            // Close stops the session at once, not after the 10 s auth bound.
            assert!(
                started.elapsed() < Duration::from_secs(2),
                "{:?}",
                started.elapsed()
            );
            let mut ws =
                tokio_tungstenite::WebSocketStream::from_raw_socket(client, Role::Client, None)
                    .await;
            let _ = ws.send(Message::text(r#"{"auth":"t"}"#)).await;
            let read = async {
                while let Some(Ok(m)) = ws.next().await {
                    if let Message::Text(t) = m {
                        return Some(t.to_string());
                    }
                }
                None
            };
            tokio::time::timeout(Duration::from_secs(2), read)
                .await
                .unwrap_or(None)
        })
    });
    assert_eq!(reply, None, "no ack after close");
    assert!(!verified.load(Ordering::SeqCst), "verify ran after close");
}
