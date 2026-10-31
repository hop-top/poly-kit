//! Test spawners for async dispatch, shared by `bus_async*.rs`.
//!
//! - [`threads`]: one OS thread per task, the closest thing to Go's
//!   goroutine-per-handler.
//! - [`Manual`]: a queue the test drains by hand, so the order tasks
//!   were spawned and run in is deterministic.
//!
//! Neither needs an async runtime; the crate ships none.

#![allow(dead_code)]

use std::collections::VecDeque;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use std::task::{Context as TaskCx, Wake, Waker};
use std::thread;
use std::time::{Duration, Instant};

use hop_top_kit::bus::{BusError, Event, SharedBus, Spawn, Task};
use serde_json::json;

pub const WATCHDOG: Duration = Duration::from_secs(10);

pub fn ev(topic: &str) -> Event {
    Event::new(topic, "src", json!(null))
}

/// Publishes `topic` under the watchdog: a publish stuck on a full pool
/// fails the test instead of hanging it.
pub fn publish(bus: &SharedBus, topic: &str) -> Result<(), BusError> {
    let (b, e) = (bus.clone(), ev(topic));
    within(move || b.publish(&e))
}

struct Unpark(thread::Thread);

impl Wake for Unpark {
    fn wake(self: Arc<Self>) {
        self.0.unpark();
    }
}

/// Polls `task` to completion on the current thread.
pub fn block_on(mut task: Task) {
    let waker = Waker::from(Arc::new(Unpark(thread::current())));
    let mut cx = TaskCx::from_waker(&waker);
    while task.as_mut().poll(&mut cx).is_pending() {
        thread::park();
    }
}

/// Runs every task on a thread of its own.
pub fn threads() -> Arc<dyn Spawn> {
    Arc::new(|task: Task| {
        thread::spawn(move || block_on(task));
    })
}

/// Queues tasks until the test runs them, in spawn order, on its own
/// thread.
#[derive(Clone, Default)]
pub struct Manual {
    queue: Arc<Mutex<VecDeque<Task>>>,
    spawned: Arc<AtomicUsize>,
}

impl Spawn for Manual {
    fn spawn(&self, task: Task) {
        self.spawned.fetch_add(1, Ordering::SeqCst);
        self.queue.lock().unwrap().push_back(task);
    }
}

impl Manual {
    pub fn spawner(&self) -> Arc<dyn Spawn> {
        Arc::new(self.clone())
    }

    /// Tasks spawned so far, run or not.
    pub fn spawned(&self) -> usize {
        self.spawned.load(Ordering::SeqCst)
    }

    /// Tasks waiting to run.
    pub fn queued(&self) -> usize {
        self.queue.lock().unwrap().len()
    }

    /// Polls queued tasks in order until every one has finished. The
    /// queue lock is released while a task runs, so a task may publish
    /// and spawn more.
    pub fn run(&self) {
        let mut cx = TaskCx::from_waker(Waker::noop());
        loop {
            let Some(mut task) = self.queue.lock().unwrap().pop_front() else {
                return;
            };
            assert!(
                task.as_mut().poll(&mut cx).is_ready(),
                "test tasks finish in one poll"
            );
        }
    }

    /// Drops every queued task without running it.
    pub fn discard(&self) {
        let tasks: Vec<_> = self.queue.lock().unwrap().drain(..).collect();
        drop(tasks);
    }
}

/// Runs `f` on another thread and returns its result, failing the test
/// if it has not returned before the watchdog fires.
pub fn within<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(f());
    });
    rx.recv_timeout(WATCHDOG).expect("call deadlocked")
}

/// Spins until `cond` holds; the deadline is a watchdog only.
pub fn until(what: &str, cond: impl Fn() -> bool) {
    let deadline = Instant::now() + WATCHDOG;
    while !cond() {
        assert!(Instant::now() < deadline, "never happened: {what}");
        thread::yield_now();
    }
}

/// Waits until `n` threads are blocked in a close or unsubscribe drain.
pub fn until_drain_waiters(bus: &SharedBus, n: usize) {
    until("drain waiters", || bus.drain_waiters() >= n);
}

/// Waits until `n` publishers are blocked on a full pool.
pub fn until_slot_waiters(bus: &SharedBus, n: usize) {
    until("slot waiters", || bus.slot_waiters() >= n);
}

/// A gate async handlers block on until the test opens it. Every
/// handler that reaches it reports on `entered`.
#[derive(Clone)]
pub struct Gate {
    open: Arc<(Mutex<bool>, std::sync::Condvar)>,
    entered: mpsc::Sender<()>,
}

impl Gate {
    pub fn new() -> (Gate, mpsc::Receiver<()>) {
        let (tx, rx) = mpsc::channel();
        let gate = Gate {
            open: Arc::new((Mutex::new(false), std::sync::Condvar::new())),
            entered: tx,
        };
        (gate, rx)
    }

    /// Reports arrival, then blocks until [`Gate::open`].
    pub fn pass(&self) {
        let _ = self.entered.send(());
        let (lock, cv) = &*self.open;
        let mut open = lock.lock().unwrap();
        while !*open {
            open = cv.wait(open).unwrap();
        }
    }

    pub fn open(&self) {
        let (lock, cv) = &*self.open;
        *lock.lock().unwrap() = true;
        cv.notify_all();
    }
}

/// Receives `n` arrivals, or fails on the watchdog.
pub fn entered(rx: &mpsc::Receiver<()>, n: usize) {
    for _ in 0..n {
        rx.recv_timeout(WATCHDOG).expect("handler never started");
    }
}
