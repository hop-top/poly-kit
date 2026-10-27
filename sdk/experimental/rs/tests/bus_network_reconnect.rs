//! Outbound links that drop come back: reconnect with backoff, one loop
//! per address, stopping on disconnect and on close, plus the duplicate
//! guard on connect.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::sync::atomic::Ordering;
use std::time::Duration;

use bus_network_support::*;
use hop_top_kit::bus::network::{NetworkAdapter, NetworkOptions};
use hop_top_kit::bus::{Context, DynBus};
use serde_json::Value;

fn fast() -> NetworkOptions {
    NetworkOptions::new().backoff(Duration::from_millis(20), Duration::from_millis(100))
}

/// A hub on a fixed address that can go away and come back.
fn hub_at(addr: &str, origin: &str) -> (DynBus, NetworkAdapter) {
    let bus = DynBus::new();
    let net = NetworkAdapter::new(bus.clone(), NetworkOptions::new().origin_id(origin));
    let (n, a) = (net.clone(), addr.to_string());
    assert!(
        eventually(Duration::from_secs(5), move || n.listen(&a).is_ok()),
        "rebind {addr}"
    );
    (bus, net)
}

#[test]
fn a_dropped_outbound_link_reconnects() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let addr = hub
        .url
        .trim_start_matches("ws://")
        .trim_end_matches('/')
        .to_string();
    let b = Peer::new(fast().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    drop(hub); // closes with 1001

    assert!(
        eventually(DELIVERY, || b
            .net
            .link_state(&format!("ws://{addr}/"))
            .is_some_and(|s| !s.connected)),
        "the link goes down"
    );
    let (bus2, net2) = hub_at(&addr, "hub2");
    assert!(
        eventually(Duration::from_secs(5), || b
            .net
            .link_state(&format!("ws://{addr}/"))
            .is_some_and(|s| s.connected)),
        "the link comes back"
    );
    let got = flag_on(&bus2, "after.reconnect");
    sleep_ms(50);
    publish(&b.bus, ev("after.reconnect", "B", Value::Null));
    wait_for(&got, "events flow after the reconnect");
    within(move || net2.close()).unwrap();
}

#[test]
fn dial_keeps_trying_until_the_peer_appears() {
    let gate = Refuser::start();
    let url = gate.url();
    let b = Peer::new(fast().origin_id("B"));
    b.net.dial(&url);
    sleep_ms(150);
    assert!(b.net.link_state(&url).is_some_and(|s| !s.connected));
    assert!(gate.accepts() > 0, "the dial reached the address");

    let addr = gate.release();
    let (bus2, net2) = hub_at(&addr, "late");
    assert!(
        eventually(Duration::from_secs(5), || b
            .net
            .link_state(&url)
            .is_some_and(|s| s.connected)),
        "dial connects once the peer listens"
    );
    let got = flag_on(&bus2, "late.hello");
    sleep_ms(50);
    publish(&b.bus, ev("late.hello", "B", Value::Null));
    wait_for(&got, "events flow after a late dial");
    within(move || net2.close()).unwrap();
}

#[test]
fn disconnect_forgets_the_address() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let b = Peer::new(fast().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    let (n, u) = (b.net.clone(), hub.url.clone());
    within(move || n.disconnect(&u)).unwrap();
    sleep_ms(300);
    assert!(
        b.net.link_state(&hub.url).is_none(),
        "no reconnect after disconnect"
    );

    let got = flag_on(&hub.bus, "gone.quiet");
    publish(&b.bus, ev("gone.quiet", "B", Value::Null));
    sleep_ms(200);
    assert!(
        !got.load(Ordering::SeqCst),
        "nothing flows to a forgotten peer"
    );

    // An unknown address is not an error.
    let n = b.net.clone();
    within(move || n.disconnect("ws://127.0.0.1:1/")).unwrap();
}

#[test]
fn close_stops_reconnecting() {
    let gate = Refuser::start();
    let url = gate.url();
    let b = NetworkAdapter::new(DynBus::new(), fast());
    b.dial(&url);
    sleep_ms(100);
    let n = b.clone();
    let started = std::time::Instant::now();
    within(move || n.close()).unwrap();
    assert!(
        started.elapsed() < Duration::from_secs(2),
        "close interrupts the backoff and the dial"
    );
    let n = b.clone();
    let err =
        within(move || n.connect(&Context::background(), "ws://127.0.0.1:1/")).expect_err("closed");
    assert!(err.is_closed());
}

