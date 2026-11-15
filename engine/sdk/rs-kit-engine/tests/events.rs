#![cfg(feature = "events")]

use futures_util::SinkExt;
use hop_top_kit_engine::KitEngine;
use tokio::net::TcpListener;
use tokio_tungstenite::tungstenite::Message;

#[tokio::test]
async fn yields_only_matching_topic_data() {
    let ws_listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let port = ws_listener.local_addr().unwrap().port();
    tokio::spawn(async move {
        let (sock, _) = ws_listener.accept().await.unwrap();
        let mut ws = tokio_tungstenite::accept_async(sock).await.unwrap();
        for m in [
            r#"{"type":"notes.deleted","data":{"id":"x"}}"#,
            "not json",
            r#"{"type":"notes.created","data":{"id":"a"}}"#,
        ] {
            ws.send(Message::text(m)).await.unwrap();
        }
        // hold the socket open
        tokio::time::sleep(std::time::Duration::from_secs(5)).await;
    });
    let e = engine_on(port).await;
    let mut s = e.events("notes.created");
    let v = tokio::time::timeout(std::time::Duration::from_secs(3), s.next())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(v["id"], "a");
}

async fn engine_on(port: u16) -> KitEngine {
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
    let mut e = KitEngine::connect(mp, None, None).await.unwrap();
    e.port = port;
    std::mem::forget(m);
    e
}
