//! Thread safety and re-entrancy of the in-memory bus.
//!
//! Ports `TestConcurrentPublishSubscribe` from
//! `go/runtime/bus/bus_test.go`, and pins nested publish: Go's sync
//! handlers may publish on the bus that is dispatching to them, and the
//! inner publish delivers in full before the outer one continues.

#![cfg(feature = "bus")]

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use std::thread;
use std::time::Duration;

use hop_top_kit::bus::{Bus, BusError, ErrFn, Event, Handler, Mode, SharedBus, SubscriptionId};
use serde_json::json;

fn ev(topic: &str) -> Event {
    Event::new(topic, "src", json!(null))
}

/// Publishes `event` on another thread and fails the test if it has not
/// returned within a few seconds, so a deadlock fails instead of hanging
/// the suite.
fn publish_or_deadlock(bus: &SharedBus, event: Event) -> Result<(), BusError> {
    let (tx, rx) = mpsc::channel();
    let publisher = bus.clone();
    thread::spawn(move || {
        let _ = tx.send(publisher.publish(&event));
    });
    rx.recv_timeout(Duration::from_secs(5))
        .expect("publish deadlocked")
}

const fn assert_send_sync<T: Send + Sync>() {}

// Compile-time: a consumer can hold the bus behind `Arc`, share it
// across threads, and move handlers and errors between threads.
const _: () = {
    assert_send_sync::<Bus>();
    assert_send_sync::<SharedBus>();
    assert_send_sync::<Handler>();
    assert_send_sync::<ErrFn>();
    assert_send_sync::<BusError>();
    assert_send_sync::<Event>();
};

#[test]
fn concurrent_publish_subscribe() {
    let bus = SharedBus::new(Bus::builder().enforce(Mode::Strict).build());
    let count = Arc::new(AtomicUsize::new(0));
    let c = Arc::clone(&count);
    bus.subscribe("test.bus.concurrent.ran", move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });

    let threads: Vec<_> = (0..8)
        .map(|_| {
            let bus = bus.clone();
            thread::spawn(move || {
                for _ in 0..100 {
                    bus.publish(&ev("test.bus.concurrent.ran"))
                        .expect("publish");
                    // Subscribing and unsubscribing on other threads
                    // must not disturb in-flight publishes.
                    let id = bus.subscribe("test.bus.other.ran", |_| Ok(()));
                    bus.unsubscribe(id);
                }
            })
        })
        .collect();
    for t in threads {
        t.join().expect("thread");
    }
    assert_eq!(count.load(Ordering::SeqCst), 800);
}

