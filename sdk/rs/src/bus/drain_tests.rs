//! The single-threaded policy that wasm32-unknown-unknown selects, run
//! natively. tests/bus_wasm.rs runs the same flow on wasm32 itself.

use std::collections::VecDeque;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use std::task::{Context as TaskCx, Waker};
use std::thread;
use std::time::Duration;

use serde_json::json;

use super::*;
use crate::bus::{Bus, BusError, Event, SharedBus, Spawn, Task};

const WATCHDOG: Duration = Duration::from_secs(10);

/// Runs `f` on its own thread; a call that blocks fails the test.
fn within<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(f());
    });
    rx.recv_timeout(WATCHDOG).expect("call blocked")
}

/// A bus with the policy wasm32-unknown-unknown selects.
fn single_threaded(max_async: usize) -> SharedBus {
    let mut bus = SharedBus::new(Bus::builder().max_async(max_async).build());
    Arc::get_mut(&mut bus.0).expect("fresh bus").threading = Threading::Single;
    bus
}

/// A queue run by hand, as a single-threaded executor runs tasks only
/// once the caller yields.
#[derive(Clone, Default)]
struct Queue(Arc<Mutex<VecDeque<Task>>>);

impl Queue {
    fn spawner(&self) -> Arc<dyn Spawn> {
        let q = Arc::clone(&self.0);
        Arc::new(move |t: Task| q.lock().unwrap().push_back(t))
    }

    fn len(&self) -> usize {
        self.0.lock().unwrap().len()
    }

    fn run(&self) {
        let mut cx = TaskCx::from_waker(Waker::noop());
        let tasks: Vec<Task> = self.0.lock().unwrap().drain(..).collect();
        for mut t in tasks {
            assert!(t.as_mut().poll(&mut cx).is_ready(), "one poll");
        }
    }
}

fn subscribed(max_async: usize) -> (SharedBus, Queue, Arc<AtomicUsize>) {
    let bus = single_threaded(max_async);
    let queue = Queue::default();
    let ran = Arc::new(AtomicUsize::new(0));
    let r = Arc::clone(&ran);
    bus.subscribe_async("test.event", queue.spawner(), move |_, _| {
        let r = Arc::clone(&r);
        async move {
            r.fetch_add(1, Ordering::SeqCst);
        }
    });
    (bus, queue, ran)
}

fn publish(bus: &SharedBus, n: usize) {
    let b = bus.clone();
    within(move || {
        for _ in 0..n {
            b.publish(&Event::new("test.event", "src", json!(null)))
                .unwrap();
        }
    });
}

fn close(bus: &SharedBus, ctx: Context) -> Result<(), BusError> {
    let b = bus.clone();
    within(move || b.close_ctx(&ctx))
}

#[test]
fn only_a_multi_threaded_pool_is_capped() {
    assert!(Threading::Multi.admits(1, 2));
    assert!(!Threading::Multi.admits(2, 2));
    assert!(Threading::Single.admits(2, 2));
    assert!(Threading::Single.admits(usize::MAX - 1, 1));
}

#[test]
fn the_target_picks_the_policy() {
    let wasm = cfg!(all(target_arch = "wasm32", target_os = "unknown"));
    let want = if wasm {
        Threading::Single
    } else {
        Threading::Multi
    };
    assert_eq!(Threading::TARGET, want);
}

#[test]
fn an_undrained_check_with_a_live_context_would_block() {
    let live = Context::background();
    assert!(check(true, &live).is_ok());
    assert!(matches!(check(false, &live), Err(Undrained::WouldBlock)));
    let (done, cancel) = live.with_cancel();
    cancel.cancel();
    assert!(matches!(
        check(false, &done),
        Err(Undrained::Ctx(ContextError::Canceled))
    ));
}

#[test]
fn single_threaded_publish_never_waits_for_a_slot() {
    let (bus, queue, ran) = subscribed(1);
    publish(&bus, 3);
    assert_eq!(bus.async_in_flight(), 3, "pool uncapped");
    assert_eq!(queue.len(), 3);
    assert_eq!(ran.load(Ordering::SeqCst), 0, "tasks wait for the caller");
    queue.run();
    assert_eq!(ran.load(Ordering::SeqCst), 3);
    assert_eq!(bus.async_in_flight(), 0);
}

#[test]
fn single_threaded_close_reports_not_drained_then_drains() {
    let (bus, queue, ran) = subscribed(1);
    publish(&bus, 3);
    let err = close(&bus, Context::background()).unwrap_err();
    assert!(matches!(err, BusError::NotDrained(3)), "{err:?}");
    assert!(bus.is_closed());
    // Admitted before close, so they still run.
    queue.run();
    assert_eq!(ran.load(Ordering::SeqCst), 3);
    close(&bus, Context::background()).expect("drained");
}

#[test]
fn single_threaded_close_with_a_done_context_reports_it() {
    let (bus, queue, _ran) = subscribed(1);
    publish(&bus, 1);
    let (ctx, cancel) = Context::background().with_cancel();
    cancel.cancel();
    let err = close(&bus, ctx).unwrap_err();
    assert_eq!(err.to_string(), "context canceled");
    queue.run();
    close(&bus, Context::background()).expect("drained");
}

#[test]
fn single_threaded_unsubscribe_and_close_with_nothing_queued_return_at_once() {
    let (bus, _queue, _ran) = subscribed(1);
    let b = bus.clone();
    let id = b.subscribe("#", |_| Ok(()));
    assert!(within(move || b.unsubscribe(id)));
    close(&bus, Context::background()).expect("nothing to drain");
}
