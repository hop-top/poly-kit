//! Async dispatch: [`SharedBus::subscribe_async`], the bounded pool,
//! and the spawner seam.
//!
//! Go runs each async handler on its own goroutine, drawn from a
//! semaphore of `WithMaxAsync` slots (default 256), and `Close(ctx)`
//! waits on a `WaitGroup` for them until `ctx` is done. This port keeps
//! that model without an async runtime:
//!
//! - The caller hands [`SharedBus::subscribe_async`] a [`Spawn`], which
//!   puts a [`Task`] on whatever executor it likes: a tokio runtime, a
//!   thread pool, `wasm_bindgen_futures::spawn_local`, or a test queue.
//!   Any `Fn(Task) + Send + Sync` closure is a [`Spawn`].
//! - The pool is a counter under the bus mutex: a publish takes a slot
//!   before it spawns, and the task gives it back when it finishes or is
//!   dropped, run or not. Close waits on the same counter.
//!
//! A tokio adapter, behind its own feature, can come later; it would be
//! a [`Spawn`] that calls `tokio::spawn` or `Handle::spawn`.
//!
//! # Mirrored from Go
//!
//! - Async handlers are launched after every sync handler of the publish
//!   succeeded, in subscription order; a sync veto launches none. They
//!   receive the publisher's context and a copy of the event.
//! - They return nothing, so they cannot veto or fail the publish: Go's
//!   `AsyncHandler` has no error result, and nothing reports for it.
//! - The pool holds [`SharedBus::max_async`] running handlers: Go's
//!   `WithMaxAsync`, set by [`BusBuilder::max_async`], default
//!   [`DEFAULT_MAX_ASYNC`], zero meaning the default. A publish takes its
//!   slots after its sync handlers, out of the sync drain, so close can
//!   always proceed (Go releases its read lock first, for the same
//!   reason). While the pool is full the publisher blocks, as Go's
//!   semaphore send does.
//! - With a slot taken the publish re-checks closed, and on a closed bus
//!   returns `Ok` without launching this or any later handler. Only then
//!   is the task counted, so close never waits for a task that was not
//!   launched (Go's `wg.Add` after the slot).
//! - Close waits for counted tasks until `ctx` is done, then returns Go's
//!   `ctx.Err()` text; the bus stays closed and the tasks run on.
//! - Unsubscribe never waits for running tasks; they finish on their own.
//!
//! # Where this differs from Go
//!
//! - A publisher blocked on a full pool returns as soon as close starts,
//!   where Go's waits for a slot first. Both return `Ok` and launch
//!   nothing.
//! - Once unsubscribe returns, no task for that subscription is
//!   admitted; Go may still launch one from a publish that matched
//!   before. Tasks already admitted run on, in both.
//! - A task is counted until it finishes or the executor drops it. A
//!   handler that panics ends its task as the executor handles panics,
//!   where a goroutine's panic ends the Go process.
//! - Close called from an async handler does not wait for that handler,
//!   where Go waits for it until `ctx` is done. It still waits for every
//!   other task, so two async handlers that close at once wait for each
//!   other until `ctx` is done; with a background `ctx` that is forever,
//!   as in Go. Close from a handler with a deadline, or from outside.
//! - The executor is the caller's: see [`Spawn`] for what it must do.
//!
//! # wasm32-unknown-unknown
//!
//! That target has one thread and no `Send` futures, so [`Task`] drops
//! the `Send` bound there and handlers' futures need not be `Send` (see
//! [`MaybeSend`]). The handler closure itself must still be
//! `Send + Sync`, as [`SharedBus`] is: it cannot capture an `Rc` or a
//! `JsValue`, so build such state inside the future it returns.
//! Blocking the only thread would hang it, so:
//!
//! - the pool is uncapped: a publish never waits for a slot;
//! - close never waits for tasks, which run only once the caller
//!   yields. With tasks still running and `ctx` live it returns
//!   [`BusError::NotDrained`], the bus closed; call it again once the
//!   executor has run them.
//!
//! The target only selects this policy; it is a value every target
//! builds, so native tests run it too, and tests/bus_wasm.rs runs it on
//! wasm32 under Node (`make test-rs-wasm`).
//!
//! [`BusBuilder::max_async`]: super::BusBuilder::max_async

