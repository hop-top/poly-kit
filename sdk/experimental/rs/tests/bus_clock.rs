//! The event timestamp source.
//!
//! `Event::new` reads the system clock, which panics on
//! `wasm32-unknown-unknown`. `Event::new_at` takes the instant from the
//! caller and is the constructor that exists on every target.

#![cfg(feature = "bus")]

use std::time::{Duration, UNIX_EPOCH};

use hop_top_kit::bus::Event;
use serde_json::json;

#[test]
fn new_at_stamps_the_given_instant() {
    let at = UNIX_EPOCH + Duration::from_millis(1_700_000_000_123);
    let e = Event::new_at("crm.sales.deal.created", "crm", json!({"id": 1}), at);
    assert_eq!(e.timestamp, "2023-11-14T22:13:20.123Z");
    assert_eq!(e.topic.as_str(), "crm.sales.deal.created");
    assert_eq!(e.source, "crm");
    assert_eq!(e.payload, json!({"id": 1}));
    assert!(e.workspace_id.is_empty());
}

#[test]
fn new_at_truncates_to_milliseconds() {
    let at = UNIX_EPOCH + Duration::new(86_400, 999_999_999);
    let e = Event::new_at("a.b.c.created", "s", json!(null), at);
    assert_eq!(e.timestamp, "1970-01-02T00:00:00.999Z");
}

#[test]
fn new_at_clamps_pre_epoch_instants_to_the_epoch() {
    let at = UNIX_EPOCH - Duration::from_secs(1);
    let e = Event::new_at("a.b.c.created", "s", json!(null), at);
    assert_eq!(e.timestamp, "1970-01-01T00:00:00.000Z");
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[test]
fn new_matches_new_at_with_the_current_time() {
    let before = Event::new_at(
        "a.b.c.created",
        "s",
        json!(null),
        std::time::SystemTime::now(),
    );
    let e = Event::new("a.b.c.created", "s", json!(null));
    let after = Event::new_at(
        "a.b.c.created",
        "s",
        json!(null),
        std::time::SystemTime::now(),
    );
    // Same fixed-width format, so string order is time order.
    assert!(before.timestamp <= e.timestamp && e.timestamp <= after.timestamp);
}
