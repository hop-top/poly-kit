//! The websocket hub on the api-server, over real sockets. Ports the
//! Go hub tests (`ws_test.go`, `e2e_ws_test.go`); Go is the authority.
#![cfg(feature = "api-server-ws")]

mod api_server_support;

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;

use axum::Router;
use futures_util::{SinkExt, StreamExt};
use hop_top_kit::api_server::ws::{
    Hub, HubClosed, HubOptions, DEFAULT_MAX_MESSAGE_SIZE, DEFAULT_SEND_BUFFER,
    MAX_PATTERN_DOUBLE_STARS, MAX_PATTERN_LEN, MAX_PATTERN_SEGMENTS, MAX_SUBSCRIPTIONS,
    MAX_SUBSCRIPTION_BYTES,
};
use hop_top_kit::authn::{AuthFunc, AuthnError, Claims};
use serde_json::{json, Value};
use tokio::net::TcpStream;
use tokio_tungstenite::tungstenite::client::IntoClientRequest;
use tokio_tungstenite::tungstenite::protocol::frame::coding::CloseCode;
use tokio_tungstenite::tungstenite::{Error as WsError, Message};
use tokio_tungstenite::{client_async, WebSocketStream};

use api_server_support::{config, start, Running};

type Client = WebSocketStream<TcpStream>;

fn corpus() -> Value {
    let path = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../contracts/parity/api.json");
    serde_json::from_slice(&std::fs::read(path).expect("read api.json")).expect("JSON")
}

async fn server(
    hub: &Hub,
    tweak: impl FnOnce(&mut hop_top_kit::api_server::ApiServerConfig),
) -> Running {
    let mut cfg = config(Router::new().route("/ws", hub.route()));
    tweak(&mut cfg);
    start(cfg).await
}

async fn dial_with(run: &Running, headers: &[(&str, &str)]) -> Result<Client, WsError> {
    let tcp = TcpStream::connect(&run.addr).await.unwrap();
    let mut req = format!("ws://{}/ws", run.addr)
        .into_client_request()
        .unwrap();
    for (k, v) in headers {
        req.headers_mut().insert(
            axum::http::HeaderName::try_from(*k).unwrap(),
            v.parse().unwrap(),
        );
    }
    client_async(req, tcp).await.map(|(ws, _)| ws)
}

async fn dial(run: &Running) -> Client {
    dial_with(run, &[]).await.expect("dial")
}

async fn next_json(ws: &mut Client) -> Value {
    loop {
        match tokio::time::timeout(Duration::from_secs(5), ws.next())
            .await
            .expect("a frame within 5s")
        {
            Some(Ok(Message::Text(t))) => return serde_json::from_str(t.as_str()).unwrap(),
            Some(Ok(Message::Ping(_) | Message::Pong(_))) => continue,
            other => panic!("expected a text frame, got {other:?}"),
        }
    }
}

async fn expect_silence(ws: &mut Client) {
    assert!(
        tokio::time::timeout(Duration::from_millis(250), ws.next())
            .await
            .is_err(),
        "no frame expected"
    );
}

async fn send(ws: &mut Client, v: Value) {
    ws.send(Message::text(v.to_string())).await.unwrap();
}

async fn subscribe(ws: &mut Client, topic: &str) {
    send(ws, json!({"type": "subscribe", "topic": topic})).await;
    let ack = next_json(ws).await;
    // An empty topic is omitted from the envelope, as in Go.
    let want = if topic.is_empty() {
        json!({"type": "ack"})
    } else {
        json!({"type": "ack", "topic": topic})
    };
    assert_eq!(ack, want);
}

/// The connection ending: a close frame's code, or None for a plain end.
async fn closed(ws: &mut Client) -> Option<u16> {
    loop {
        match tokio::time::timeout(Duration::from_secs(5), ws.next())
            .await
            .expect("the connection ends within 5s")
        {
            Some(Ok(Message::Close(f))) => return f.map(|f| u16::from(f.code)),
            Some(Ok(_)) => continue,
            Some(Err(_)) | None => return None,
        }
    }
}

// --- the Go hub tests ------------------------------------------------------

#[tokio::test]
async fn welcome_on_connect_mounted_on_the_router() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    assert_eq!(next_json(&mut ws).await, json!({"type": "welcome"}));
    assert_eq!(hub.clients(), 1);
    run.stop().await;
}