use std::cell::RefCell;
use std::future::Future;
use std::pin::Pin;
use std::sync::atomic::Ordering;
use std::sync::{Arc, PoisonError};
use std::task::{Context as TaskCx, Poll};

use super::context::Context;
use super::event::Event;
#[cfg(doc)]
use super::mem::BusError;
use super::mem::SubscriptionId;
use super::shared::{Inner, Kind, SharedBus, Sub};

/// The pool size when none is configured, or zero is: Go's
/// `defaultMaxAsync`.
pub const DEFAULT_MAX_ASYNC: usize = 256;

/// A boxed async handler invocation, handed to [`Spawn::spawn`].
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub type Task = Pin<Box<dyn Future<Output = ()> + Send + 'static>>;

/// A boxed async handler invocation, handed to [`Spawn::spawn`]. Not
/// `Send` on this single-threaded target.
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
pub type Task = Pin<Box<dyn Future<Output = ()> + 'static>>;

/// Runs [`Task`]s. The executor seam for async subscriptions.
///
/// The executor must poll a task independently of the thread that
/// spawned it, or drop it: a publish whose pool is full waits for a
/// running task to finish, so a task that only runs once the publisher
/// returns would never free its slot. Tokio's multi-thread runtime and
/// a thread per task both qualify; a current-thread runtime driven by
/// the publisher does not, unless the pool never fills.
///
/// Dropping a task without running it is allowed; its slot is freed.
pub trait Spawn: Send + Sync {
    /// Schedules `task` to run to completion.
    fn spawn(&self, task: Task);
}

impl<F: Fn(Task) + Send + Sync> Spawn for F {
    fn spawn(&self, task: Task) {
        self(task)
    }
}

/// `Send` everywhere but wasm32-unknown-unknown, where it is implemented
/// for every type. Bounds async handlers' futures, so they need not be
/// `Send` on that single-threaded target.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub trait MaybeSend: Send {}
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
impl<T: Send + ?Sized> MaybeSend for T {}

/// `Send` everywhere but wasm32-unknown-unknown, where it is implemented
/// for every type.
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
pub trait MaybeSend {}
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
impl<T: ?Sized> MaybeSend for T {}

/// An async handler, boxed: builds the future for one event.
pub(super) type AsyncHandler = Arc<dyn Fn(Context, Event) -> Task + Send + Sync>;

thread_local! {
    /// Buses whose async task this thread is polling, innermost last.
    static RUNNING: RefCell<Vec<u64>> = const { RefCell::new(Vec::new()) };
}

/// How many async tasks of bus `id` this thread is polling: a close
/// called from one of them must not wait for itself.
pub(super) fn own_tasks(id: u64) -> usize {
    RUNNING.with(|r| r.borrow().iter().filter(|&&b| b == id).count())
}

/// One admitted async handler invocation, holding a pool slot until it
/// finishes or is dropped.
pub(super) struct AsyncTask {
    bus: u64,
    /// The slot's owner; taken when the slot is given back.
    slot: Option<Arc<Inner>>,
    state: State,
}

enum State {
    /// Not yet polled. The handler is called on the first poll, inside
    /// the task, as Go calls it inside the goroutine.
    Start(Box<(AsyncHandler, Context, Event)>),
    Running(Task),
    Done,
}

impl AsyncTask {
    /// Wraps a handler call for which a slot was already taken.
    pub(super) fn boxed(inner: Arc<Inner>, h: AsyncHandler, ctx: Context, e: Event) -> Task {
        Box::pin(AsyncTask {
            bus: inner.id,
            slot: Some(inner),
            state: State::Start(Box::new((h, ctx, e))),
        })
    }

    /// Drops the handler state, then gives the slot back, so close
    /// returns only after the handler's future is gone.
    fn finish(&mut self) {
        self.state = State::Done;
        if let Some(inner) = self.slot.take() {
            inner.finish_async();
        }
    }
}

