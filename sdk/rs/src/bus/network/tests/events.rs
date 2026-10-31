//! The adapter's own events where Go's tests reach inside: an ack
//! timeout and a dropped link.

use std::sync::{mpsc, Mutex, PoisonError};
use std::time::Duration;

use serde_json::{json, Value};

use super::{close, linked, within};
use crate::bus::context::Context;
use crate::bus::network::conn::STATUS_GOING_AWAY;
use crate::bus::network::{NetworkAdapter, NetworkOptions, OpenAuth};
use crate::bus::DynBus;

/// Every `kit.runtime.*` event `bus` delivers, as (topic, payload).
fn collect(bus: &DynBus) -> mpsc::Receiver<(String, Value)> {
    let (tx, rx) = mpsc::channel();
    let tx = Mutex::new(tx);
    bus.subscribe("kit.runtime.#", move |e| {
        let _ = tx
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .send((e.topic.as_str().to_string(), e.payload.clone()));
        Ok(())
    });
    rx
}

fn next_topic(rx: &mpsc::Receiver<(String, Value)>, want: &str) -> Value {
    let (topic, payload) = rx
        .recv_timeout(Duration::from_secs(5))
        .unwrap_or_else(|_| panic!("no event, want {want}"));
    assert_eq!(topic, want, "payload {payload}");
    payload
}

/// Go's `TestHandshake_AckTimeoutIsNotAnAuthFailure`: a missing ack is a
/// retryable `timeout`, not `auth`.
#[test]
fn handshake_ack_timeout_is_not_an_auth_failure() {
    let (addr, _rx) = super::handshake::scripted_acceptor(None);
    let bus = DynBus::new();
    let n = NetworkAdapter::new(bus.clone(), NetworkOptions::new().auth(OpenAuth));
    *n.0.handshake_timeout
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Duration::from_millis(50);
    let events = collect(&bus);
    let nn = n.clone();
    let url = format!("ws://{addr}/");
    let u = url.clone();
    let err = within(move || nn.connect(&Context::background(), &u)).expect_err("no ack");
    assert!(
        matches!(err, super::super::NetworkError::AckTimeout),
        "{err:?}"
    );
    assert_eq!(
        next_topic(&events, "kit.runtime.link.disconnected"),
        json!({"peer": url, "final": false, "code": "timeout"})
    );
    close(&n);
}

/// Go's `TestResume_ReconnectToLegacyInvalidates`: a link without
/// positions that comes back has missed whatever went by meanwhile.
#[test]
fn resume_reconnect_to_legacy_invalidates() {
    let hub = NetworkAdapter::new(DynBus::new(), NetworkOptions::new().origin_id("hub"));
    let bus = DynBus::new();
    let cl = NetworkAdapter::new(
        bus.clone(),
        NetworkOptions::new().backoff(Duration::from_millis(1), Duration::from_millis(1)),
    );
    let events = collect(&bus);
    let url = linked(&hub, &cl);
    next_topic(&events, "kit.runtime.link.connected");
    // Go's dropLink: close the live link as a network failure would.
    let link = cl.0.state().conns.get(&url).cloned().expect("link");
    link.close(STATUS_GOING_AWAY, "dropped");
    assert_eq!(
        next_topic(&events, "kit.runtime.link.disconnected"),
        json!({"peer": url, "final": false})
    );
    next_topic(&events, "kit.runtime.link.connected");
    assert_eq!(
        next_topic(&events, "kit.runtime.stream.invalidated"),
        json!({"peer": url, "reason": "unsupported", "stream": "", "after": "", "head": ""})
    );
    close(&cl);
    close(&hub);
}

/// Close while a reconnect is publishing its `link.connected`: the link
/// is registered and counted by then, so its reader must still start
/// and end, or close waits forever.
#[test]
fn close_during_a_reconnect_link_up_does_not_hang() {
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::Arc;

    let hub = NetworkAdapter::new(DynBus::new(), NetworkOptions::new().origin_id("hub"));
    let bus = DynBus::new();
    let cl = NetworkAdapter::new(
        bus.clone(),
        NetworkOptions::new().backoff(Duration::from_millis(1), Duration::from_millis(1)),
    );
    let (entered_tx, entered) = mpsc::channel();
    let (release, released) = mpsc::channel::<()>();
    let (entered_tx, released) = (Mutex::new(entered_tx), Mutex::new(released));
    let seen = Arc::new(AtomicUsize::new(0));
    let s = Arc::clone(&seen);
    bus.subscribe("kit.runtime.link.connected", move |_| {
        // Hold the reconnect's link-up, not the first connect's.
        if s.fetch_add(1, Ordering::SeqCst) == 1 {
            let _ = entered_tx
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .send(());
            let _ = released
                .lock()
                .unwrap_or_else(PoisonError::into_inner)
                .recv_timeout(Duration::from_secs(10));
        }
        Ok(())
    });
    let url = linked(&hub, &cl);
    let link = cl.0.state().conns.get(&url).cloned().expect("link");
    link.close(super::super::conn::STATUS_GOING_AWAY, "dropped");
    entered
        .recv_timeout(Duration::from_secs(5))
        .expect("the reconnect publishes link.connected");
    let c = cl.clone();
    let closing = std::thread::spawn(move || c.close());
    std::thread::sleep(Duration::from_millis(100));
    release.send(()).unwrap();
    within(move || closing.join().unwrap()).expect("close returns");
    assert_eq!(cl.0.tasks.count(), 0);
    close(&hub);
}
