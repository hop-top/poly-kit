//! The offline policy on dials: Go's `network_offline_test.go`
//! (`TestNetworkAdapter_OfflineRefusesRemotePeer`,
//! `_RefusalIndependentOfDefaultTransport`, `_OfflineAllowsLoopbackPeer`).
//! `_ReconnectInheritsOfflinePolicy` and `_OnlineReconnectRootStaysClean`
//! read the adapter's private reconnect root, as in Go; they are unit
//! tests in `src/bus/network/tests.rs`.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::net::TcpListener;
use std::sync::mpsc;
use std::thread;
use std::time::Duration;

use bus_network_support::*;
use hop_top_kit::bus::network::{NetworkAdapter, NetworkError, NetworkOptions};
use hop_top_kit::bus::{Context, DynBus};
use hop_top_kit::netpolicy::NetPolicy;

/// Every connect below is bounded; a regression that fails to block
/// must fail an assertion, never hang.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(5);

/// A listener that accepts and closes at once; reaching it proves the
/// dial was not blocked. Returns the port and a probe.
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

fn offline_ctx() -> Context {
    let (ctx, _) = Context::background()
        .with_value(NetPolicy::offline())
        .with_timeout(CONNECT_TIMEOUT);
    ctx
}

fn adapter() -> NetworkAdapter {
    NetworkAdapter::new(DynBus::new(), NetworkOptions::new())
}

fn connect(n: &NetworkAdapter, ctx: Context, addr: String) -> Result<(), NetworkError> {
    let n = n.clone();
    within(move || n.connect(&ctx, &addr))
}

#[test]
fn network_adapter_offline_refuses_remote_peer() {
    let (port, reached) = bus_listener();
    let n = adapter();
    let addr = format!("ws://kit-offline-probe.invalid:{port}/bus");
    let err = connect(&n, offline_ctx(), addr).expect_err("offline Connect not refused");
    let off = err.as_offline().expect("refusal is the offline error");
    assert_eq!(
        err.to_string(),
        format!(
            "failed to WebSocket dial: GET http://kit-offline-probe.invalid:{port}/bus: \
             network disabled by --offline"
        )
    );
    assert_eq!(off.method, "GET");
    assert!(
        !reached(),
        "Connect reached a listener despite offline context"
    );
    within(move || n.close()).unwrap();
}

/// Go swaps `http.DefaultTransport` for an unguarded one. Rust has no
/// process-wide transport; the equivalent pin is that the refusal comes
/// from the policy check on the address, before name resolution: an
/// unresolvable name is refused as offline, never as a DNS failure.
#[test]
fn network_adapter_refusal_independent_of_default_transport() {
    let (port, reached) = bus_listener();
    let n = adapter();
    for addr in [
        format!("ws://kit-offline-probe.invalid:{port}/bus"),
        format!("wss://kit-offline-probe.invalid:{port}/bus"),
        format!("http://kit-offline-probe.invalid:{port}/bus"),
    ] {
        let err = connect(&n, offline_ctx(), addr.clone()).expect_err(&addr);
        assert!(err.as_offline().is_some(), "{addr}: {err}");
    }
    assert!(!reached(), "Connect reached a listener");
    within(move || n.close()).unwrap();
}

#[test]
fn network_adapter_offline_allows_loopback_peer() {
    let (port, reached) = bus_listener();
    let n = adapter();
    let addr = format!("ws://127.0.0.1:{port}/bus");
    if let Err(err) = connect(&n, offline_ctx(), addr) {
        assert!(
            err.as_offline().is_none(),
            "loopback peer refused while offline: {err}"
        );
    }
    assert!(reached(), "loopback Connect never reached the listener");
    within(move || n.close()).unwrap();
}
