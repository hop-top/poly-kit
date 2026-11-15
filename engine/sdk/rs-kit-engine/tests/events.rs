#![cfg(feature = "events")]

use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use hop_top_kit_engine::{EventStream, KitEngine};
use tokio::net::TcpListener;
use tokio_tungstenite::tungstenite::handshake::server::{Request, Response};
use tokio_tungstenite::tungstenite::Message;

/// What a scripted connection saw.
#[derive(Default, Clone)]
struct Seen {
    auth: Arc<Mutex<Vec<Option<String>>>>,
    frames: Arc<Mutex<Vec<serde_json::Value>>>,
    conns: Arc<Mutex<usize>>,
}

/// Loopback ws server. Each accepted connection records its
/// Authorization header and client frames, waits for the first client
/// frame (the subscribe), then plays `script[conn]` and, if `drop_after`
/// is set for that connection, closes the socket.
// The handshake callback type fixes its large error type.
#[allow(clippy::result_large_err)]
async fn server(script: Vec<(Vec<String>, bool)>) -> (u16, Seen) {
    let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = l.local_addr().unwrap().port();
    let seen = Seen::default();
    let s = seen.clone();
    tokio::spawn(async move {
        for (frames, drop_after) in script {
            let (sock, _) = l.accept().await.unwrap();
            let auth = s.auth.clone();
            let mut ws =
                tokio_tungstenite::accept_hdr_async(sock, move |r: &Request, p: Response| {
                    auth.lock().unwrap().push(
                        r.headers()
                            .get("authorization")
                            .map(|v| v.to_str().unwrap().to_owned()),
                    );
                    Ok(p)
                })
                .await
                .unwrap();
            *s.conns.lock().unwrap() += 1;
            if let Some(Ok(Message::Text(t))) = ws.next().await {
                s.frames
                    .lock()
                    .unwrap()
                    .push(serde_json::from_str(t.as_str()).unwrap());
            }
            for f in frames {
                ws.send(Message::text(f)).await.unwrap();
            }
            if drop_after {
                drop(ws);
            } else {
                let s2 = s.clone();
                tokio::spawn(async move {
                    // keep open, record further client frames
                    while let Some(Ok(m)) = ws.next().await {
                        if let Message::Text(t) = m {
                            s2.frames
                                .lock()
                                .unwrap()
                                .push(serde_json::from_str(t.as_str()).unwrap());
                        }
                    }
                });
            }
        }
    });
    (port, seen)
}

fn msg(topic: &str, payload: &str) -> String {
    format!(r#"{{"type":"message","topic":"{topic}","payload":{payload}}}"#)
}

async fn next(s: &mut EventStream) -> serde_json::Value {
    tokio::time::timeout(Duration::from_secs(4), s.next())
        .await
        .expect("timed out")
        .unwrap()
}

#[tokio::test]
async fn subscribes_then_yields_message_payloads_skipping_control_frames() {
    let (port, seen) = server(vec![(
        vec![
            r#"{"type":"welcome"}"#.into(),
            r#"{"type":"ack","topic":"a.b"}"#.into(),
            r#"{"type":"error","topic":"a.b"}"#.into(),
            r#"{"type":"a.b","data":{"old":true}}"#.into(),
            "not json".into(),
            msg("a.b", r#"{"id":"a"}"#),
        ],
        false,
    )])
    .await;
    let e = engine_on(port, None).await;
    let mut s = e.events("a.b");
    assert_eq!(next(&mut s).await["id"], "a");
    assert_eq!(
        seen.frames.lock().unwrap()[0],
        serde_json::json!({"type":"subscribe","topic":"a.b"})
    );
}

#[tokio::test]
async fn star_is_one_segment_and_double_star_is_one_or_more() {
    let (port, _) = server(vec![(
        vec![
            msg("a", "1"),
            msg("a.b.c", "2"),
            msg("a.b", "3"),
            msg("x.b", "4"),
        ],
        false,
    )])
    .await;
    let e = engine_on(port, None).await;
    let mut one = e.events("a.*");
    assert_eq!(next(&mut one).await, 3);

    let (port, _) = server(vec![(
        vec![
            msg("a", "1"),
            msg("a.b.c", "2"),
            msg("x.b", "3"),
            msg("a.b", "4"),
        ],
        false,
    )])
    .await;
    let e = engine_on(port, None).await;
    let mut many = e.events("a.**");
    assert_eq!(next(&mut many).await, 2);
    assert_eq!(next(&mut many).await, 4);
}

#[tokio::test]
async fn bare_star_subscribes_double_star_and_yields_every_topic() {
    let (port, seen) = server(vec![(vec![msg("a", "1"), msg("a.b.c", "2")], false)]).await;
    let e = engine_on(port, None).await;
    let mut s = e.events("*");
    assert_eq!(next(&mut s).await, 1);
    assert_eq!(next(&mut s).await, 2);
    assert_eq!(
        seen.frames.lock().unwrap()[0],
        serde_json::json!({"type":"subscribe","topic":"**"})
    );
}

#[tokio::test]
async fn sends_bearer_token_when_set() {
    let (port, seen) = server(vec![(vec![msg("a.b", "1")], false)]).await;
    let e = engine_on(port, Some("tok")).await;
    let mut s = e.events("a.b");
    next(&mut s).await;
    assert_eq!(seen.auth.lock().unwrap()[0].as_deref(), Some("Bearer tok"));
}

#[tokio::test]
async fn reconnects_and_resubscribes_after_a_drop() {
    let (port, seen) = server(vec![
        (vec![msg("a.b", "1")], true),
        (vec![msg("a.b", "2")], false),
    ])
    .await;
    let e = engine_on(port, None).await;
    let mut s = e.events("a.b");
    assert_eq!(next(&mut s).await, 1);
    assert_eq!(next(&mut s).await, 2);
    assert_eq!(*seen.conns.lock().unwrap(), 2);
    let frames = seen.frames.lock().unwrap();
    assert_eq!(frames.len(), 2);
    assert_eq!(
        frames[1],
        serde_json::json!({"type":"subscribe","topic":"a.b"})
    );
}

#[tokio::test]
async fn first_connect_failure_is_an_error() {
    let l = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = l.local_addr().unwrap().port();
    drop(l);
    let e = engine_on(port, None).await;
    let mut s = e.events("a.b");
    assert!(s.next().await.is_err());
}

async fn engine_on(port: u16, token: Option<&str>) -> KitEngine {
    // events() derives its URL from the engine port: attach via a mock
    // engine for /health, then point the handle at the websocket server.
    let mut m = mockito::Server::new_async().await;
    m.mock("GET", "/health")
        .with_body(r#"{"pid":1}"#)
        .create_async()
        .await;
    let mp: u16 = m
        .host_with_port()
        .rsplit(':')
        .next()
        .unwrap()
        .parse()
        .unwrap();
    let mut e = KitEngine::connect(mp, token.map(str::to_owned), None)
        .await
        .unwrap();
    e.port = port;
    std::mem::forget(m);
    e
}
