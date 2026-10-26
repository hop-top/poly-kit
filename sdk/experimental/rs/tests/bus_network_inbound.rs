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

/// A relaying hub never sends an event back to the link it arrived on,
/// whatever origin the frame claims: Go's
/// `TestNetworkAdapter_Relay_NoEchoToArrivalLink`. The raw peer would
/// accept its own frame, so only the hub's exclusion keeps it away.
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
            // The hub learns this link's origin, raw-A, from the first
            // frame; the second claims another origin.
            ws.send(Message::text(
                r#"{"topic":"warmup.test.event.sent","source":"raw","origin":"raw-A"}"#,
            ))
            .await
            .unwrap();
            ws.send(Message::text(
                r#"{"topic":"relay.test.event.sent","source":"raw","origin":"spoofed"}"#,
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

/// A frame without an origin must not loop around a ring of relay hubs:
/// Go's `TestNetworkAdapter_Relay_EmptyOriginNoLoop`. Three hubs each
/// dial the next; the counts must settle at 1 or 2 per hub (a mesh may
/// deliver twice, a loop never stops).
#[test]
fn relay_empty_origin_no_loop() {
    use hop_top_kit::bus::Context;
    use std::sync::atomic::Ordering;

    let hubs: Vec<Hub> = (0..3)
        .map(|i| Hub::start(NetworkOptions::new().origin_id(format!("h{i}")).relay(true)))
        .collect();
    let counts: Vec<_> = hubs
        .iter()
        .map(|h| count_on(&h.bus, "loop.test.event.sent"))
        .collect();
    for i in 0..3 {
        let (n, u) = (hubs[i].net.clone(), hubs[(i + 1) % 3].url.clone());
        within(move || n.connect(&Context::background(), &u)).expect("ring link");
    }
    send_raw(
        hubs[0].url.clone(),
        vec![r#"{"topic":"loop.test.event.sent","source":"raw","origin":""}"#],
    );
    sleep_ms(200);
    let before: Vec<usize> = counts.iter().map(|c| c.load(Ordering::SeqCst)).collect();
    sleep_ms(300);
    for (i, c) in counts.iter().enumerate() {
        let got = c.load(Ordering::SeqCst);
        assert!(
            (1..=2).contains(&got) && got == before[i],
            "hub {i}: {got} deliveries ({} 300 ms earlier), want 1 or 2 and settled",
            before[i]
        );
    }
}