impl Future for AsyncTask {
    type Output = ();

    fn poll(self: Pin<&mut Self>, cx: &mut TaskCx<'_>) -> Poll<()> {
        let this = self.get_mut();
        let _frame = Frame::enter(this.bus);
        if let State::Start(..) = this.state {
            if let State::Start(start) = std::mem::replace(&mut this.state, State::Done) {
                let (h, ctx, e) = *start;
                this.state = State::Running(h(ctx, e));
            }
        }
        let State::Running(fut) = &mut this.state else {
            return Poll::Ready(());
        };
        let poll = fut.as_mut().poll(cx);
        if poll.is_ready() {
            this.finish();
        }
        poll
    }
}

impl Drop for AsyncTask {
    fn drop(&mut self) {
        self.finish();
    }
}

/// Marks this thread as polling a task of one bus; popped on return or
/// unwind.
struct Frame;

impl Frame {
    fn enter(id: u64) -> Frame {
        RUNNING.with(|r| r.borrow_mut().push(id));
        Frame
    }
}

impl Drop for Frame {
    fn drop(&mut self) {
        let _ = RUNNING.try_with(|r| r.borrow_mut().pop());
    }
}

impl SharedBus {
    /// Registers an async handler for every event whose topic matches
    /// `pattern`: Go's `SubscribeAsync`. Each matching publish calls
    /// `handler` with its context and a copy of the event, on a task
    /// `spawner` runs, holding a pool slot. See the
    /// [`async_dispatch`](self) module.
    pub fn subscribe_async<F, Fut>(
        &self,
        pattern: impl Into<String>,
        spawner: Arc<dyn Spawn>,
        handler: F,
    ) -> SubscriptionId
    where
        F: Fn(Context, Event) -> Fut + Send + Sync + 'static,
        Fut: Future<Output = ()> + MaybeSend + 'static,
    {
        let handler: AsyncHandler = Arc::new(move |ctx, e| Box::pin(handler(ctx, e)) as Task);
        self.add(pattern.into(), Kind::Async(spawner, handler))
    }

    /// The pool size: how many async handlers may run at once. Go's
    /// `WithMaxAsync`, resolved.
    pub fn max_async(&self) -> usize {
        self.0.max_async
    }

    /// Launches the async handlers of one publish, in order, once its
    /// sync phase is over.
    pub(super) fn launch(&self, ctx: &Context, event: &Event, subs: Vec<Arc<Sub>>) {
        for s in subs {
            let Kind::Async(spawner, handler) = &s.kind else {
                continue;
            };
            match self.acquire_slot(&s) {
                Slot::Taken => {}
                Slot::Removed => continue,
                Slot::Closed => return,
            }
            let h = Arc::clone(handler);
            spawner.spawn(AsyncTask::boxed(
                Arc::clone(&self.0),
                h,
                ctx.clone(),
                event.clone(),
            ));
        }
    }

    /// Takes a pool slot for `s`, waiting while the pool is full (never
    /// on wasm32-unknown-unknown, where it is uncapped), and counts the
    /// task in under the same lock: Go's semaphore send, closed re-check
    /// and `wg.Add`.
    fn acquire_slot(&self, s: &Sub) -> Slot {
        let inner = &*self.0;
        let mut st = inner.lock();
        loop {
            if st.closed {
                return Slot::Closed;
            }
            if s.removed.load(Ordering::Acquire) {
                return Slot::Removed;
            }
            if inner.threading.admits(st.async_in_flight, inner.max_async) {
                st.async_in_flight += 1;
                return Slot::Taken;
            }
            st.slot_waiters += 1;
            st = inner.idle.wait(st).unwrap_or_else(PoisonError::into_inner);
            st.slot_waiters -= 1;
        }
    }
}

/// Outcome of [`SharedBus::acquire_slot`].
enum Slot {
    Taken,
    /// Unsubscribed since the snapshot: skip this handler.
    Removed,
    /// Closed: launch nothing more for this publish.
    Closed,
}