#[test]
fn a_second_connect_to_a_linked_address_adds_no_link() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let b = Peer::new(NetworkOptions::new().origin_id("B"));
    b.connect(&hub.url).expect("connect");
    b.connect(&hub.url)
        .expect("duplicate connect is not an error");
    let n = count_on(&hub.bus, "dup.check");
    sleep_ms(50);
    publish(&b.bus, ev("dup.check", "B", Value::Null));
    sleep_ms(300);
    assert_eq!(n.load(Ordering::SeqCst), 1, "one link, one delivery");
}

#[test]
fn a_failed_connect_starts_no_reconnect_loop() {
    // The address stays bound for the whole test: no other listener can
    // take it between the failed connect and the check for redials.
    let gate = Refuser::start();
    assert!(
        std::net::TcpListener::bind(&gate.addr).is_err(),
        "the address is the test's own"
    );
    let url = gate.url();
    let b = Peer::new(fast().origin_id("B"));
    let err = b.connect(&url).expect_err("every dial is refused");
    assert!(
        err.to_string().starts_with("failed to WebSocket dial: "),
        "{err}"
    );
    assert!(
        b.net.link_state(&url).is_none(),
        "a failed dial registers nothing"
    );
    assert_eq!(gate.accepts(), 1, "one dial");

    sleep_ms(400);
    assert_eq!(gate.accepts(), 1, "Go: only dial and dropped links retry");
    assert!(b.net.link_state(&url).is_none(), "no redial registered");
}

/// An acceptor that upgrades every connection and closes it with 1000
/// `duplicate` after `hold`, as one does to a second client keyed under
/// a linked address. Returns its URL and an upgrade counter.
fn flapping_acceptor(hold: Duration) -> (String, std::sync::Arc<std::sync::atomic::AtomicUsize>) {
    use futures_util::SinkExt;
    use tokio_tungstenite::tungstenite::protocol::frame::coding::CloseCode;
    use tokio_tungstenite::tungstenite::protocol::CloseFrame;
    use tokio_tungstenite::tungstenite::Message;

    let ln = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("ws://{}/", ln.local_addr().unwrap());
    let accepts = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let n = std::sync::Arc::clone(&accepts);
    std::thread::spawn(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            ln.set_nonblocking(true).unwrap();
            let ln = tokio::net::TcpListener::from_std(ln).unwrap();
            while let Ok((tcp, _)) = ln.accept().await {
                let n = std::sync::Arc::clone(&n);
                tokio::spawn(async move {
                    if let Ok(mut ws) = tokio_tungstenite::accept_async(tcp).await {
                        n.fetch_add(1, Ordering::SeqCst);
                        tokio::time::sleep(hold).await;
                        let _ = ws
                            .send(Message::Close(Some(CloseFrame {
                                code: CloseCode::Normal,
                                reason: "duplicate".into(),
                            })))
                            .await;
                    }
                });
            }
        });
    });
    (url, accepts)
}

/// Subscribes a channel to `kit.runtime.link.*` on `bus`, as (topic).
fn link_events(bus: &DynBus) -> std::sync::mpsc::Receiver<String> {
    let (tx, rx) = std::sync::mpsc::channel();
    let tx = std::sync::Mutex::new(tx);
    bus.subscribe("kit.runtime.link.#", move |e| {
        let _ = tx.lock().unwrap().send(e.topic.as_str().to_string());
        Ok(())
    });
    rx
}

/// Go's `TestNetworkAdapter_RedialNeverLostUnderManyFlappingPeers`: 40
/// adapters flapping against one acceptor all keep redialing; no drop is
/// lost to a loop on its way out. Each adapter must link three times
/// (waited on through its own link events) and closes once it has.
#[test]
fn redial_never_lost_under_many_flapping_peers() {
    let (url, _) = flapping_acceptor(Duration::ZERO);
    let workers: Vec<_> = (0..40)
        .map(|i| {
            let bus = DynBus::new();
            let n = NetworkAdapter::new(
                bus.clone(),
                NetworkOptions::new().backoff(Duration::from_millis(5), Duration::from_millis(20)),
            );
            let events = link_events(&bus);
            n.dial(&url);
            std::thread::spawn(move || {
                let mut ups = 0;
                while ups < 3 {
                    match events.recv_timeout(Duration::from_secs(10)) {
                        Ok(t) if t == "kit.runtime.link.connected" => ups += 1,
                        Ok(_) => {}
                        Err(_) => break,
                    }
                }
                let _ = n.close();
                (i, ups)
            })
        })
        .collect();
    for w in workers {
        let (i, ups) = within(move || w.join().unwrap());
        assert_eq!(ups, 3, "adapter {i} stopped redialing after {ups} links");
    }
}
