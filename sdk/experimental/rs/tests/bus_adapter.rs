//! The adapter seam: [`Adapter`], [`MemoryAdapter`] and the composed
//! [`DynBus`] the builder returns.
//!
//! Ports from `go/runtime/bus`: `TestMemoryAdapter_ImplementsAdapter`,
//! `TestMemoryAdapter_PublishSubscribe`, `TestMemoryAdapter_AsyncHandler`,
//! `TestWithAdapter_MemoryAdapter`, `TestNew_DefaultStillWorks` and
//! `TestNewNoArgs`, plus pins for the Rust seams: enforcement on the
//! adapter path, the network hook and unsubscribe through the layers.
//! Every call that can block runs under the `within` watchdog.

#![cfg(feature = "bus")]

#[path = "bus_async/exec.rs"]
mod exec;

use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use exec::{ev, threads, within};
use hop_top_kit::bus::{
    async_handler, Adapter, Bus, BusError, Context, DynBus, Event, MemoryAdapter, Mode,
};
use serde_json::json;

#[test]
fn memory_adapter_implements_adapter() {
    let _: Arc<dyn Adapter> = Arc::new(MemoryAdapter::new());
}

#[test]
fn memory_adapter_publish_subscribe() {
    let a = MemoryAdapter::new();
    let got = Arc::new(Mutex::new(None::<Event>));
    let g = Arc::clone(&got);
    a.subscribe(
        "test.event",
        Box::new(move |_, e| {
            *g.lock().unwrap() = Some(e.clone());
            Ok(())
        }),
    );

    let e = Event::new("test.event", "src", json!("hello"));
    let a = Arc::new(a);
    let b = Arc::clone(&a);
    within(move || b.publish(&Context::background(), &e)).expect("publish");
    assert_eq!(got.lock().unwrap().as_ref().unwrap().source, "src");
}

#[test]
fn memory_adapter_async_handler() {
    let a = Arc::new(MemoryAdapter::new());
    let called = Arc::new(AtomicBool::new(false));
    let c = Arc::clone(&called);
    a.subscribe_async(
        "test.event",
        threads(),
        async_handler(move |_, _| {
            let c = Arc::clone(&c);
            async move { c.store(true, Ordering::SeqCst) }
        }),
    );

    let b = Arc::clone(&a);
    within(move || b.publish(&Context::background(), &ev("test.event"))).expect("publish");
    let b = Arc::clone(&a);
    within(move || b.close(&Context::background())).expect("close");
    assert!(
        called.load(Ordering::SeqCst),
        "async handler was not called"
    );
}

#[test]
fn with_adapter_memory_adapter() {
    let bus = Bus::builder().with_adapter(MemoryAdapter::new()).build();
    let got = Arc::new(Mutex::new(None::<Event>));
    let g = Arc::clone(&got);
    bus.subscribe("test.event", move |e| {
        *g.lock().unwrap() = Some(e.clone());
        Ok(())
    });

    let e = Event::new("test.event", "src", json!("hello"));
    let b = bus.clone();
    within(move || b.publish(&e)).expect("publish");
    assert_eq!(got.lock().unwrap().as_ref().unwrap().source, "src");
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).expect("close");
}

#[test]
fn new_default_still_works() {
    let bus = DynBus::new();
    let count = Arc::new(AtomicUsize::new(0));
    let c = Arc::clone(&count);
    bus.subscribe("x", move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let b = bus.clone();
    let _ = within(move || b.publish(&ev("x")));
    assert_eq!(count.load(Ordering::SeqCst), 1);
    let b = bus.clone();
    let _ = within(move || b.close_ctx(&Context::background()));

    // The plain in-memory bus is untouched by the composition path.
    let mut plain = Bus::builder().build();
    let c = Arc::clone(&count);
    plain.subscribe("x", move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    plain.publish(&ev("x")).unwrap();
    assert_eq!(count.load(Ordering::SeqCst), 2);
    assert_eq!(plain.enforce_mode(), Mode::Warn);
}

#[test]
fn new_no_args() {
    let bus = DynBus::new();
    let got = Arc::new(Mutex::new(None::<Event>));
    let g = Arc::clone(&got);
    bus.subscribe("test.topic", move |e| {
        *g.lock().unwrap() = Some(e.clone());
        Ok(())
    });

    let e = Event::new("test.topic", "regression", json!("payload"));
    let b = bus.clone();
    within(move || b.publish(&e)).expect("Publish");
    assert_eq!(
        got.lock().unwrap().as_ref().unwrap().topic.as_str(),
        "test.topic"
    );

    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).expect("Close");
}

#[test]
fn adapter_path_enforces_the_mode_before_the_adapter() {
    // Go's adapterBus checks the topic, then delegates; the memory
    // adapter itself validates nothing (its memBus mode is ModeOff).
    let reported = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&reported);
    let bus = Bus::builder()
        .with_adapter(MemoryAdapter::new())
        .enforce(Mode::Strict)
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
    let err = within(move || b.publish(&ev("Bad Topic"))).unwrap_err();
    assert!(err.is_invalid_topic(), "got {err}");
    assert_eq!(reported.load(Ordering::SeqCst), 1);
    assert_eq!(delivered.load(Ordering::SeqCst), 0);

    // The adapter alone does not validate.
    let a = Arc::new(MemoryAdapter::new());
    let b = Arc::clone(&a);
    within(move || b.publish(&Context::background(), &ev("Bad Topic"))).expect("no validation");
}