#[tokio::test]
async fn subscribe_ack_and_receive_the_envelope() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    subscribe(&mut ws, "order.created").await;

    hub.publish("order.created", &json!({"id": 7, "tags": ["a"]}))
        .unwrap();
    assert_eq!(
        next_json(&mut ws).await,
        json!({"type": "message", "topic": "order.created", "payload": {"id": 7, "tags": ["a"]}})
    );
    hub.publish("order.deleted", "nope").unwrap();
    expect_silence(&mut ws).await;
    run.stop().await;
}

#[tokio::test]
async fn unsubscribe_stops_messages() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    subscribe(&mut ws, "t").await;
    send(&mut ws, json!({"type": "unsubscribe", "topic": "t"})).await;
    assert_eq!(
        next_json(&mut ws).await,
        json!({"type": "ack", "topic": "t"})
    );
    hub.publish("t", "x").unwrap();
    expect_silence(&mut ws).await;
    run.stop().await;
}

#[tokio::test]
async fn glob_topics() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;

    let mut single = dial(&run).await;
    next_json(&mut single).await;
    subscribe(&mut single, "order.*").await;
    hub.publish("order.created", "a").unwrap();
    assert_eq!(next_json(&mut single).await["topic"], "order.created");
    hub.publish("order.item.added", "c").unwrap();
    expect_silence(&mut single).await;

    let mut double = dial(&run).await;
    next_json(&mut double).await;
    subscribe(&mut double, "domain.**").await;
    hub.publish("domain.entity.created", "x").unwrap();
    assert_eq!(
        next_json(&mut double).await["topic"],
        "domain.entity.created"
    );
    run.stop().await;
}

#[tokio::test]
async fn only_subscribers_receive() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut a = dial(&run).await;
    let mut b = dial(&run).await;
    next_json(&mut a).await;
    next_json(&mut b).await;
    subscribe(&mut a, "t").await;
    hub.publish("t", &1).unwrap();
    assert_eq!(next_json(&mut a).await["payload"], 1);
    expect_silence(&mut b).await;
    run.stop().await;
}

#[tokio::test]
async fn a_subscription_over_the_limit_is_an_error_envelope() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    for i in 0..MAX_SUBSCRIPTIONS {
        send(
            &mut ws,
            json!({"type": "subscribe", "topic": format!("t{i}")}),
        )
        .await;
    }
    for _ in 0..MAX_SUBSCRIPTIONS {
        assert_eq!(next_json(&mut ws).await["type"], "ack");
    }
    send(
        &mut ws,
        json!({"type": "subscribe", "topic": "one.too.many"}),
    )
    .await;
    assert_eq!(
        next_json(&mut ws).await,
        json!({"type": "error", "topic": "one.too.many"})
    );
    run.stop().await;
}

#[tokio::test]
async fn close_ends_connections_going_away_and_publish_reports_closed() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    hub.close();
    assert_eq!(closed(&mut ws).await, Some(1001));
    assert_eq!(hub.publish("t", &1), Err(HubClosed));
    assert_eq!(hub.clients(), 0);
    // A new upgrade is refused with a plain HTTP answer.
    let err = dial_with(&run, &[]).await.unwrap_err();
    match err {
        WsError::Http(r) => assert_eq!(r.status(), 503),
        e => panic!("{e:?}"),
    }
    hub.close(); // idempotent
    run.stop().await;
}

#[tokio::test]
async fn a_closed_client_is_unregistered() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    assert_eq!(hub.clients(), 1);
    ws.close(None).await.unwrap();
    closed(&mut ws).await;
    for _ in 0..100 {
        if hub.clients() == 0 {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert_eq!(hub.clients(), 0);
    run.stop().await;
}

#[tokio::test]
async fn garbage_and_unknown_types_are_ignored_and_binary_is_read_too() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    ws.send(Message::text("not json")).await.unwrap();
    send(&mut ws, json!({"type": "dance"})).await;
    send(&mut ws, json!({"type": 5})).await;
    ws.send(Message::binary(
        json!({"type": "subscribe", "topic": "b"})
            .to_string()
            .into_bytes(),
    ))
    .await
    .unwrap();
    assert_eq!(
        next_json(&mut ws).await,
        json!({"type": "ack", "topic": "b"})
    );
    run.stop().await;
}

// --- upgrade-time policy: auth, host, origin ------------------------------

