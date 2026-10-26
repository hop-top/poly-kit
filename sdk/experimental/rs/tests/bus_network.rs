//! The network adapter's core: Go's `network_test.go`
//! (`TestNetworkAdapter_ConnectDisconnect`, `_Close`, `_OutboundForward`,
//! `_FilterBlocks`, `_OriginIDUniqueness`, `_LoopPrevention`) and
//! `TestWithNetwork_AutoConnects`. Go peeks at the private `conns` map;
//! these read the same state through `link_state`.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::collections::HashSet;
use std::sync::atomic::Ordering;

use bus_network_support::*;
use hop_top_kit::bus::network::{NetworkAdapter, NetworkOptions, TopicFilter};
use hop_top_kit::bus::{Context, DynBus};
use serde_json::{json, Value};

#[test]
fn with_network_auto_connects() {
    let hub = Hub::start(NetworkOptions::new().origin_id("srv"));
    let url = hub.url.clone();
    let bus_b = within(move || DynBus::builder().with_network([url]).build());
    sleep_ms(100);

    let received = flag_on(&hub.bus, "net.test");
    publish(&bus_b, ev("net.test", "B", Value::Null));
    assert!(
        eventually(DELIVERY, || received.load(Ordering::SeqCst)),
        "WithNetwork: event not delivered"
    );
    within(move || bus_b.close()).unwrap();
}

#[test]
fn network_adapter_connect_disconnect() {
    let hub = Hub::start(NetworkOptions::new().origin_id("A"));
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    assert!(
        b.net.link_state(&hub.url).is_some_and(|s| s.connected),
        "expected 1 connection"
    );

    let (n, u) = (b.net.clone(), hub.url.clone());
    within(move || n.disconnect(&u)).expect("disconnect");
    assert!(
        b.net.link_state(&hub.url).is_none(),
        "expected 0 connections after disconnect"
    );
}

#[test]
fn network_adapter_close() {
    let net = NetworkAdapter::new(DynBus::new(), NetworkOptions::new().origin_id("A"));
    let n = net.clone();
    within(move || n.close()).expect("close");
    let n = net.clone();
    within(move || n.close()).expect("double close");

    let n = net.clone();
    let err = within(move || n.connect(&Context::background(), "ws://localhost:9999"))
        .expect_err("connect after close");
    assert!(err.is_closed(), "expected the closed error, got {err}");
    assert_eq!(err.to_string(), "bus: publish after close");
}

#[test]
fn network_adapter_outbound_forward() {
    let hub = Hub::start(NetworkOptions::new().origin_id("A"));
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    sleep_ms(50);

    let received = flag_on(&hub.bus, "test.event");
    publish(&b.bus, ev("test.event", "B", json!("hello")));
    wait_for(&received, "timed out waiting for event delivery");
}

#[test]
fn network_adapter_filter_blocks() {
    let hub = Hub::start(NetworkOptions::new().origin_id("A"));
    let b = Peer::new(
        NetworkOptions::new()
            .origin_id("B")
            .filter(TopicFilter::allow(["task.*"])),
    );
    b.connect(&hub.url).expect("connect");
    sleep_ms(50);

    let received = flag_on(&hub.bus, "llm.request");
    publish(&b.bus, ev("llm.request", "B", Value::Null));
    sleep_ms(200);
    assert!(
        !received.load(Ordering::SeqCst),
        "filter should have blocked llm.request"
    );
}

#[test]
fn network_adapter_origin_id_uniqueness() {
    let mut seen = HashSet::with_capacity(1000);
    for _ in 0..1000 {
        let a = NetworkAdapter::new(DynBus::new(), NetworkOptions::new());
        let id = a.origin_id().to_string();
        assert_eq!(id.len(), 16, "origin id is 8 random bytes in hex: {id}");
        assert!(seen.insert(id.clone()), "duplicate originID: {id}");
        within(move || a.close()).unwrap();
    }
}

#[test]
fn network_adapter_loop_prevention() {
    let hub = Hub::start(NetworkOptions::new().origin_id("A"));
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    sleep_ms(50);

    let count = count_on(&hub.bus, "loop.test");
    publish(&b.bus, ev("loop.test", "B", Value::Null));
    sleep_ms(300);
    assert_eq!(
        count.load(Ordering::SeqCst),
        1,
        "expected exactly 1 delivery (loop detected)"
    );
}