#[test]
fn adapter_path_warn_reports_and_delivers() {
    let reported = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&reported);
    let bus = Bus::builder()
        .invalid_topic_reporter(move |_| {
            r.fetch_add(1, Ordering::SeqCst);
        })
        .with_adapter(MemoryAdapter::new())
        .build();
    let delivered = Arc::new(AtomicUsize::new(0));
    let d = Arc::clone(&delivered);
    bus.subscribe("#", move |_| {
        d.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let b = bus.clone();
    within(move || b.publish(&ev("Bad"))).expect("warn delivers");
    assert_eq!(reported.load(Ordering::SeqCst), 1);
    assert_eq!(delivered.load(Ordering::SeqCst), 1);
}

#[test]
fn an_arc_adapter_stays_reachable_by_its_owner() {
    let a = Arc::new(MemoryAdapter::new());
    let bus = Bus::builder().with_adapter(Arc::clone(&a)).build();
    let hits = Arc::new(AtomicUsize::new(0));
    let h = Arc::clone(&hits);
    a.subscribe(
        "x",
        Box::new(move |_, _| {
            h.fetch_add(1, Ordering::SeqCst);
            Ok(())
        }),
    );
    let b = bus.clone();
    within(move || b.publish(&ev("x"))).unwrap();
    assert_eq!(hits.load(Ordering::SeqCst), 1);
}

#[test]
fn unsubscribe_and_publish_after_close_through_the_layers() {
    let bus = Bus::builder().with_adapter(MemoryAdapter::new()).build();
    let hits = Arc::new(AtomicUsize::new(0));
    let h = Arc::clone(&hits);
    let id = bus.subscribe("x", move |_| {
        h.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let b = bus.clone();
    assert!(within(move || b.unsubscribe(id)));
    let b = bus.clone();
    within(move || b.publish(&ev("x"))).unwrap();
    assert_eq!(hits.load(Ordering::SeqCst), 0);

    let b = bus.clone();
    within(move || b.close()).unwrap();
    let b = bus.clone();
    let err = within(move || b.publish(&ev("x"))).unwrap_err();
    assert!(matches!(err, BusError::Closed), "got {err}");
    assert_eq!(err.to_string(), "bus: publish after close");
}

#[test]
fn async_subscribe_through_the_builder() {
    let bus = DynBus::builder().max_async(2).build();
    let called = Arc::new(AtomicBool::new(false));
    let c = Arc::clone(&called);
    bus.subscribe_async("x", threads(), move |_, _| {
        let c = Arc::clone(&c);
        async move { c.store(true, Ordering::SeqCst) }
    });
    let b = bus.clone();
    within(move || b.publish(&ev("x"))).unwrap();
    let b = bus.clone();
    within(move || b.close()).unwrap();
    assert!(called.load(Ordering::SeqCst));
}

#[test]
fn with_network_is_accepted_and_local_delivery_is_unchanged() {
    // The network layer is a seam until the network adapter lands: the
    // addresses are recorded and the bus behaves as without them.
    let bus = Bus::builder()
        .with_network(["ws://127.0.0.1:1/bus"])
        .build();
    let hits = Arc::new(AtomicUsize::new(0));
    let h = Arc::clone(&hits);
    bus.subscribe("x", move |_| {
        h.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let b = bus.clone();
    within(move || b.publish(&ev("x"))).unwrap();
    assert_eq!(hits.load(Ordering::SeqCst), 1);
    let b = bus.clone();
    within(move || b.close()).unwrap();
}

#[test]
fn a_dyn_bus_is_itself_an_adapter() {
    // Layers wrap layers: the composed bus can sit under another one.
    let inner = DynBus::new();
    let outer = Bus::builder().with_adapter(inner.clone()).build();
    let hits = Arc::new(AtomicUsize::new(0));
    let h = Arc::clone(&hits);
    inner.subscribe("x", move |_| {
        h.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let b = outer.clone();
    within(move || b.publish(&ev("x"))).unwrap();
    assert_eq!(hits.load(Ordering::SeqCst), 1);
}
