//! What the reader does with inbound frames: drop the ones this adapter
//! originated and the adapter's reserved topics, read CloudEvents frames
//! too, and mark what it publishes as network-originated.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::sync::{Arc, Mutex};
use std::time::Duration;

use bus_network_support::*;
use futures_util::SinkExt;
use hop_top_kit::bus::network::{NetworkOptions, TopicFilter};
use tokio_tungstenite::tungstenite::Message;

/// Sends `frames` from a raw client, then holds the link open briefly.
fn send_raw(url: String, frames: Vec<&'static str>) {
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
            tokio::time::sleep(Duration::from_millis(300)).await;
            let _ = ws.close(None).await;
        });
    });
}

#[test]
fn own_origin_and_reserved_topics_are_dropped() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let seen = Arc::new(Mutex::new(Vec::new()));
    let s = Arc::clone(&seen);
    hub.bus.subscribe("#", move |e| {
        s.lock().unwrap().push(e.topic.as_str().to_string());
        Ok(())
    });
    send_raw(
        hub.url.clone(),
        vec![
            r#"{"topic":"mine.echo.event.sent","source":"x","origin":"hub"}"#,
            r#"{"topic":"kit.runtime.link.connected","source":"x","origin":"peer"}"#,
            r#"{"topic":"kit.runtime.stream.invalidated","source":"x","origin":"peer"}"#,
            "not json",
            r#"{"topic":7,"origin":"peer"}"#,
            r#"{"specversion":"1.0","id":"e1","source":"ce","type":"ce.test.event.sent","kitorigin":"peer"}"#,
            r#"{"topic":"theirs.test.event.sent","source":"x","origin":"peer"}"#,
        ],
    );
    assert!(eventually(DELIVERY, || seen.lock().unwrap().len() >= 2));
    sleep_ms(200);
    assert_eq!(
        *seen.lock().unwrap(),
        vec![
            "ce.test.event.sent".to_string(),
            "theirs.test.event.sent".to_string()
        ]
    );
}

/// Inbound events carry the network marker: a hub without relay does not
/// forward them, even to a peer whose filter would take them.
#[test]
fn inbound_events_are_not_forwarded_without_relay() {
    let hub = Hub::start(
        NetworkOptions::new()
            .origin_id("hub")
            .filter(TopicFilter::default()),
    );
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    let got = count_on(&b.bus, "relay.test.event.sent");
    send_raw(
        hub.url.clone(),
        vec![r#"{"topic":"relay.test.event.sent","source":"raw","origin":"raw"}"#],
    );
    sleep_ms(300);
    assert_eq!(got.load(std::sync::atomic::Ordering::SeqCst), 0);
}

/// A relaying hub never sends an event back to the peer it came from:
/// the raw peer here would accept its own frame, so only the hub's
/// exclusion keeps it away.
#[test]
fn a_relay_hub_excludes_the_origin_peer() {
    use futures_util::StreamExt;

    let hub = Hub::start(NetworkOptions::new().origin_id("hub").relay(true));
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    let b_got = count_on(&b.bus, "relay.test.event.sent");
    let url = hub.url.clone();
    let echoed = within(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            let (mut ws, _) = tokio_tungstenite::connect_async(url).await.unwrap();
            ws.send(Message::text(
                r#"{"topic":"relay.test.event.sent","source":"raw","origin":"raw-A"}"#,
            ))
            .await
            .unwrap();
            let mut echoed = Vec::new();
            let read = async {
                while let Some(Ok(m)) = ws.next().await {
                    if let Message::Text(t) = m {
                        echoed.push(t.to_string());
                    }
                }
            };
            let _ = tokio::time::timeout(Duration::from_millis(400), read).await;
            echoed
        })
    });
    assert!(echoed.is_empty(), "echoed to its origin: {echoed:?}");
    assert!(eventually(DELIVERY, || b_got
        .load(std::sync::atomic::Ordering::SeqCst)
        == 1));
}