fn bearer() -> AuthFunc {
    Arc::new(|c| match c.authorization {
        Some("Bearer ws-token") => Ok(Claims {
            subject: "u".into(),
            ..Default::default()
        }),
        _ => Err(AuthnError::NoToken(None)),
    })
}

#[tokio::test]
async fn authentication_applies_before_the_upgrade() {
    let hub = Hub::new();
    let run = server(&hub, |c| c.auth = Some(bearer())).await;
    match dial_with(&run, &[]).await.unwrap_err() {
        WsError::Http(r) => assert_eq!(r.status(), 401),
        e => panic!("{e:?}"),
    }
    assert_eq!(hub.clients(), 0, "nothing registered for a refused upgrade");
    let mut ws = dial_with(&run, &[("authorization", "Bearer ws-token")])
        .await
        .expect("accepted with a token");
    assert_eq!(next_json(&mut ws).await["type"], "welcome");
    run.stop().await;
}

#[tokio::test]
async fn the_host_check_applies_to_the_upgrade() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    match dial_with(&run, &[("host", "rebind.evil.test")])
        .await
        .unwrap_err()
    {
        WsError::Http(r) => assert_eq!(r.status(), 403),
        e => panic!("{e:?}"),
    }
    assert_eq!(hub.clients(), 0);
    run.stop().await;
}

#[tokio::test]
async fn a_cross_origin_upgrade_is_refused_unless_accepted() {
    let evil = [("origin", "https://evil.test")];
    // Same origin and no Origin at all (not a browser) pass.
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let own = format!("http://{}", run.addr);
    assert!(dial_with(&run, &[("origin", own.as_str())]).await.is_ok());
    assert!(dial_with(&run, &[]).await.is_ok());
    // Another site is refused 403 at the upgrade, before registering.
    match dial_with(&run, &evil).await.unwrap_err() {
        WsError::Http(r) => assert_eq!(r.status(), 403),
        e => panic!("{e:?}"),
    }
    assert!(dial_with(&run, &[("origin", "null")]).await.is_err());
    run.stop().await;

    // Accepted by pattern.
    let hub = Hub::with_options(HubOptions {
        accept_origins: vec!["*.test".into()],
        ..HubOptions::default()
    });
    let run = server(&hub, |_| {}).await;
    assert!(dial_with(&run, &evil).await.is_ok());
    assert!(dial_with(&run, &[("origin", "https://evil.example")])
        .await
        .is_err());
    run.stop().await;
}

#[tokio::test]
async fn a_malformed_upgrade_is_answered_as_http() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let raw = |req: &'static str| {
        let addr = run.addr.clone();
        async move {
            use tokio::io::{AsyncReadExt, AsyncWriteExt};
            let mut s = TcpStream::connect(&addr).await.unwrap();
            s.write_all(req.replace("HOST", &addr).as_bytes())
                .await
                .unwrap();
            let mut b = vec![0u8; 512];
            let n = s.read(&mut b).await.unwrap();
            String::from_utf8_lossy(&b[..n]).into_owned()
        }
    };
    let plain = raw("GET /ws HTTP/1.1\r\nhost: HOST\r\n\r\n").await;
    assert!(plain.starts_with("HTTP/1.1 426"), "{plain}");
    let post = raw("POST /ws HTTP/1.1\r\nhost: HOST\r\nconnection: upgrade\r\nupgrade: websocket\r\ncontent-length: 0\r\n\r\n").await;
    assert!(post.starts_with("HTTP/1.1 405"), "{post}");
    let v8 = raw("GET /ws HTTP/1.1\r\nhost: HOST\r\nconnection: upgrade\r\nupgrade: websocket\r\nsec-websocket-version: 8\r\nsec-websocket-key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n").await;
    assert!(v8.starts_with("HTTP/1.1 400"), "{v8}");
    let nokey = raw("GET /ws HTTP/1.1\r\nhost: HOST\r\nconnection: upgrade\r\nupgrade: websocket\r\nsec-websocket-version: 13\r\n\r\n").await;
    assert!(nokey.starts_with("HTTP/1.1 400"), "{nokey}");
    let badkey = raw("GET /ws HTTP/1.1\r\nhost: HOST\r\nconnection: upgrade\r\nupgrade: websocket\r\nsec-websocket-version: 13\r\nsec-websocket-key: short\r\n\r\n").await;
    assert!(badkey.starts_with("HTTP/1.1 400"), "{badkey}");
    run.stop().await;
}

