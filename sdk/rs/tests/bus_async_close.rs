//! Close and unsubscribe with async subscriptions on [`SharedBus`].
//!
//! Ports from `go/runtime/bus`: `TestClose_WaitsForAsync`,
//! `TestClose_RespectsDeadline`, `TestRegressionCloseDuringSaturation`,
//! `TestRegressionCloseStopsNewGoroutines` and the tight-deadline half
//! of `TestIntegration_CloseDrainUnderLoad`. Pins Go's close contract:
//! the async drain is bounded by the context, a timeout returns Go's
//! `ctx.Err()` text and leaves the bus closed, and no handler starts
//! after close. Also pins unsubscribe and subscribe-after-close.

#![cfg(feature = "bus")]

#[path = "bus_async/exec.rs"]
mod exec;

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

use exec::{
    entered, ev, publish, threads, until, until_drain_waiters, until_slot_waiters, within, Gate,
    Manual,
};
use hop_top_kit::bus::{Bus, BusError, Context, ContextError, SharedBus};

/// A bus with one async handler on `test.event` that blocks on the
/// returned gate.
fn blocked_bus(max: usize) -> (SharedBus, Gate, std::sync::mpsc::Receiver<()>) {
    let bus = SharedBus::new(Bus::builder().max_async(max).build());
    let (gate, arrivals) = Gate::new();
    let g = gate.clone();
    bus.subscribe_async("test.event", threads(), move |_, _| {
        let g = g.clone();
        async move { g.pass() }
    });
    (bus, gate, arrivals)
}

#[test]
fn close_waits_for_async() {
    let (bus, gate, arrivals) = blocked_bus(0);
    publish(&bus, "test.event").unwrap();
    entered(&arrivals, 1);

    let log = Arc::new(Mutex::new(Vec::new()));
    let (b, l) = (bus.clone(), Arc::clone(&log));
    let closer = thread::spawn(move || {
        let res = b.close_ctx(&Context::background());
        l.lock().unwrap().push("close:returned");
        res
    });
    until_drain_waiters(&bus, 1);
    log.lock().unwrap().push("handler:released");
    gate.open();
    within(move || closer.join().unwrap()).expect("close");
    assert_eq!(*log.lock().unwrap(), ["handler:released", "close:returned"]);
    assert_eq!(bus.async_in_flight(), 0);
}

#[test]
fn close_respects_deadline() {
    let (bus, gate, arrivals) = blocked_bus(0);
    publish(&bus, "test.event").unwrap();
    entered(&arrivals, 1);

    let (ctx, _cancel) = Context::background().with_timeout(Duration::from_millis(10));
    let b = bus.clone();
    let err = within(move || b.close_ctx(&ctx)).unwrap_err();
    assert!(matches!(
        err,
        BusError::Context(ContextError::DeadlineExceeded)
    ));
    assert_eq!(err.to_string(), "context deadline exceeded");
    // The bus stays closed after a timed-out close.
    assert!(bus.is_closed());
    assert!(matches!(publish(&bus, "test.event"), Err(BusError::Closed)));
    assert_eq!(bus.async_in_flight(), 1, "handler still running");
    gate.open();
    // A later close finishes the drain.
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).expect("second close drains");
}

#[test]
fn close_returns_canceled_when_canceled_during_the_async_drain() {
    let (bus, gate, arrivals) = blocked_bus(0);
    publish(&bus, "test.event").unwrap();
    entered(&arrivals, 1);

    let (ctx, cancel) = Context::background().with_cancel();
    let b = bus.clone();
    let closer = thread::spawn(move || b.close_ctx(&ctx));
    until_drain_waiters(&bus, 1);
    cancel.cancel();
    let err = within(move || closer.join().unwrap()).unwrap_err();
    assert_eq!(err.to_string(), "context canceled");
    gate.open();
    let b = bus.clone();
    within(move || b.close());
}

