//! Async subscribe and the bounded pool on [`SharedBus`].
//!
//! Ports from `go/runtime/bus`: `TestPublish_AsyncHandler`,
//! `TestPublish_AsyncDoesNotBlockPublisher`,
//! `TestAsyncPool_BoundedGoroutines`, `TestWithMaxAsync_*`,
//! `TestRegressionPool*`, `TestRegressionWithMaxAsyncZeroNoPanic` and
//! `TestIntegration_VetoBlocksAsync`, plus dispatch-order pins.
//! `bus_async_close.rs` covers close and unsubscribe.
//!
//! Go's tests sleep to keep handlers busy; these block handlers on a
//! gate and wait on bus state instead. Timeouts are watchdogs only.

#![cfg(feature = "bus")]

#[path = "bus_async/exec.rs"]
mod exec;

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;

use exec::{entered, ev, publish, threads, until, until_slot_waiters, within, Gate, Manual};
use hop_top_kit::bus::{Bus, BusError, Context, SharedBus, DEFAULT_MAX_ASYNC};

fn bus_with_max(n: usize) -> SharedBus {
    SharedBus::new(Bus::builder().max_async(n).build())
}

#[test]
fn publish_async_handler() {
    let bus = SharedBus::default();
    let called = Arc::new(AtomicUsize::new(0));
    let c = Arc::clone(&called);
    bus.subscribe_async("test.event", threads(), move |_, _| {
        let c = Arc::clone(&c);
        async move {
            c.fetch_add(1, Ordering::SeqCst);
        }
    });
    publish(&bus, "test.event").expect("publish");
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).expect("close");
    assert_eq!(
        called.load(Ordering::SeqCst),
        1,
        "close drained the handler"
    );
}

#[test]
fn async_does_not_block_publisher() {
    let bus = SharedBus::default();
    let (gate, arrivals) = Gate::new();
    let g = gate.clone();
    bus.subscribe_async("test.event", threads(), move |_, _| {
        let g = g.clone();
        async move { g.pass() }
    });
    // The handler stays blocked; publish must return regardless.
    let b = bus.clone();
    within(move || b.publish(&ev("test.event"))).expect("publish");
    entered(&arrivals, 1);
    gate.open();
    let b = bus.clone();
    within(move || b.close());
}

#[test]
fn async_handler_gets_the_publish_context_and_event() {
    #[derive(Debug, PartialEq)]
    struct Origin(&'static str);
    let manual = Manual::default();
    let bus = SharedBus::default();
    let seen = Arc::new(Mutex::new(None));
    let s = Arc::clone(&seen);
    bus.subscribe_async("test.*", manual.spawner(), move |ctx, e| {
        let s = Arc::clone(&s);
        async move {
            let origin = ctx.value::<Origin>().map(|o| o.0);
            *s.lock().unwrap() = Some((origin, e.topic.to_string()));
        }
    });
    let ctx = Context::background().with_value(Origin("peer"));
    bus.publish_ctx(&ctx, &ev("test.event")).unwrap();
    manual.run();
    let got = seen.lock().unwrap().take();
    assert_eq!(got, Some((Some("peer"), "test.event".to_string())));
}

// Go: sync handlers run first, under the read lock; async handlers are
// launched after all of them succeed, in subscription order.
#[test]
fn sync_handlers_run_before_any_async_launch_and_async_keeps_order() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    let log = Arc::new(Mutex::new(Vec::new()));
    for name in ["a1", "a2"] {
        let l = Arc::clone(&log);
        bus.subscribe_async("test.event", manual.spawner(), move |_, _| {
            let l = Arc::clone(&l);
            async move { l.lock().unwrap().push(name) }
        });
    }
    let (l, m) = (Arc::clone(&log), manual.clone());
    bus.subscribe("test.event", move |_| {
        assert_eq!(
            m.spawned(),
            0,
            "no async launch before sync handlers finish"
        );
        l.lock().unwrap().push("sync");
        Ok(())
    });
    publish(&bus, "test.event").unwrap();
    assert_eq!(manual.spawned(), 2);
    manual.run();
    assert_eq!(*log.lock().unwrap(), ["sync", "a1", "a2"]);
}

// TestIntegration_VetoBlocksAsync.
#[test]
fn sync_veto_blocks_async() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    bus.subscribe("order.create", |_| Err(BusError::handler("vetoed")));
    bus.subscribe_async("order.create", manual.spawner(), |_, _| async {});
    let err = publish(&bus, "order.create").unwrap_err();
    assert_eq!(err.to_string(), "vetoed");
    assert_eq!(manual.spawned(), 0, "async handler launched despite veto");
    let b = bus.clone();
    within(move || b.close());
}

// Async handlers return nothing, so they cannot veto: Go's
// AsyncHandler has no error result.
#[test]
fn async_handlers_never_affect_the_publish_result() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    bus.subscribe_async("test.event", manual.spawner(), |_, _| async {});
    bus.subscribe("test.event", |_| Ok(()));
    assert!(publish(&bus, "test.event").is_ok());
    manual.run();
}

#[test]
fn nested_publish_launches_its_async_before_the_outer_ones() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    let log = Arc::new(Mutex::new(Vec::new()));
    let l = Arc::clone(&log);
    bus.subscribe_async("test.#", manual.spawner(), move |_, e| {
        let l = Arc::clone(&l);
        async move { l.lock().unwrap().push(e.topic.to_string()) }
    });
    let b = bus.clone();
    bus.subscribe("test.outer", move |_| b.publish(&ev("test.inner")));
    publish(&bus, "test.outer").unwrap();
    manual.run();
    assert_eq!(*log.lock().unwrap(), ["test.inner", "test.outer"]);
}