// --- bounds ---------------------------------------------------------------

#[tokio::test]
async fn a_message_over_the_cap_closes_the_connection_1009() {
    let hub = Hub::with_options(HubOptions {
        max_message_size: 1024,
        ..HubOptions::default()
    });
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    // Under the cap still works.
    let pad = "x".repeat(200);
    send(&mut ws, json!({"type": "subscribe", "topic": pad})).await;
    assert_eq!(next_json(&mut ws).await["type"], "ack");
    // Over it does not.
    ws.send(Message::text("y".repeat(4096))).await.unwrap();
    assert_eq!(closed(&mut ws).await, Some(u16::from(CloseCode::Size)));
    for _ in 0..100 {
        if hub.clients() == 0 {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert_eq!(hub.clients(), 0, "and the hub forgot it");
    run.stop().await;
}

#[tokio::test]
async fn the_default_cap_is_64_kib() {
    assert_eq!(DEFAULT_MAX_MESSAGE_SIZE, 65536);
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    ws.send(Message::text("z".repeat(DEFAULT_MAX_MESSAGE_SIZE + 1)))
        .await
        .unwrap();
    assert_eq!(closed(&mut ws).await, Some(1009));
    run.stop().await;
}

#[tokio::test]
async fn a_full_send_buffer_drops_and_counts_never_queues_without_bound() {
    let dropped = Arc::new(AtomicUsize::new(0));
    let d2 = dropped.clone();
    let hub = Hub::with_options(HubOptions {
        send_buffer: 1,
        on_drop: Some(Arc::new(move |addr, topic| {
            assert!(!addr.is_empty());
            assert_eq!(topic, "storm");
            d2.fetch_add(1, Ordering::SeqCst);
        })),
        ..HubOptions::default()
    });
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    subscribe(&mut ws, "storm").await;
    for i in 0..2000 {
        hub.publish("storm", &i).unwrap();
    }
    assert!(hub.dropped() > 0, "a 1-message buffer overflowed");
    assert_eq!(hub.dropped() as usize, dropped.load(Ordering::SeqCst));
    // The client is alive and still served.
    let mut seen = 0;
    while let Ok(Some(Ok(Message::Text(_)))) =
        tokio::time::timeout(Duration::from_millis(300), ws.next()).await
    {
        seen += 1;
    }
    assert!((1..2000).contains(&seen), "{seen}");
    hub.publish("storm", "after").unwrap();
    assert_eq!(DEFAULT_SEND_BUFFER, 64);
    run.stop().await;
}

#[tokio::test]
async fn ping_is_answered_with_pong() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    ws.send(Message::Ping(b"hb".to_vec().into())).await.unwrap();
    loop {
        match tokio::time::timeout(Duration::from_secs(5), ws.next())
            .await
            .unwrap()
        {
            Some(Ok(Message::Pong(p))) => {
                assert_eq!(&p[..], b"hb");
                break;
            }
            Some(Ok(_)) => continue,
            other => panic!("{other:?}"),
        }
    }
    run.stop().await;
}

#[tokio::test]
async fn an_idle_connection_is_closed_only_when_a_timeout_is_set() {
    let hub = Hub::with_options(HubOptions {
        idle_timeout: Some(Duration::from_millis(300)),
        ..HubOptions::default()
    });
    let run = server(&hub, |_| {}).await;
    let mut quiet = dial(&run).await;
    next_json(&mut quiet).await;
    let mut chatty = dial(&run).await;
    next_json(&mut chatty).await;
    for _ in 0..6 {
        tokio::time::sleep(Duration::from_millis(100)).await;
        chatty.send(Message::Ping(Vec::new().into())).await.unwrap();
        // drain the pong
        let _ = tokio::time::timeout(Duration::from_millis(50), chatty.next()).await;
    }
    assert_eq!(closed(&mut quiet).await, Some(1001));
    assert_eq!(hub.clients(), 1, "the one that kept talking stays");
    run.stop().await;

    // Go's default: never.
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    expect_silence(&mut ws).await;
    assert_eq!(hub.clients(), 1);
    run.stop().await;
}

// --- the corpus -----------------------------------------------------------

#[test]
fn constants_and_topics_match_the_corpus() {
    let c = corpus();
    let k = &c["constants"]["websocket"];
    assert_eq!(k["default_max_message_size"], DEFAULT_MAX_MESSAGE_SIZE);
    assert_eq!(k["send_buffer"], DEFAULT_SEND_BUFFER);
    assert_eq!(k["max_subscriptions"], MAX_SUBSCRIPTIONS);
    assert_eq!(k["max_pattern_len"], MAX_PATTERN_LEN);
    assert_eq!(k["max_pattern_segments"], MAX_PATTERN_SEGMENTS);
    assert_eq!(k["max_pattern_double_stars"], MAX_PATTERN_DOUBLE_STARS);
    assert_eq!(k["max_subscription_bytes"], MAX_SUBSCRIPTION_BYTES);
}

#[tokio::test]
async fn topic_corpus() {
    // The matcher through the hub: a client subscribed to the pattern
    // receives a publish on the topic iff the corpus says it matches.
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    for case in corpus()["websocket_topics"].as_array().unwrap() {
        let (pattern, topic) = (
            case["pattern"].as_str().unwrap(),
            case["topic"].as_str().unwrap(),
        );
        let mut ws = dial(&run).await;
        next_json(&mut ws).await;
        subscribe(&mut ws, pattern).await;
        hub.publish(topic, &1).unwrap();
        let got = tokio::time::timeout(Duration::from_millis(150), ws.next())
            .await
            .is_ok();
        assert_eq!(
            got,
            case["match"].as_bool().unwrap(),
            "{pattern:?} vs {topic:?}"
        );
    }
    run.stop().await;
}

async fn expect_error(ws: &mut Client, topic: &str) {
    assert_eq!(
        next_json(ws).await,
        json!({"type": "error", "topic": topic}),
        "refused with an error envelope"
    );
}

#[tokio::test]
async fn an_oversized_or_deep_pattern_is_refused_and_not_stored() {
    let hub = Hub::new();
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;

    let long = "a".repeat(MAX_PATTERN_LEN + 1);
    send(&mut ws, json!({"type": "subscribe", "topic": long})).await;
    expect_error(&mut ws, &long).await;

    let deep = vec!["a"; MAX_PATTERN_SEGMENTS + 1].join(".");
    send(&mut ws, json!({"type": "subscribe", "topic": deep})).await;
    expect_error(&mut ws, &deep).await;

    let stars = ["**"; MAX_PATTERN_DOUBLE_STARS + 1].join(".");
    send(&mut ws, json!({"type": "subscribe", "topic": stars})).await;
    expect_error(&mut ws, &stars).await;

    // Nothing was stored: a publish on a topic those would match is silent.
    hub.publish(&long, &1).unwrap();
    hub.publish(&deep, &1).unwrap();
    expect_silence(&mut ws).await;

    // The bounds themselves are allowed.
    subscribe(&mut ws, &"a".repeat(MAX_PATTERN_LEN)).await;
    subscribe(&mut ws, &vec!["a"; MAX_PATTERN_SEGMENTS].join(".")).await;
    subscribe(&mut ws, &["**"; MAX_PATTERN_DOUBLE_STARS].join(".")).await;
    run.stop().await;
}

#[tokio::test]
async fn the_total_subscription_bytes_are_budgeted_and_released() {
    let hub = Hub::with_options(HubOptions {
        max_message_size: 1 << 20,
        ..HubOptions::default()
    });
    let run = server(&hub, |_| {}).await;
    let mut ws = dial(&run).await;
    next_json(&mut ws).await;
    let per = MAX_PATTERN_LEN;
    let fits = MAX_SUBSCRIPTION_BYTES / per;
    assert!(
        fits < MAX_SUBSCRIPTIONS,
        "the byte budget bites before the count"
    );
    let pat = |i: usize| format!("{i:0>per$}");
    for i in 0..fits {
        send(&mut ws, json!({"type": "subscribe", "topic": pat(i)})).await;
    }
    for _ in 0..fits {
        assert_eq!(next_json(&mut ws).await["type"], "ack");
    }
    send(&mut ws, json!({"type": "subscribe", "topic": pat(fits)})).await;
    expect_error(&mut ws, &pat(fits)).await;
    // Subscribing again to a held pattern costs nothing.
    subscribe(&mut ws, &pat(0)).await;
    // Unsubscribing frees the budget.
    send(&mut ws, json!({"type": "unsubscribe", "topic": pat(0)})).await;
    assert_eq!(next_json(&mut ws).await["type"], "ack");
    subscribe(&mut ws, &pat(fits)).await;
    run.stop().await;
}