#[test]
fn deadline_bounds_tasks_the_executor_has_not_run() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    let ran = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&ran);
    bus.subscribe_async("test.event", manual.spawner(), move |_, _| {
        let r = Arc::clone(&r);
        async move {
            r.fetch_add(1, Ordering::SeqCst);
        }
    });
    publish(&bus, "test.event").unwrap();
    let (ctx, _c) = Context::background().with_timeout(Duration::from_millis(5));
    let b = bus.clone();
    let err = within(move || b.close_ctx(&ctx)).unwrap_err();
    assert_eq!(err.to_string(), "context deadline exceeded");
    // Admitted before close, so it still runs; then close drains.
    manual.run();
    assert_eq!(ran.load(Ordering::SeqCst), 1);
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).expect("drained");
}

// TestRegressionCloseDuringSaturation, plus the publisher Go leaves
// blocked on the semaphore: it bails without launching.
#[test]
fn close_during_saturation() {
    let (bus, gate, arrivals) = blocked_bus(2);
    publish(&bus, "test.event").unwrap();
    publish(&bus, "test.event").unwrap();
    entered(&arrivals, 2);
    let b = bus.clone();
    let blocked = thread::spawn(move || b.publish(&ev("test.event")));
    until_slot_waiters(&bus, 1);

    let b = bus.clone();
    let closer = thread::spawn(move || {
        let (ctx, _c) = Context::background().with_timeout(Duration::from_secs(5));
        b.close_ctx(&ctx)
    });
    // Close wakes the blocked publisher, which returns Ok without
    // launching, as Go's closed re-check after the slot does.
    within(move || blocked.join().unwrap()).expect("blocked publish returns Ok");
    until_drain_waiters(&bus, 1);
    gate.open();
    within(move || closer.join().unwrap()).expect("close drains");
    assert!(
        arrivals.try_recv().is_err(),
        "no handler launched after close"
    );
}

// TestRegressionCloseStopsNewGoroutines.
#[test]
fn close_stops_new_tasks() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    bus.subscribe_async("#", manual.spawner(), |_, _| async {});
    let b = bus.clone();
    within(move || b.close());
    for _ in 0..50 {
        assert!(matches!(publish(&bus, "post.close"), Err(BusError::Closed)));
    }
    assert_eq!(manual.spawned(), 0);
}

#[test]
fn close_from_an_async_handler_does_not_wait_for_itself() {
    let bus = SharedBus::default();
    let (tx, rx) = std::sync::mpsc::channel();
    let b = bus.clone();
    bus.subscribe_async("test.event", threads(), move |_, _| {
        let (b, tx) = (b.clone(), tx.clone());
        async move {
            let _ = tx.send(b.close_ctx(&Context::background()));
        }
    });
    publish(&bus, "test.event").unwrap();
    let res = rx.recv_timeout(exec::WATCHDOG).expect("close deadlocked");
    assert!(res.is_ok());
    until("drained", || bus.async_in_flight() == 0);
}

// Go's unsubscribe removes the subscription; tasks already launched run
// on. Here, once unsubscribe returns, no task for it is admitted.
#[test]
fn unsubscribe_stops_new_tasks_but_admitted_ones_run() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    let ran = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&ran);
    let id = bus.subscribe_async("test.event", manual.spawner(), move |_, _| {
        let r = Arc::clone(&r);
        async move {
            r.fetch_add(1, Ordering::SeqCst);
        }
    });
    publish(&bus, "test.event").unwrap();
    assert!(bus.unsubscribe(id));
    publish(&bus, "test.event").unwrap();
    assert_eq!(manual.spawned(), 1);
    manual.run();
    assert_eq!(ran.load(Ordering::SeqCst), 1);
    let b = bus.clone();
    within(move || b.close());
}

#[test]
fn unsubscribe_from_a_sync_handler_skips_the_async_launch() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    let slot = Arc::new(Mutex::new(None));
    let (b, s) = (bus.clone(), Arc::clone(&slot));
    bus.subscribe("test.event", move |_| {
        if let Some(id) = s.lock().unwrap().take() {
            b.unsubscribe(id);
        }
        Ok(())
    });
    let id = bus.subscribe_async("test.event", manual.spawner(), |_, _| async {});
    *slot.lock().unwrap() = Some(id);
    publish(&bus, "test.event").unwrap();
    assert_eq!(manual.spawned(), 0);
}