#[test]
fn with_max_async_default_bounds() {
    assert_eq!(DEFAULT_MAX_ASYNC, 256);
    assert_eq!(SharedBus::default().max_async(), DEFAULT_MAX_ASYNC);
}

#[test]
fn with_max_async_custom_value() {
    assert_eq!(bus_with_max(32).max_async(), 32);
}

#[test]
fn with_max_async_zero_falls_back_to_default() {
    assert_eq!(bus_with_max(0).max_async(), DEFAULT_MAX_ASYNC);
}

// TestRegressionWithMaxAsyncZeroNoPanic.
#[test]
fn zero_max_async_still_delivers() {
    let bus = bus_with_max(0);
    let delivered = Arc::new(AtomicUsize::new(0));
    let d = Arc::clone(&delivered);
    bus.subscribe_async("#", threads(), move |_, _| {
        let d = Arc::clone(&d);
        async move {
            d.fetch_add(1, Ordering::SeqCst);
        }
    });
    for _ in 0..10 {
        publish(&bus, "zero.pool").unwrap();
    }
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).unwrap();
    assert_eq!(delivered.load(Ordering::SeqCst), 10);
}

/// Publishes `events` events to one async handler on a pool of `max`,
/// and returns the peak number of handlers running at once and the
/// number delivered.
fn load(max: usize, events: usize) -> (usize, usize) {
    let bus = bus_with_max(max);
    let active = Arc::new(AtomicUsize::new(0));
    let peak = Arc::new(AtomicUsize::new(0));
    let delivered = Arc::new(AtomicUsize::new(0));
    let (a, p, d) = (
        Arc::clone(&active),
        Arc::clone(&peak),
        Arc::clone(&delivered),
    );
    bus.subscribe_async("load.#", threads(), move |_, _| {
        let (a, p, d) = (Arc::clone(&a), Arc::clone(&p), Arc::clone(&d));
        async move {
            let cur = a.fetch_add(1, Ordering::SeqCst) + 1;
            p.fetch_max(cur, Ordering::SeqCst);
            for _ in 0..50 {
                thread::yield_now();
            }
            a.fetch_sub(1, Ordering::SeqCst);
            d.fetch_add(1, Ordering::SeqCst);
        }
    });
    let b = bus.clone();
    within(move || {
        for _ in 0..events {
            b.publish(&ev("load.test")).unwrap();
        }
        b.close_ctx(&Context::background()).unwrap();
    });
    (
        peak.load(Ordering::SeqCst),
        delivered.load(Ordering::SeqCst),
    )
}

// TestAsyncPool_BoundedGoroutines.
#[test]
fn async_pool_bounds_concurrency() {
    let (peak, delivered) = load(8, 200);
    assert!(peak <= 8, "peak {peak} > 8");
    assert_eq!(delivered, 200);
}

// TestRegressionPoolPeakConcurrencyBounded and
// TestRegressionPoolAllEventsDelivered.
#[test]
fn pool_peak_bounded_and_every_event_delivered() {
    let (peak, delivered) = load(4, 500);
    assert!(peak <= 4, "peak {peak} > 4");
    assert_eq!(delivered, 500, "no drops under saturation");
}

// Go's publish blocks on the semaphore while the pool is full, and
// launches as soon as a slot frees.
#[test]
fn full_pool_blocks_the_publisher_until_a_slot_frees() {
    let bus = bus_with_max(2);
    let (gate, arrivals) = Gate::new();
    let g = gate.clone();
    let running = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&running);
    bus.subscribe_async("sat.test", threads(), move |_, _| {
        let (g, r) = (g.clone(), Arc::clone(&r));
        async move {
            r.fetch_add(1, Ordering::SeqCst);
            g.pass();
        }
    });
    publish(&bus, "sat.test").unwrap();
    publish(&bus, "sat.test").unwrap();
    entered(&arrivals, 2);

    let b = bus.clone();
    let third = thread::spawn(move || b.publish(&ev("sat.test")));
    until_slot_waiters(&bus, 1);
    assert_eq!(bus.async_in_flight(), 2);
    assert_eq!(running.load(Ordering::SeqCst), 2, "third waits for a slot");

    gate.open();
    within(move || third.join().unwrap()).expect("third publish launches");
    entered(&arrivals, 1);
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).unwrap();
    assert_eq!(running.load(Ordering::SeqCst), 3);
}

#[test]
fn a_task_dropped_unrun_releases_its_slot() {
    let manual = Manual::default();
    let bus = bus_with_max(1);
    let ran = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&ran);
    bus.subscribe_async("test.event", manual.spawner(), move |_, _| {
        let r = Arc::clone(&r);
        async move {
            r.fetch_add(1, Ordering::SeqCst);
        }
    });
    publish(&bus, "test.event").unwrap();
    assert_eq!(bus.async_in_flight(), 1);
    manual.discard();
    assert_eq!(bus.async_in_flight(), 0, "slot released on drop");
    // The freed slot admits the next publish at once.
    let b = bus.clone();
    within(move || b.publish(&ev("test.event"))).unwrap();
    manual.run();
    assert_eq!(ran.load(Ordering::SeqCst), 1);
    until("drained", || bus.async_in_flight() == 0);
    let b = bus.clone();
    within(move || b.close());
}
