//! wasm32-unknown-unknown smoke test for async dispatch, run under Node
//! by `make test-rs-wasm`.
//!
//! One thread, so tasks run only once the test yields: the pool is
//! uncapped, and close reports `NotDrained` instead of blocking until
//! the executor has run them. The same flow runs natively against the
//! single-threaded policy in src/bus/drain_tests.rs.

#![cfg(all(target_arch = "wasm32", target_os = "unknown"))]

use std::future::Future;
use std::pin::Pin;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::task::{Context as TaskCx, Poll};
use std::time::{Duration, UNIX_EPOCH};

use hop_top_kit::bus::{Bus, BusError, Context, Event, SharedBus, Spawn, Task};
use serde_json::json;
use wasm_bindgen_test::wasm_bindgen_test;

/// Yields to the executor once.
struct YieldNow(bool);

impl Future for YieldNow {
    type Output = ();

    fn poll(mut self: Pin<&mut Self>, cx: &mut TaskCx<'_>) -> Poll<()> {
        if self.0 {
            return Poll::Ready(());
        }
        self.0 = true;
        cx.waker().wake_by_ref();
        Poll::Pending
    }
}

/// Yields until `cond` holds; the bound stands in for a timeout.
async fn until(what: &str, cond: impl Fn() -> bool) {
    for _ in 0..1000 {
        if cond() {
            return;
        }
        YieldNow(false).await;
    }
    panic!("never happened: {what}");
}

#[wasm_bindgen_test]
async fn async_subscribe_publish_and_drain_on_one_thread() {
    let spawner: Arc<dyn Spawn> = Arc::new(|t: Task| wasm_bindgen_futures::spawn_local(t));
    let bus = SharedBus::new(Bus::builder().max_async(1).build());
    let ran = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&ran);
    bus.subscribe_async("test.event", spawner, move |_, e: Event| {
        let r = Arc::clone(&r);
        async move {
            // Futures need not be Send here.
            let topic = std::rc::Rc::new(e.topic.to_string());
            YieldNow(false).await;
            assert_eq!(*topic, "test.event");
            r.fetch_add(1, Ordering::SeqCst);
        }
    });

    // More publishes than slots: none blocks the only thread.
    for _ in 0..3 {
        // No clock on this target: the host supplies the time.
        let at = UNIX_EPOCH + Duration::from_secs(1_700_000_000);
        let event = Event::new_at("test.event", "src", json!(null), at);
        bus.publish(&event).unwrap();
    }
    assert_eq!(bus.async_in_flight(), 3);
    assert_eq!(ran.load(Ordering::SeqCst), 0, "tasks wait for a yield");

    let err = bus.close_ctx(&Context::background()).unwrap_err();
    assert!(matches!(err, BusError::NotDrained(3)), "{err:?}");
    assert!(bus.is_closed());

    until("delivered", || ran.load(Ordering::SeqCst) == 3).await;
    until("slots freed", || bus.async_in_flight() == 0).await;
    bus.close_ctx(&Context::background()).expect("drained");
}
