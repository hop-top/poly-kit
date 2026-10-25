//! Draining close and unsubscribe on [`SharedBus`].
//!
//! Go's `Close` and the `Unsubscribe` closure take the write lock, so
//! they wait for any sync dispatch already running, with no deadline;
//! `Close(ctx)` bounds only the async drain that follows by `ctx`.
//! Ports `TestClose_RejectsPublish` and `TestClosePublishRace` with
//! sync handlers, pins that a done context never cuts the sync drain
//! short, and pins the re-entrant cases Go deadlocks on.
//!
//! `TestClose_RespectsDeadline` and `TestClose_WaitsForAsync` exercise
//! the async drain, which is the only part `ctx` bounds; they are ported
//! with async dispatch.
//!
//! Synchronisation is by channels and barriers. `recv_timeout` serves
//! only as a watchdog, so a deadlock fails instead of hanging.

#![cfg(feature = "bus")]

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, Barrier, Mutex, OnceLock};
use std::thread;
use std::time::Duration;

use hop_top_kit::bus::{BusError, Context, Event, SharedBus, SubscriptionId};
use serde_json::json;

const WATCHDOG: Duration = Duration::from_secs(10);

fn ev(topic: &str) -> Event {
    Event::new(topic, "src", json!(null))
}

/// Runs `f` on another thread and returns its result, failing the test
/// if it has not returned before the watchdog fires.
fn within<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(f());
    });
    rx.recv_timeout(WATCHDOG).expect("call deadlocked")
}

/// A handler that signals entry and blocks until released.
struct Blocked {
    entered: mpsc::Receiver<()>,
    release: mpsc::Sender<()>,
}

fn subscribe_blocking(bus: &SharedBus, topic: &str) -> (SubscriptionId, Blocked) {
    let (entered_tx, entered) = mpsc::channel();
    let (release, release_rx) = mpsc::channel::<()>();
    let release_rx = Mutex::new(release_rx);
    let id = bus.subscribe(topic, move |_| {
        entered_tx.send(()).unwrap();
        release_rx.lock().unwrap().recv().unwrap();
        Ok(())
    });
    (id, Blocked { entered, release })
}

/// Publishes `topic` on another thread; returns once `b`'s handler is
/// running.
fn publish_until_entered(
    bus: &SharedBus,
    topic: &'static str,
    b: &Blocked,
) -> thread::JoinHandle<Result<(), BusError>> {
    let publisher = bus.clone();
    let t = thread::spawn(move || publisher.publish(&ev(topic)));
    b.entered.recv_timeout(WATCHDOG).expect("handler entered");
    t
}

#[test]
fn close_rejects_publish() {
    let bus = SharedBus::default();
    bus.close_ctx(&Context::background()).expect("close");
    let err = bus.publish(&ev("test")).expect_err("closed");
    assert!(matches!(err, BusError::Closed), "{err}");
    assert_eq!(err.to_string(), "bus: publish after close");
}

#[test]
fn close_twice_is_ok() {
    let bus = SharedBus::default();
    bus.close_ctx(&Context::background()).expect("first");
    bus.close_ctx(&Context::background()).expect("second");
}

/// Closes with `ctx` while a sync handler is blocked in flight, then
/// releases the handler. Returns close's result and the order in which
/// the release and close's return happened.
fn close_during_blocked_handler(ctx: Context) -> (Result<(), BusError>, Vec<&'static str>) {
    let bus = SharedBus::default();
    let (_, blocked) = subscribe_blocking(&bus, "test.event");
    let publisher = publish_until_entered(&bus, "test.event", &blocked);

    let log = Arc::new(Mutex::new(Vec::new()));
    let (closing_tx, closing) = mpsc::channel();
    let (b, l) = (bus.clone(), Arc::clone(&log));
    let closer = thread::spawn(move || {
        closing_tx.send(()).unwrap();
        let res = b.close_ctx(&ctx);
        l.lock().unwrap().push("close:returned");
        res
    });
    closing.recv_timeout(WATCHDOG).expect("closer started");
    log.lock().unwrap().push("handler:released");
    blocked.release.send(()).unwrap();

    let res = within(move || closer.join().unwrap());
    publisher
        .join()
        .unwrap()
        .expect("in-flight publish completes");
    assert!(bus.is_closed());
    assert!(matches!(
        bus.publish(&ev("test.event")),
        Err(BusError::Closed)
    ));
    let order = log.lock().unwrap().clone();
    (res, order)
}

#[test]
fn expired_deadline_does_not_cut_the_sync_drain_short() {
    // Go's Close takes mu.Lock() before it looks at ctx: no sync handler
    // is running when it returns, whatever the deadline.
    let (ctx, _cancel) = Context::background().with_timeout(Duration::ZERO);
    let (res, order) = close_during_blocked_handler(ctx);
    res.expect("nothing async to drain");
    assert_eq!(order, ["handler:released", "close:returned"]);
}