struct DropFlag(Arc<AtomicUsize>);

impl Drop for DropFlag {
    fn drop(&mut self) {
        self.0.fetch_add(1, Ordering::SeqCst);
    }
}

// A subscription added after close is never registered: its handler is
// dropped at once, so a handler holding a clone of the bus cannot keep
// it alive through a cycle.
#[test]
fn subscribe_after_close_registers_nothing() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    let b = bus.clone();
    within(move || b.close());
    let dropped = Arc::new(AtomicUsize::new(0));

    let (flag, b) = (DropFlag(Arc::clone(&dropped)), bus.clone());
    let a = bus.subscribe_async("#", manual.spawner(), move |_, _| {
        let _ = (&flag, &b);
        async {}
    });
    let flag = DropFlag(Arc::clone(&dropped));
    let s = bus.subscribe("#", move |_| {
        let _ = &flag;
        Ok(())
    });
    assert_eq!(dropped.load(Ordering::SeqCst), 2, "handlers dropped");
    assert_ne!(a, s, "ids stay unique");
    assert!(!bus.unsubscribe(a));
    assert!(!bus.unsubscribe(s));
}

#[test]
fn plain_bus_subscribe_after_close_registers_nothing() {
    let mut bus = Bus::new();
    bus.close();
    let id = bus.subscribe("#", |_| Ok(()));
    assert!(!bus.unsubscribe(id));
}

// Tight-deadline half of TestIntegration_CloseDrainUnderLoad.
#[test]
fn tight_deadline_under_load_times_out() {
    let (bus, gate, arrivals) = blocked_bus(0);
    for _ in 0..20 {
        publish(&bus, "test.event").unwrap();
    }
    entered(&arrivals, 20);
    let (ctx, _c) = Context::background().with_timeout(Duration::from_millis(5));
    let b = bus.clone();
    let err = within(move || b.close_ctx(&ctx)).unwrap_err();
    assert_eq!(err.to_string(), "context deadline exceeded");
    gate.open();
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).unwrap();
}

// Go's unsubscribe takes the write lock, so it waits for a sync phase
// that matched; the publish then launches nothing for it here.
#[test]
fn unsubscribe_during_the_sync_phase_skips_the_launch() {
    let manual = Manual::default();
    let bus = SharedBus::default();
    let (gate, arrivals) = Gate::new();
    let g = gate.clone();
    bus.subscribe("test.event", move |_| {
        g.pass();
        Ok(())
    });
    let id = bus.subscribe_async("test.event", manual.spawner(), |_, _| async {});
    let b = bus.clone();
    let publisher = thread::spawn(move || b.publish(&ev("test.event")));
    entered(&arrivals, 1);
    let b = bus.clone();
    let unsub = thread::spawn(move || b.unsubscribe(id));
    until_drain_waiters(&bus, 1);
    gate.open();
    assert!(within(move || unsub.join().unwrap()));
    within(move || publisher.join().unwrap()).unwrap();
    assert_eq!(manual.spawned(), 0, "no launch after unsubscribe");
}

// A publisher waiting for a slot has left its sync phase, so neither
// unsubscribe nor close waits for it: Go releases its read lock before
// the semaphore send for the same reason.
#[test]
fn unsubscribe_does_not_wait_for_a_publisher_blocked_on_the_pool() {
    let (bus, gate, arrivals) = blocked_bus(1);
    publish(&bus, "test.event").unwrap();
    entered(&arrivals, 1);
    // Its publish matches this and the async handler; it blocks on the
    // full pool after this one ran.
    let id = bus.subscribe("test.#", |_| Ok(()));
    let b = bus.clone();
    let blocked = thread::spawn(move || b.publish(&ev("test.event")));
    until_slot_waiters(&bus, 1);
    let b = bus.clone();
    assert!(within(move || b.unsubscribe(id)));
    gate.open();
    within(move || blocked.join().unwrap()).unwrap();
    let b = bus.clone();
    within(move || b.close_ctx(&Context::background())).unwrap();
}
