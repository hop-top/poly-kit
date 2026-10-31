//! The offline policy on the reconnect root, and one reconnect loop per
//! address.

use std::net::TcpListener;
use std::sync::mpsc;
use std::thread;
use std::time::Duration;

use super::{close, within};
use crate::bus::context::Context;
use crate::bus::network::adapter::policy;
use crate::bus::network::{NetworkAdapter, NetworkOptions};
use crate::bus::DynBus;
use crate::netpolicy::NetPolicy;

/// A listener that accepts and closes at once.
fn bus_listener() -> (u16, impl Fn() -> bool) {
    let ln = TcpListener::bind("127.0.0.1:0").expect("listen");
    let port = ln.local_addr().unwrap().port();
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        for c in ln.incoming() {
            if tx.send(()).is_err() {
                return;
            }
            drop(c);
        }
    });
    (port, move || {
        rx.recv_timeout(Duration::from_millis(200)).is_ok()
    })
}

/// Go's `TestNetworkAdapter_ReconnectInheritsOfflinePolicy`: the
/// reconnect root must carry the offline marker a connect saw, or a
/// dropped peer would be re-dialed on a run that asked for no network.
#[test]
fn network_adapter_reconnect_inherits_offline_policy() {
    let (port, reached) = bus_listener();
    let n = NetworkAdapter::new(DynBus::new(), NetworkOptions::new());
    let addr = format!("ws://kit-offline-probe.invalid:{port}/bus");
    let (ctx, _) = Context::background()
        .with_value(NetPolicy::offline())
        .with_timeout(Duration::from_secs(5));
    let (nn, a) = (n.clone(), addr.clone());
    let err = within(move || nn.connect(&ctx, &a)).expect_err("offline Connect not refused");
    assert!(err.as_offline().is_some(), "{err}");

    let root = n.0.base_ctx();
    assert!(
        policy(&root).is_offline(),
        "reconnect root dropped the offline marker"
    );
    let (nn, a) = (n.clone(), addr.clone());
    let err = within(move || nn.connect(&root, &a)).expect_err("reconnect-context Connect");
    assert!(err.as_offline().is_some(), "{err}");

    // Sticky: a later online connect does not clear it.
    let (nn, a) = (n.clone(), addr);
    let _ = within(move || nn.connect(&Context::background(), &a));
    assert!(
        policy(&n.0.base_ctx()).is_offline(),
        "offline policy not sticky"
    );
    assert!(
        !reached(),
        "reconnect reached a listener despite offline policy"
    );
    close(&n);
}

/// Go's `TestNetworkAdapter_OnlineReconnectRootStaysClean`.
#[test]
fn network_adapter_online_reconnect_root_stays_clean() {
    let n = NetworkAdapter::new(DynBus::new(), NetworkOptions::new());
    assert!(
        !policy(&n.0.base_ctx()).is_offline(),
        "reconnect root tagged offline without an offline Connect"
    );
    close(&n);
}

/// One reconnect loop per address, however many times it is started.
#[test]
fn reconnect_loops_are_deduplicated() {
    // Held to the end: every dial fails, and no other listener can take
    // the port, as one can a port freed by dropping a probe listener.
    let (port, _held) = bus_listener();
    let url = format!("ws://127.0.0.1:{port}/");
    let n = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().backoff(Duration::from_millis(50), Duration::from_millis(50)),
    );
    n.dial(&url);
    n.dial(&url);
    n.0.go_reconnect(&url);
    assert_eq!(n.0.state().reconnecting.len(), 1);
    assert_eq!(n.0.tasks.count(), 1, "one loop task");
    close(&n);
    assert_eq!(n.0.tasks.count(), 0, "close waits for the loop");
}

/// With `bus-network-webpki-roots`, Mozilla's roots join the platform's.
#[cfg(feature = "bus-network-webpki-roots")]
#[test]
fn the_bundled_roots_join_the_default_store() {
    let mut native = rustls::RootCertStore::empty();
    for cert in rustls_native_certs::load_native_certs().certs {
        let _ = native.add(cert);
    }
    let roots = crate::bus::network::session::default_roots();
    assert_eq!(
        roots.len(),
        native.len() + webpki_roots::TLS_SERVER_ROOTS.len(),
        "the platform's roots plus Mozilla's"
    );
}

/// Go's `TestNetworkAdapter_ReconnectBackoffResetsAfterAStableLink`: a
/// link that stayed up for the stability window (the maximum backoff)
/// resets its address's backoff; a flapping one keeps growing it.
#[test]
fn reconnect_backoff_resets_after_a_stable_link() {
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::Arc;
    use std::time::Instant;

    use futures_util::SinkExt;
    use tokio_tungstenite::tungstenite::Message;

    fn acceptor(hold: Duration) -> (String, Arc<AtomicUsize>) {
        let ln = TcpListener::bind("127.0.0.1:0").unwrap();
        let url = format!("ws://{}/", ln.local_addr().unwrap());
        let accepts = Arc::new(AtomicUsize::new(0));
        let n = Arc::clone(&accepts);
        thread::spawn(move || {
            let rt = tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .unwrap();
            rt.block_on(async move {
                ln.set_nonblocking(true).unwrap();
                let ln = tokio::net::TcpListener::from_std(ln).unwrap();
                while let Ok((tcp, _)) = ln.accept().await {
                    let n = Arc::clone(&n);
                    tokio::spawn(async move {
                        if let Ok(mut ws) = tokio_tungstenite::accept_async(tcp).await {
                            n.fetch_add(1, Ordering::SeqCst);
                            tokio::time::sleep(hold).await;
                            let _ = ws.send(Message::Close(None)).await;
                        }
                    });
                }
            });
        });
        (url, accepts)
    }

    let (short, _) = acceptor(Duration::ZERO);
    let (long, long_accepts) = acceptor(Duration::from_millis(150));
    let n = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().backoff(Duration::from_millis(10), Duration::from_millis(100)),
    );
    n.dial(&short);
    n.dial(&long);
    let deadline = Instant::now() + Duration::from_secs(5);
    while long_accepts.load(Ordering::SeqCst) < 3 {
        assert!(
            Instant::now() < deadline,
            "the stable peer stopped reconnecting"
        );
        thread::sleep(Duration::from_millis(10));
    }
    let flaps = |addr: &str| n.0.state().peers.get(addr).map(|p| p.flaps);
    assert_eq!(
        flaps(&long),
        Some(0),
        "a link up 150 ms (window 100 ms) resets"
    );
    assert!(
        flaps(&short).is_some_and(|f| f >= 2),
        "a flapping link keeps growing: {:?}",
        flaps(&short)
    );
    close(&n);
}
