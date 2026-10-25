//! Go's `New` options reach the [`DynBus`] they build: `WithEnforce`,
//! `WithInvalidTopicReporter` and `WithMaxAsync`, with no adapter and
//! with a [`MemoryAdapter`]. Also the kit publisher traits on
//! [`DynBus`]. Every call that can block runs under the watchdog.

#![cfg(feature = "bus")]

#[path = "bus_async/exec.rs"]
mod exec;

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{mpsc, Arc};
use std::thread;
use std::time::Duration;

use exec::{ev, within, Manual, WATCHDOG};
use hop_top_kit::bus::{DynBus, DynBusBuilder, MemoryAdapter, Mode};

/// Counts reporter calls and deliveries on a bus from `builder`, after
/// a publish of an invalid topic; returns (result ok, reported, delivered).
fn publish_invalid(builder: DynBusBuilder) -> (bool, usize, usize) {
    let reported = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&reported);
    let bus = builder
        .invalid_topic_reporter(move |_| {
            r.fetch_add(1, Ordering::SeqCst);
        })
        .build();
    let delivered = Arc::new(AtomicUsize::new(0));
    let d = Arc::clone(&delivered);
    bus.subscribe("#", move |_| {
        d.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let b = bus.clone();
    let res = within(move || b.publish(&ev("Bad Topic")));
    if let Err(e) = &res {
        assert!(e.is_invalid_topic(), "got {e}");
    }
    (
        res.is_ok(),
        reported.load(Ordering::SeqCst),
        delivered.load(Ordering::SeqCst),
    )
}

#[test]
fn strict_and_reporter_reach_the_default_bus() {
    let b = DynBus::builder().enforce(Mode::Strict);
    assert_eq!(publish_invalid(b), (false, 1, 0));
}

#[test]
fn warn_reporter_reaches_the_default_bus() {
    assert_eq!(publish_invalid(DynBus::builder()), (true, 1, 1));
}

#[test]
fn off_reaches_the_default_bus() {
    let b = DynBus::builder().enforce(Mode::Off);
    assert_eq!(publish_invalid(b), (true, 0, 1));
}

#[test]
fn strict_and_reporter_reach_the_adapter_path() {
    let b = DynBus::builder()
        .with_adapter(MemoryAdapter::new())
        .enforce(Mode::Strict);
    assert_eq!(publish_invalid(b), (false, 1, 0));
}

#[test]
fn warn_reporter_reaches_the_adapter_path() {
    let b = DynBus::builder().with_adapter(MemoryAdapter::new());
    assert_eq!(publish_invalid(b), (true, 1, 1));
}

/// Publishes two events to one async subscriber whose tasks queue on a
/// [`Manual`] spawner, and reports whether the second publish blocked
/// on a full pool until the first task ran.
fn second_publish_blocks(bus: DynBus) -> bool {
    let manual = Manual::default();
    bus.subscribe_async("x", manual.spawner(), |_, _| async {});
    let b = bus.clone();
    within(move || b.publish(&ev("x"))).unwrap();

    let (tx, rx) = mpsc::channel();
    let b = bus.clone();
    thread::spawn(move || {
        let _ = tx.send(b.publish(&ev("x")));
    });
    let early = rx.recv_timeout(Duration::from_millis(200)).ok();
    let blocked = early.is_none();
    manual.run();
    let res = match early {
        Some(res) => res,
        None => rx
            .recv_timeout(WATCHDOG)
            .expect("publish stayed blocked after a slot freed"),
    };
    res.unwrap();
    manual.run();
    let b = bus.clone();
    within(move || b.close()).unwrap();
    blocked
}

#[test]
fn max_async_reaches_the_default_bus() {
    assert!(second_publish_blocks(
        DynBus::builder().max_async(1).build()
    ));
}

#[test]
fn max_async_does_not_apply_to_an_adapter() {
    // Go's WithMaxAsync applies only without an adapter; a
    // MemoryAdapter keeps the default pool.
    let bus = DynBus::builder()
        .with_adapter(MemoryAdapter::new())
        .max_async(1)
        .build();
    assert!(!second_publish_blocks(bus));
}

#[cfg(feature = "config")]
#[test]
fn dyn_bus_is_a_reload_publisher() {
    use hop_top_kit::config::ReloadPublisher;
    let bus = DynBus::new();
    let hits = Arc::new(AtomicUsize::new(0));
    let h = Arc::clone(&hits);
    bus.subscribe("kit.config.snapshot.reloaded", move |e| {
        assert_eq!(e.source, "cfg");
        h.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let p: Arc<dyn ReloadPublisher> = Arc::new(bus.clone());
    let res =
        within(move || p.publish("kit.config.snapshot.reloaded", "cfg", serde_json::json!({})));
    assert_eq!(res, Ok(()));
    assert_eq!(hits.load(Ordering::SeqCst), 1);

    // Errors surface as their text.
    let closed: Arc<dyn ReloadPublisher> = Arc::new(bus.clone());
    within(move || bus.close()).unwrap();
    let err = within(move || closed.publish("x", "s", serde_json::json!(null)));
    assert_eq!(err, Err("bus: publish after close".to_string()));
}

#[cfg(feature = "serve")]
#[test]
fn dyn_bus_is_a_serve_publisher() {
    use hop_top_kit::serve::{EventPayload, Publisher};
    let bus = DynBus::new();
    let got = Arc::new(std::sync::Mutex::new(None));
    let g = Arc::clone(&got);
    bus.subscribe("kit.serve.service.started", move |e| {
        *g.lock().unwrap() = Some(e.payload.clone());
        Ok(())
    });
    let p: Arc<dyn Publisher> = Arc::new(bus.clone());
    let payload = EventPayload::default();
    let want = payload.to_json();
    within(move || p.publish("kit.serve.service.started", "svc", &payload));
    assert_eq!(got.lock().unwrap().clone(), Some(want));
}