#[test]
fn canceled_context_does_not_cut_the_sync_drain_short() {
    let (ctx, cancel) = Context::background().with_cancel();
    cancel.cancel();
    let (res, order) = close_during_blocked_handler(ctx);
    res.expect("nothing async to drain");
    assert_eq!(order, ["handler:released", "close:returned"]);
}

#[test]
fn close_succeeds_once_the_in_flight_publish_finishes() {
    let bus = SharedBus::default();
    let (_, blocked) = subscribe_blocking(&bus, "test.event");
    let publisher = publish_until_entered(&bus, "test.event", &blocked);

    let (closing_tx, closing) = mpsc::channel();
    let b = bus.clone();
    let closer = thread::spawn(move || {
        closing_tx.send(()).unwrap();
        let (ctx, _cancel) = Context::background().with_timeout(WATCHDOG);
        b.close_ctx(&ctx)
    });
    closing.recv_timeout(WATCHDOG).expect("closer started");
    blocked.release.send(()).unwrap();
    closer.join().unwrap().expect("drained in time");
    publisher.join().unwrap().expect("publish");
}

#[test]
fn close_publish_race() {
    // Go's TestClosePublishRace, with sync handlers. Every publish that
    // was admitted before close has finished its handlers by the time
    // close returns, and no handler runs after it.
    const N: usize = 64;
    let bus = SharedBus::default();
    let handled = Arc::new(AtomicUsize::new(0));
    let h = Arc::clone(&handled);
    bus.subscribe("#", move |_| {
        h.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });

    let start = Arc::new(Barrier::new(N + 1));
    let publishers: Vec<_> = (0..N)
        .map(|_| {
            let (bus, start) = (bus.clone(), Arc::clone(&start));
            thread::spawn(move || {
                start.wait();
                bus.publish(&ev("race.test"))
            })
        })
        .collect();

    start.wait();
    let (ctx, _cancel) = Context::background().with_timeout(WATCHDOG);
    bus.close_ctx(&ctx).expect("close");
    let at_close = handled.load(Ordering::SeqCst);

    let mut admitted = 0;
    for p in publishers {
        match p.join().unwrap() {
            Ok(()) => admitted += 1,
            Err(BusError::Closed) => {}
            Err(e) => panic!("unexpected error: {e}"),
        }
    }
    assert_eq!(at_close, admitted);
    assert_eq!(handled.load(Ordering::SeqCst), admitted);
}

#[test]
fn close_from_a_handler_does_not_wait_for_itself() {
    // Go deadlocks: Close wants the write lock its own dispatch holds.
    let bus = SharedBus::default();
    let b = bus.clone();
    bus.subscribe("test.event", move |_| {
        b.close();
        Ok(())
    });
    let p = bus.clone();
    within(move || p.publish(&ev("test.event"))).expect("publish");
    assert!(bus.is_closed());
}

#[test]
fn close_from_a_handler_still_drains_other_threads() {
    // It skips its own publish but waits, with no deadline, for the
    // sync publish blocked on another thread.
    let bus = SharedBus::default();
    let (_, blocked) = subscribe_blocking(&bus, "test.slow.ran");
    let slow = publish_until_entered(&bus, "test.slow.ran", &blocked);

    let log = Arc::new(Mutex::new(Vec::new()));
    let (closing_tx, closing) = mpsc::channel();
    let closing_tx = Mutex::new(closing_tx);
    let (b, l) = (bus.clone(), Arc::clone(&log));
    bus.subscribe("test.closer.ran", move |_| {
        closing_tx.lock().unwrap().send(()).unwrap();
        let (ctx, _cancel) = Context::background().with_timeout(Duration::ZERO);
        b.close_ctx(&ctx)?;
        l.lock().unwrap().push("close:returned");
        Ok(())
    });
    let p = bus.clone();
    let closer = thread::spawn(move || p.publish(&ev("test.closer.ran")));
    closing.recv_timeout(WATCHDOG).expect("closer started");
    log.lock().unwrap().push("slow:released");
    blocked.release.send(()).unwrap();

    within(move || closer.join().unwrap()).expect("publish");
    slow.join().unwrap().expect("publish");
    assert_eq!(*log.lock().unwrap(), ["slow:released", "close:returned"]);
}

#[test]
fn unsubscribe_unknown_returns_false() {
    let bus = SharedBus::default();
    let id = bus.subscribe("test.event", |_| Ok(()));
    assert!(bus.unsubscribe(id));
    assert!(!bus.unsubscribe(id));
}

