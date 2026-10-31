//! Two adapters end to end: Go's `network_e2e_test.go`
//! (`TestE2E_Bidirectional`, `_NoLoops`, `_TopicFilterSelectiveForwarding`).

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::sync::atomic::Ordering;

use bus_network_support::*;
use hop_top_kit::bus::network::{NetworkOptions, TopicFilter};
use serde_json::{json, Value};

#[test]
fn e2e_bidirectional() {
    let a = Hub::start(NetworkOptions::new().origin_id("A"));
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&a.url).expect("connect");
    sleep_ms(50);

    let b_received = flag_on(&b.bus, "from.a");
    publish(&a.bus, ev("from.a", "A", json!("hello-b")));
    wait_for(&b_received, "B should receive event from A");

    let a_received = flag_on(&a.bus, "from.b");
    publish(&b.bus, ev("from.b", "B", json!("hello-a")));
    wait_for(&a_received, "A should receive event from B");
}

#[test]
fn e2e_no_loops() {
    let a = Hub::start(NetworkOptions::new().origin_id("A"));
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&a.url).expect("connect");
    sleep_ms(50);

    let count_a = count_on(&a.bus, "ping");
    let count_b = count_on(&b.bus, "ping");
    publish(&a.bus, ev("ping", "A", Value::Null));
    sleep_ms(500);

    assert_eq!(count_a.load(Ordering::SeqCst), 1, "A count");
    assert_eq!(count_b.load(Ordering::SeqCst), 1, "B count");
}

#[test]
fn e2e_topic_filter_selective_forwarding() {
    let a = Hub::start(
        NetworkOptions::new()
            .origin_id("A")
            .filter(TopicFilter::allow(["task.*"])),
    );
    let b = Peer::new(
        NetworkOptions::new()
            .origin_id("B")
            .filter(TopicFilter::allow(["track.*"])),
    );
    b.connect(&a.url).expect("connect");
    sleep_ms(50);

    let a_track = count_on(&a.bus, "track.created");
    let a_task = count_on(&a.bus, "task.created");
    let b_track = count_on(&b.bus, "track.created");
    let b_task = count_on(&b.bus, "task.created");

    publish(&a.bus, ev("task.created", "A", Value::Null));
    publish(&b.bus, ev("track.created", "B", Value::Null));
    publish(&a.bus, ev("track.created", "A", Value::Null));
    publish(&b.bus, ev("task.created", "B", Value::Null));
    sleep_ms(500);

    assert!(
        b_task.load(Ordering::SeqCst) >= 1,
        "B should receive task.created"
    );
    assert!(
        a_track.load(Ordering::SeqCst) >= 1,
        "A should receive track.created"
    );
    // Stricter than Go: each side sees the allowed remote event once plus
    // its own local publish, and nothing the other side's filter blocked.
    assert_eq!(
        b_task.load(Ordering::SeqCst),
        2,
        "B task.created: local + A's"
    );
    assert_eq!(
        a_track.load(Ordering::SeqCst),
        2,
        "A track.created: local + B's"
    );
    assert_eq!(
        a_task.load(Ordering::SeqCst),
        1,
        "A task.created: local only"
    );
    assert_eq!(
        b_track.load(Ordering::SeqCst),
        1,
        "B track.created: local only"
    );
}