#[test]
fn plain_bus_publishes_from_many_threads() {
    let mut bus = Bus::new();
    let count = Arc::new(AtomicUsize::new(0));
    let c = Arc::clone(&count);
    bus.subscribe("test.bus.plain.ran", move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let bus = Arc::new(bus);
    thread::scope(|s| {
        for _ in 0..4 {
            s.spawn(|| {
                for _ in 0..50 {
                    bus.publish(&ev("test.bus.plain.ran")).expect("publish");
                }
            });
        }
    });
    assert_eq!(count.load(Ordering::SeqCst), 200);
}

#[test]
fn handler_can_publish_reentrantly() {
    let bus = SharedBus::default();
    let order = Arc::new(Mutex::new(Vec::<String>::new()));

    let (inner, o) = (bus.clone(), Arc::clone(&order));
    bus.subscribe("test.bus.outer.ran", move |_| {
        o.lock().unwrap().push("outer:start".into());
        inner.publish(&ev("test.bus.inner.ran"))?;
        o.lock().unwrap().push("outer:end".into());
        Ok(())
    });
    let o = Arc::clone(&order);
    bus.subscribe("test.bus.inner.ran", move |_| {
        o.lock().unwrap().push("inner".into());
        Ok(())
    });
    let o = Arc::clone(&order);
    bus.subscribe("test.bus.outer.ran", move |_| {
        o.lock().unwrap().push("outer:second".into());
        Ok(())
    });

    publish_or_deadlock(&bus, ev("test.bus.outer.ran")).expect("publish");
    // Inner delivery completes before the outer handler resumes, and
    // the outer publish then continues in subscription order.
    assert_eq!(
        *order.lock().unwrap(),
        ["outer:start", "inner", "outer:end", "outer:second"]
    );
}

#[test]
fn nested_publish_does_not_deadlock_behind_a_waiting_writer() {
    // Holding the lock across dispatch would make the inner publish a
    // recursive read; with a subscriber queued for the write lock in
    // between, a writer-preferring lock never grants it.
    //
    // The sleep gives the writer time to queue. std's RwLock exposes no
    // way to observe a queued writer, so there is nothing to sync on
    // deterministically. If the writer has not queued yet, the inner
    // publish succeeds and the test passes vacuously; it cannot fail
    // against a correct bus, because no lock is held while a handler
    // runs, so the writer and the inner publish never wait on each other.
    let bus = SharedBus::default();
    let (b, writer_done) = (bus.clone(), Arc::new(AtomicUsize::new(0)));
    let w = Arc::clone(&writer_done);
    bus.subscribe("test.bus.outer.ran", move |_| {
        let (sub, w) = (b.clone(), Arc::clone(&w));
        let writer = thread::spawn(move || {
            sub.subscribe("test.bus.late.ran", |_| Ok(()));
            w.fetch_add(1, Ordering::SeqCst);
        });
        thread::sleep(Duration::from_millis(50));
        b.publish(&ev("test.bus.inner.ran"))?;
        writer.join().expect("writer");
        Ok(())
    });

    publish_or_deadlock(&bus, ev("test.bus.outer.ran")).expect("publish");
    assert_eq!(writer_done.load(Ordering::SeqCst), 1);
}

#[test]
fn nested_publish_veto_propagates_to_outer_publisher() {
    let bus = SharedBus::default();
    let inner = bus.clone();
    bus.subscribe("test.bus.outer.ran", move |_| {
        inner.publish(&ev("test.bus.inner.ran"))
    });
    bus.subscribe("test.bus.inner.ran", |_| Err(BusError::handler("nope")));

    let err = publish_or_deadlock(&bus, ev("test.bus.outer.ran")).expect_err("veto");
    assert_eq!(err.to_string(), "nope");
}

#[test]
fn handler_can_subscribe_reentrantly() {
    let bus = SharedBus::default();
    let count = Arc::new(AtomicUsize::new(0));

    let (b, c) = (bus.clone(), Arc::clone(&count));
    bus.subscribe("test.bus.setup.ran", move |_| {
        let c = Arc::clone(&c);
        b.subscribe("test.bus.late.ran", move |_| {
            c.fetch_add(1, Ordering::SeqCst);
            Ok(())
        });
        Ok(())
    });

    publish_or_deadlock(&bus, ev("test.bus.setup.ran")).expect("publish");
    bus.publish(&ev("test.bus.late.ran")).expect("publish");
    assert_eq!(count.load(Ordering::SeqCst), 1);
}

#[test]
fn reporter_can_publish_reentrantly() {
    let slot: Arc<Mutex<Option<SharedBus>>> = Arc::default();
    let reported = Arc::new(AtomicUsize::new(0));

    let (s, r) = (Arc::clone(&slot), Arc::clone(&reported));
    let bus = SharedBus::new(
        Bus::builder()
            .enforce(Mode::Warn)
            .invalid_topic_reporter(move |_| {
                r.fetch_add(1, Ordering::SeqCst);
                let bus = s.lock().unwrap().clone().expect("bus set");
                bus.publish(&ev("test.bus.violation.reported"))
                    .expect("report publish");
            })
            .build(),
    );
    *slot.lock().unwrap() = Some(bus.clone());

    let seen = Arc::new(AtomicUsize::new(0));
    let c = Arc::clone(&seen);
    bus.subscribe("test.bus.violation.reported", move |_| {
        c.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });

    publish_or_deadlock(&bus, ev("Bad")).expect("warn delivers");
    assert_eq!(reported.load(Ordering::SeqCst), 1);
    assert_eq!(seen.load(Ordering::SeqCst), 1);
    // Break the bus -> reporter -> bus cycle so the test does not leak.
    slot.lock().unwrap().take();
}

/// Calls back into the bus when dropped, as a handler capture might.
struct TouchesBusOnDrop(SharedBus);

impl Drop for TouchesBusOnDrop {
    fn drop(&mut self) {
        let _ = self.0.is_closed();
    }
}

/// Runs `f` on another thread and fails the test if it has not returned
/// within a few seconds.
fn finishes(f: impl FnOnce() + Send + 'static) {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        f();
        let _ = tx.send(());
    });
    rx.recv_timeout(Duration::from_secs(5))
        .expect("call deadlocked");
}

#[test]
fn unsubscribe_drops_the_handler_outside_the_lock() {
    let bus = SharedBus::default();
    let capture = TouchesBusOnDrop(bus.clone());
    let id = bus.subscribe("test.bus.drop.ran", move |_| {
        let _ = &capture;
        Ok(())
    });
    finishes(move || assert!(bus.unsubscribe(id)));
}

#[test]
fn close_drops_handlers_outside_the_lock() {
    let bus = SharedBus::default();
    let capture = TouchesBusOnDrop(bus.clone());
    bus.subscribe("test.bus.drop.ran", move |_| {
        let _ = &capture;
        Ok(())
    });
    finishes(move || bus.close());
}

/// Starts a publish on another thread whose handler blocks until
/// released. Returns once the handler is running.
fn publish_with_blocked_handler(
    bus: &SharedBus,
) -> (SubscriptionId, mpsc::Sender<()>, thread::JoinHandle<()>) {
    let (entered_tx, entered_rx) = mpsc::channel();
    let (release_tx, release_rx) = mpsc::channel::<()>();
    let release_rx = Mutex::new(release_rx);
    let id = bus.subscribe("test.bus.slow.ran", move |_| {
        entered_tx.send(()).unwrap();
        release_rx.lock().unwrap().recv().unwrap();
        Ok(())
    });
    let publisher = bus.clone();
    let t = thread::spawn(move || {
        publisher
            .publish(&ev("test.bus.slow.ran"))
            .expect("publish");
    });
    entered_rx
        .recv_timeout(Duration::from_secs(5))
        .expect("handler entered");
    (id, release_tx, t)
}

// Pins current behaviour, which differs from Go: Go's unsubscribe and
// Close take the write lock and so wait for an in-flight sync dispatch.
// Here they return while the handler is still running.
#[test]
fn unsubscribe_returns_while_its_handler_is_in_flight() {
    let bus = SharedBus::default();
    let (id, release, publisher) = publish_with_blocked_handler(&bus);
    let b = bus.clone();
    finishes(move || assert!(b.unsubscribe(id)));
    release.send(()).unwrap();
    publisher.join().expect("publisher");
}

#[test]
fn close_returns_while_a_handler_is_in_flight() {
    let bus = SharedBus::default();
    let (_, release, publisher) = publish_with_blocked_handler(&bus);
    let b = bus.clone();
    finishes(move || b.close());
    assert!(bus.is_closed());
    release.send(()).unwrap();
    publisher.join().expect("publisher");
}
