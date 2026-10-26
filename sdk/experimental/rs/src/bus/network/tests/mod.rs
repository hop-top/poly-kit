//! Unit tests over the adapter internals Go's tests read directly: the
//! reconnect root, write-failure eviction, the handshake bounds and the
//! task accounting.

use std::sync::mpsc;
use std::thread;
use std::time::{Duration, Instant};

use super::NetworkAdapter;
use crate::bus::context::Context;

mod links;

pub(super) fn within<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(f());
    });
    rx.recv_timeout(Duration::from_secs(20))
        .expect("call deadlocked")
}

pub(super) fn eventually(limit: Duration, cond: impl Fn() -> bool) -> bool {
    let deadline = Instant::now() + limit;
    while !cond() {
        if Instant::now() >= deadline {
            return false;
        }
        thread::sleep(Duration::from_millis(10));
    }
    true
}

pub(super) fn close(n: &NetworkAdapter) {
    let n = n.clone();
    within(move || n.close()).unwrap();
}

/// A hub and a dialer `b` linked to it; returns the hub's URL.
pub(super) fn linked(hub: &NetworkAdapter, b: &NetworkAdapter) -> String {
    let h = hub.clone();
    let addr = within(move || h.listen("127.0.0.1:0")).unwrap();
    let url = format!("ws://{addr}/");
    let (bb, u) = (b.clone(), url.clone());
    within(move || bb.connect(&Context::background(), &u)).expect("connect");
    url
}
