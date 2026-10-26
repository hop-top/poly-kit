//! Star-topology relay: Go's `network_relay_test.go`
//! (`TestNetworkAdapter_Relay_3Node_Fanout`, `_OriginExcluded`,
//! `_Disabled_Default`).

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::sync::atomic::Ordering;

use bus_network_support::*;
use hop_top_kit::bus::network::NetworkOptions;
use serde_json::{json, Value};

#[test]
fn network_adapter_relay_3node_fanout() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub").relay(true));
    let a = Peer::new(NetworkOptions::new().origin_id("peer-A"));
    a.connect(&hub.url).expect("A connect");
    let b = Peer::new(NetworkOptions::new().origin_id("peer-B"));
    b.connect(&hub.url).expect("B connect");
    sleep_ms(150);

    let b_got = count_on(&b.bus, "test.ping");
    publish(&a.bus, ev("test.ping", "A", json!("hello")));
    assert!(
        eventually(DELIVERY, || b_got.load(Ordering::SeqCst) > 0),
        "relay: B did not receive A's event through hub"
    );
    sleep_ms(200);
    assert_eq!(
        b_got.load(Ordering::SeqCst),
        1,
        "relay fanout: exactly 1 delivery to B"
    );
}

#[test]
fn network_adapter_relay_origin_excluded() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub").relay(true));
    let a = Peer::new(NetworkOptions::new().origin_id("peer-A"));
    a.connect(&hub.url).expect("A connect");
    let b = Peer::new(NetworkOptions::new().origin_id("peer-B"));
    b.connect(&hub.url).expect("B connect");
    sleep_ms(150);

    let a_got = count_on(&a.bus, "echo.test");
    publish(&a.bus, ev("echo.test", "A", Value::Null));
    sleep_ms(400);
    assert_eq!(
        a_got.load(Ordering::SeqCst),
        1,
        "origin exclusion: local only, no echo"
    );
}

#[test]
fn network_adapter_relay_disabled_default() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let a = Peer::new(NetworkOptions::new().origin_id("peer-A"));
    a.connect(&hub.url).expect("A connect");
    let b = Peer::new(NetworkOptions::new().origin_id("peer-B"));
    b.connect(&hub.url).expect("B connect");
    sleep_ms(100);

    let b_got = count_on(&b.bus, "noreplay.ping");
    publish(&a.bus, ev("noreplay.ping", "A", Value::Null));
    sleep_ms(300);
    assert_eq!(
        b_got.load(Ordering::SeqCst),
        0,
        "relay-off default: B should not receive A's event"
    );
}
