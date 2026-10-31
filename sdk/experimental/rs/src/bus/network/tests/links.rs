//! Links: write-failure eviction and close waiting for the readers.

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, PoisonError};
use std::thread;
use std::time::Duration;

use super::{close, eventually, linked, within};
use crate::bus::network::{NetworkAdapter, NetworkOptions};
use crate::bus::{DynBus, Event};

fn publish(bus: &DynBus, topic: &str) {
    let b = bus.clone();
    let e = Event::new(topic, "test", serde_json::Value::Null);
    let _ = within(move || b.publish(&e));
}

/// Three consecutive write failures evict an outbound link, which then
/// reconnects; two do not.
#[test]
fn three_write_failures_evict_and_reconnect_an_outbound_link() {
    let hub_bus = DynBus::new();
    let hub = NetworkAdapter::new(hub_bus.clone(), NetworkOptions::new().origin_id("hub"));
    let b_bus = DynBus::new();
    let b = NetworkAdapter::new(
        b_bus.clone(),
        NetworkOptions::new()
            .origin_id("B")
            .backoff(Duration::from_millis(20), Duration::from_millis(50)),
    );
    let url = linked(&hub, &b);

    let failing = Arc::new(AtomicUsize::new(0));
    let f = Arc::clone(&failing);
    *b.0.write_fault
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Some(Arc::new(move |_: &str| {
        // Decrement-if-positive as a CAS loop: `fetch_update` is deprecated
        // on the pinned toolchain and `try_update` is newer than the MSRV.
        let mut n = f.load(Ordering::SeqCst);
        while n > 0 {
            match f.compare_exchange(n, n - 1, Ordering::SeqCst, Ordering::SeqCst) {
                Ok(_) => return true,
                Err(cur) => n = cur,
            }
        }
        false
    }));

    // Two failures, then a success: the counter resets, the link stays.
    failing.store(2, Ordering::SeqCst);
    publish(&b_bus, "w.one");
    publish(&b_bus, "w.two");
    assert!(eventually(Duration::from_secs(2), || failing
        .load(Ordering::SeqCst)
        == 0));
    let got = Arc::new(AtomicUsize::new(0));
    let g = Arc::clone(&got);
    hub_bus.subscribe("w.ok", move |_| {
        g.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    publish(&b_bus, "w.ok");
    assert!(
        eventually(Duration::from_secs(2), || got.load(Ordering::SeqCst) == 1),
        "a success resets the failure count"
    );

    // Two more after the reset leave the link up; a third evicts it, and
    // it comes back by reconnect.
    let first = b.0.state().conns.get(&url).cloned().expect("link");
    failing.store(2, Ordering::SeqCst);
    publish(&b_bus, "w.f1");
    publish(&b_bus, "w.f2");
    assert!(eventually(Duration::from_secs(2), || failing
        .load(Ordering::SeqCst)
        == 0));
    thread::sleep(Duration::from_millis(100));
    assert!(
        !first.done.is_fired(),
        "the count restarted after the success"
    );
    failing.store(1, Ordering::SeqCst);
    publish(&b_bus, "w.f3");
    assert!(
        eventually(Duration::from_secs(2), || first.done.is_fired()),
        "three failures evict the link"
    );
    assert!(
        eventually(Duration::from_secs(5), || b
            .0
            .state()
            .conns
            .get(&url)
            .is_some_and(|l| !Arc::ptr_eq(l, &first))),
        "an evicted outbound link reconnects"
    );
    close(&b);
    close(&hub);
}

/// An evicted inbound link is gone for good: only dialers reconnect.
#[test]
fn write_failures_evict_an_inbound_link_without_reconnecting() {
    let hub_bus = DynBus::new();
    let hub = NetworkAdapter::new(hub_bus.clone(), NetworkOptions::new().origin_id("hub"));
    let b = NetworkAdapter::new(DynBus::new(), NetworkOptions::new().origin_id("B"));
    let _url = linked(&hub, &b);
    assert!(eventually(Duration::from_secs(2), || hub
        .0
        .state()
        .conns
        .len()
        == 1));
    let first = hub.0.state().conns.values().next().cloned().unwrap();
    *hub.0
        .write_fault
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Some(Arc::new(|_: &str| true));
    for t in ["x.f1", "x.f2", "x.f3"] {
        publish(&hub_bus, t);
    }
    assert!(
        eventually(Duration::from_secs(2), || first.done.is_fired()),
        "inbound link evicted"
    );
    // The dialer comes back on a new link; the hub itself never redials.
    assert!(
        hub.0.state().reconnecting.is_empty(),
        "acceptors never redial"
    );
    assert!(
        hub.0.state().peers.is_empty(),
        "acceptors register no peers"
    );
    close(&b);
    close(&hub);
}

/// Close returns only once every reader has exited: Go's `wg.Wait`.
#[test]
fn close_waits_for_the_readers() {
    let hub = NetworkAdapter::new(DynBus::new(), NetworkOptions::new().origin_id("hub"));
    let b = NetworkAdapter::new(DynBus::new(), NetworkOptions::new().origin_id("B"));
    let url = linked(&hub, &b);
    assert!(eventually(Duration::from_secs(2), || hub
        .0
        .state()
        .conns
        .len()
        == 1));
    let hub_link = hub.0.state().conns.values().next().cloned().unwrap();
    let b_link = b.0.state().conns.get(&url).cloned().unwrap();
    close(&hub);
    assert!(
        hub_link.done.is_fired(),
        "hub close returned before its reader exited"
    );
    close(&b);
    assert!(
        b_link.done.is_fired(),
        "dialer close returned before its reader exited"
    );
    assert_eq!(b.0.tasks.count(), 0, "no reconnect loop survives close");
}

/// Two inbound links under one address: the second is refused with 1000
/// `duplicate`, and close ends every reader. Go's
/// `TestNetworkAdapter_Handler_DuplicateAddrRefused`.
#[test]
fn handler_duplicate_addr_refused() {
    use futures_util::StreamExt;
    use tokio_tungstenite::tungstenite::Message;

    let hub = NetworkAdapter::new(DynBus::new(), NetworkOptions::new().origin_id("hub"));
    let h = hub.clone();
    let closes = within(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            let mut peers = Vec::new();
            for _ in 0..2 {
                let (client, server) = tokio::io::duplex(1 << 16);
                h.accept_upgraded(server, "10.0.0.1:1");
                let ws = tokio_tungstenite::WebSocketStream::from_raw_socket(
                    client,
                    tokio_tungstenite::tungstenite::protocol::Role::Client,
                    None,
                )
                .await;
                peers.push(ws);
                tokio::time::sleep(Duration::from_millis(100)).await;
            }
            let mut out = Vec::new();
            let closer = h.clone();
            let close = tokio::task::spawn_blocking(move || closer.close());
            for mut ws in peers {
                let read = async {
                    while let Some(Ok(m)) = ws.next().await {
                        if let Message::Close(f) = m {
                            return f.map(|f| (u16::from(f.code), f.reason.to_string()));
                        }
                    }
                    None
                };
                out.push(
                    tokio::time::timeout(Duration::from_secs(5), read)
                        .await
                        .unwrap_or(None),
                );
            }
            tokio::time::timeout(Duration::from_secs(10), close)
                .await
                .expect("close ends every reader")
                .unwrap()
                .unwrap();
            out
        })
    });
    assert_eq!(
        closes,
        vec![
            Some((1001, "closing".to_string())),
            Some((1000, "duplicate".to_string()))
        ]
    );
    assert_eq!(hub.0.tasks.count(), 0);
}