#[test]
fn unsubscribe_lets_the_in_flight_publish_finish_delivering() {
    // Go's unsubscribe waits for the whole in-flight publish, so a
    // handler later in that publish's order is still delivered.
    let bus = SharedBus::default();
    let (_, blocked) = subscribe_blocking(&bus, "test.event");
    let log = Arc::new(Mutex::new(Vec::new()));
    let l = Arc::clone(&log);
    let later = bus.subscribe("test.event", move |_| {
        l.lock().unwrap().push("later:ran");
        Ok(())
    });
    let publisher = publish_until_entered(&bus, "test.event", &blocked);

    let (unsubbing_tx, unsubbing) = mpsc::channel();
    let (b, l) = (bus.clone(), Arc::clone(&log));
    let unsubscriber = thread::spawn(move || {
        unsubbing_tx.send(()).unwrap();
        assert!(b.unsubscribe(later));
        l.lock().unwrap().push("unsubscribed");
    });
    unsubbing
        .recv_timeout(WATCHDOG)
        .expect("unsubscriber started");
    blocked.release.send(()).unwrap();
    unsubscriber.join().unwrap();
    publisher.join().unwrap().expect("publish");
    assert_eq!(*log.lock().unwrap(), ["later:ran", "unsubscribed"]);
}

#[test]
fn handler_unsubscribes_itself() {
    let bus = SharedBus::default();
    let calls = Arc::new(AtomicUsize::new(0));
    let me: Arc<OnceLock<SubscriptionId>> = Arc::default();
    let (b, c, m) = (bus.clone(), Arc::clone(&calls), Arc::clone(&me));
    let id = bus.subscribe("test.event", move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        assert!(b.unsubscribe(*m.get().expect("id set")));
        Ok(())
    });
    me.set(id).unwrap();

    let p = bus.clone();
    within(move || {
        p.publish(&ev("test.event"))?;
        p.publish(&ev("test.event"))
    })
    .expect("publish");
    assert_eq!(calls.load(Ordering::SeqCst), 1);
}

#[test]
fn handler_unsubscribing_a_later_handler_skips_it_in_the_same_publish() {
    // Go deadlocks here. The removal takes effect at once: the rest of
    // this publish no longer delivers to it.
    let bus = SharedBus::default();
    let target: Arc<OnceLock<SubscriptionId>> = Arc::default();
    let (b, t) = (bus.clone(), Arc::clone(&target));
    bus.subscribe("test.event", move |_| {
        assert!(b.unsubscribe(*t.get().expect("id set")));
        Ok(())
    });
    let id = bus.subscribe("test.event", |_| panic!("removed before its turn"));
    target.set(id).unwrap();

    let p = bus.clone();
    within(move || p.publish(&ev("test.event"))).expect("publish");
}

#[test]
fn handler_unsubscribing_one_running_elsewhere_does_not_wait() {
    // A handler never blocks on another thread's dispatch, so two
    // handlers unsubscribing each other cannot deadlock.
    let bus = SharedBus::default();
    let (slow_id, blocked) = subscribe_blocking(&bus, "test.slow.ran");
    let slow = publish_until_entered(&bus, "test.slow.ran", &blocked);

    let b = bus.clone();
    bus.subscribe("test.remover.ran", move |_| {
        assert!(b.unsubscribe(slow_id));
        Ok(())
    });
    let p = bus.clone();
    within(move || p.publish(&ev("test.remover.ran"))).expect("publish");

    blocked.release.send(()).unwrap();
    slow.join().unwrap().expect("publish");
}

/// Name of the thread whose panic the scoped hook below keeps quiet.
const QUIET: &str = "bus-drain-expected-panic";

#[test]
fn a_panicking_handler_does_not_wedge_close() {
    let bus = SharedBus::default();
    bus.subscribe("test.event", |_| panic!("boom"));

    // Keep the expected panic out of the test output; every other
    // thread's panic still reaches the previous hook.
    let previous: Arc<dyn Fn(&std::panic::PanicHookInfo<'_>) + Send + Sync> =
        Arc::from(std::panic::take_hook());
    let fallback = Arc::clone(&previous);
    std::panic::set_hook(Box::new(move |info| {
        if thread::current().name() != Some(QUIET) {
            fallback(info);
        }
    }));
    let p = bus.clone();
    let joined = thread::Builder::new()
        .name(QUIET.into())
        .spawn(move || p.publish(&ev("test.event")))
        .unwrap()
        .join();
    drop(std::panic::take_hook());
    std::panic::set_hook(Box::new(move |info| previous(info)));
    assert!(joined.is_err(), "handler panic propagates to the publisher");

    let b = bus.clone();
    within(move || {
        let (ctx, _cancel) = Context::background().with_timeout(WATCHDOG);
        b.close_ctx(&ctx)
    })
    .expect("nothing left in flight");
}
