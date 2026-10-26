//! Shared helpers for the bus network tests. Every wait is bounded: a
//! regression fails an assertion instead of hanging the suite.

#![allow(dead_code)]

use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{mpsc, Arc};
use std::thread;
use std::time::{Duration, Instant};

use hop_top_kit::bus::network::{NetworkAdapter, NetworkOptions};
use hop_top_kit::bus::{DynBus, Event};
use serde_json::Value;

/// The watchdog for any call that may block.
pub const WATCHDOG: Duration = Duration::from_secs(20);

/// Go's `waitFor` deadline.
pub const DELIVERY: Duration = Duration::from_secs(2);

/// Runs `f` on another thread, failing the test if it has not returned
/// before the watchdog fires.
pub fn within<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(f());
    });
    rx.recv_timeout(WATCHDOG).expect("call deadlocked")
}

/// Polls `cond` every 10 ms until it holds or `limit` passes.
pub fn eventually(limit: Duration, cond: impl Fn() -> bool) -> bool {
    let deadline = Instant::now() + limit;
    loop {
        if cond() {
            return true;
        }
        if Instant::now() >= deadline {
            return false;
        }
        thread::sleep(Duration::from_millis(10));
    }
}

/// Go's `waitFor`: fails with `msg` unless `flag` is set within 2 s.
pub fn wait_for(flag: &AtomicBool, msg: &str) {
    assert!(
        eventually(DELIVERY, || flag.load(Ordering::SeqCst)),
        "timeout: {msg}"
    );
}

pub fn sleep_ms(ms: u64) {
    thread::sleep(Duration::from_millis(ms));
}

/// An event like Go's `NewEvent`.
pub fn ev(topic: &str, source: &str, payload: Value) -> Event {
    Event::new(topic, source, payload)
}

/// Publishes on `bus` under the watchdog.
pub fn publish(bus: &DynBus, e: Event) {
    let b = bus.clone();
    let _ = within(move || b.publish(&e));
}

/// Subscribes a flag setter on `pattern`.
pub fn flag_on(bus: &DynBus, pattern: &str) -> Arc<AtomicBool> {
    let flag = Arc::new(AtomicBool::new(false));
    let f = Arc::clone(&flag);
    bus.subscribe(pattern, move |_| {
        f.store(true, Ordering::SeqCst);
        Ok(())
    });
    flag
}

/// Subscribes a counter on `pattern`.
pub fn count_on(bus: &DynBus, pattern: &str) -> Arc<AtomicUsize> {
    let n = Arc::new(AtomicUsize::new(0));
    let c = Arc::clone(&n);
    bus.subscribe(pattern, move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    n
}

/// A serving adapter: Go's `httptest.NewServer(adapter.Handler())`.
pub struct Hub {
    pub bus: DynBus,
    pub net: NetworkAdapter,
    pub url: String,
}

impl Hub {
    pub fn start(opts: NetworkOptions) -> Hub {
        let bus = DynBus::new();
        let net = NetworkAdapter::new(bus.clone(), opts);
        let n = net.clone();
        let addr = within(move || n.listen("127.0.0.1:0")).expect("listen");
        Hub {
            bus,
            net,
            url: format!("ws://{addr}/"),
        }
    }
}

impl Drop for Hub {
    fn drop(&mut self) {
        let n = self.net.clone();
        let _ = within(move || n.close());
    }
}

/// A dialing adapter that closes on drop.
pub struct Peer {
    pub bus: DynBus,
    pub net: NetworkAdapter,
}

impl Peer {
    pub fn new(opts: NetworkOptions) -> Peer {
        let bus = DynBus::new();
        let net = NetworkAdapter::new(bus.clone(), opts);
        Peer { bus, net }
    }

    /// Connects with a background context under the watchdog.
    pub fn connect(&self, url: &str) -> Result<(), hop_top_kit::bus::network::NetworkError> {
        let n = self.net.clone();
        let u = url.to_string();
        within(move || n.connect(&hop_top_kit::bus::Context::background(), &u))
    }
}

impl Drop for Peer {
    fn drop(&mut self) {
        let n = self.net.clone();
        let _ = within(move || n.close());
    }
}
