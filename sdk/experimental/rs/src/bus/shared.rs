//! [`SharedBus`]: the thread-safe bus, with async subscriptions and
//! draining unsubscribe and close.

use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex, MutexGuard, PoisonError};

use super::async_dispatch::{own_tasks, AsyncHandler, Spawn, DEFAULT_MAX_ASYNC};
use super::context::Context;
use super::drain::{Threading, Undrained};
use super::event::Event;
use super::mem::{check_topic, Bus, BusError, Mode, SharedErrFn, SharedHandler, SubscriptionId};

/// A thread-safe [`Bus`] with `&self` methods, shared by cheap clones.
///
/// Subscribing on a plain [`Bus`] needs `&mut`, which is awkward when
/// handlers are registered in one place and events published from
/// another. Unsubscribe and close drain in-flight dispatch, under the
/// rules below.
///
/// # Go's model, and where this one differs
///
/// Go's `memBus` runs sync handlers under a read lock; `Unsubscribe`
/// and `Close` take the write lock, so they wait for every in-flight
/// sync dispatch, and `Close(ctx)` then waits for async handlers until
/// `ctx` is done. Any call back into the bus from a handler other than
/// a nested publish deadlocks there.
///
/// Here a publish snapshots its matching subscriptions under a mutex,
/// counts itself in flight, and runs the handlers with the mutex
/// released. Unsubscribe and close wait on that count instead of on a
/// lock. The rules:
///
/// - **Publish** is admitted after Go's checks, topic then closed, and
///   never reads its context: a canceled or expired context is passed
///   to the handlers like any other, as in Go. Once admitted, a publish
///   is in flight until its last handler returns, and delivers to its
///   whole snapshot.
/// - **Unsubscribe** stops new publishes from matching at once, then
///   waits for every in-flight publish that matched the subscription,
///   so on return its handler is neither running nor about to run. It
///   waits even when it returns `false` because close or another
///   unsubscribe removed the subscription first: Go's closure takes the
///   write lock either way. Go waits for every in-flight sync dispatch,
///   matching or not; this returns while an unrelated handler may still
///   be running. That is observable, but promises nothing Go's
///   unsubscribe does not.
/// - **Close** marks the bus closed, drops the subscriptions, and waits
///   for every in-flight sync publish with no deadline, as Go's
///   `mu.Lock()` does: when it returns, no sync handler is running,
///   whatever `ctx` says. `ctx` bounds only the async drain that
///   follows, as in Go: see [Async dispatch](#async-dispatch). When
///   `ctx` is already done and nothing is left to drain, Go's `select`
///   returns nil or `ctx.Err()` at random; this returns `Ok`.
/// - **Subscribe after close** registers nothing: the handler is dropped
///   at once and the returned id is unknown to [`SharedBus::unsubscribe`].
///   Go appends it to a bus that will never call it; here a handler that
///   holds a clone of the bus cannot keep it alive through that cycle.
///
/// # Re-entrancy
///
/// A handler may publish, subscribe, unsubscribe and close on the bus
/// dispatching to it; Go deadlocks on all but the nested publish.
///
/// - A nested publish delivers in full before the outer one continues,
///   and its veto reaches the handler as an ordinary error, as in Go.
/// - Waits skip the calling thread's own in-flight publishes, which
///   cannot finish until the call returns.
/// - Unsubscribe from a handler of this bus does not wait at all. It
///   takes effect at once: no invocation of the removed handler starts
///   afterwards on this thread, including later in the current
///   publish, or on another thread that has not yet reached it in its
///   snapshot. An invocation already running, or one on another thread
///   that has passed that check, may still run to completion. A
///   dispatching thread therefore never blocks in unsubscribe, so
///   handlers removing each other across threads cannot deadlock.
/// - Close from a handler waits for other threads' sync publishes, with
///   no deadline. Two handlers on different threads closing the bus at
///   once therefore wait for each other forever, as any re-entrant
///   close does in Go; close from outside a handler instead.
///
/// No lock is held while a handler, reporter or handler drop runs. A
/// removed handler is dropped by whichever call releases the last
/// reference: unsubscribe, close, or the last in-flight publish.
///
/// A handler that panics unwinds through `publish`; the publish still
/// leaves the in-flight count, so a later close is not wedged.
///
/// # wasm32-unknown-unknown
///
/// That target is single-threaded, so every in-flight publish belongs
/// to the caller and the sync waits above never block: unsubscribe
/// returns at once. No clock is read there. Close cannot block for async
/// tasks either, since they run only when the caller yields: with tasks
/// still running and `ctx` live it returns [`BusError::NotDrained`]
/// instead of waiting. Call it again once the executor has run them.
///
/// # Async dispatch
///
/// [`SharedBus::subscribe_async`] is Go's `SubscribeAsync`. The pool,
/// the launch order and close's async drain, and where they differ from
/// Go, are set out in the [`async_dispatch`](super::async_dispatch)
/// module.
#[derive(Clone)]
pub struct SharedBus(pub(super) Arc<Inner>);

pub(super) struct Inner {
    /// Tells this bus's frames apart in [`DISPATCHING`] and in async
    /// task frames.
    pub(super) id: u64,
    enforce: Mode,
    on_invalid: Option<SharedErrFn>,
    /// Pool size: async tasks counted at once, at most.
    pub(super) max_async: usize,
    /// Whether waits may block; set by the target.
    pub(super) threading: Threading,
    state: Mutex<State>,
    /// Signalled when an in-flight publish or async task finishes, when
    /// the bus closes, or when a context a waiter watches is canceled.
    pub(super) idle: Condvar,
}

pub(super) struct State {
    subs: Vec<Arc<Sub>>,
    next_id: u64,
    pub(super) closed: bool,
    /// Admitted publishes whose handlers have not all returned.
    in_flight: usize,
    /// Async tasks holding a pool slot: launched, not yet finished or
    /// dropped. Close's `ctx` bounds the wait on it.
    pub(super) async_in_flight: usize,
    /// In-flight publishes per subscription in their snapshot. Kept by
    /// id, not on the subscription, so an unsubscribe that no longer
    /// finds the subscription can still wait for it.
    holds: HashMap<SubscriptionId, usize>,
    /// Threads blocked in an unsubscribe or close drain.
    drain_waiters: usize,
    /// Publishers blocked on a full pool.
    pub(super) slot_waiters: usize,
}

pub(super) struct Sub {
    id: SubscriptionId,
    pattern: String,
    pub(super) kind: Kind,
    /// Set by a re-entrant unsubscribe, and by any unsubscribe of an
    /// async subscription; snapshots and launches skip it from then on.
    pub(super) removed: AtomicBool,
}

pub(super) enum Kind {
    Sync(SharedHandler),
    Async(Arc<dyn Spawn>, AsyncHandler),
}

static NEXT_BUS: AtomicU64 = AtomicU64::new(0);

thread_local! {
    /// Buses this thread is dispatching on, innermost last.
    static DISPATCHING: RefCell<Vec<u64>> = const { RefCell::new(Vec::new()) };
}

/// How many of this thread's in-flight publishes are on bus `id`.
fn own_frames(id: u64) -> usize {
    DISPATCHING.with(|d| d.borrow().iter().filter(|&&b| b == id).count())
}

impl Default for SharedBus {
    fn default() -> Self {
        Self::new(Bus::new())
    }
}

impl SharedBus {
    /// Wraps `bus`, keeping its subscriptions, mode, reporter and closed
    /// state.
    pub fn new(bus: Bus) -> Self {
        let subs = bus
            .subs
            .into_iter()
            .map(|s| {
                Arc::new(Sub {
                    id: s.id,
                    pattern: s.pattern,
                    kind: Kind::Sync(s.handler),
                    removed: AtomicBool::new(false),
                })
            })
            .collect();
        SharedBus(Arc::new(Inner {
            id: NEXT_BUS.fetch_add(1, Ordering::Relaxed),
            enforce: bus.enforce,
            on_invalid: bus.on_invalid,
            max_async: match bus.max_async {
                0 => DEFAULT_MAX_ASYNC,
                n => n,
            },
            threading: Threading::TARGET,
            state: Mutex::new(State {
                subs,
                next_id: bus.next_id,
                closed: bus.closed,
                in_flight: 0,
                async_in_flight: 0,
                holds: HashMap::new(),
                drain_waiters: 0,
                slot_waiters: 0,
            }),
            idle: Condvar::new(),
        }))
    }

    /// Registers a handler that ignores the publish context. See
    /// [`Bus::subscribe`].
    pub fn subscribe(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        self.subscribe_ctx(pattern, move |_, e| handler(e))
    }

    /// Registers a handler that receives the publish context. See
    /// [`Bus::subscribe_ctx`].
    pub fn subscribe_ctx(
        &self,
        pattern: impl Into<String>,
        handler: impl Fn(&Context, &Event) -> Result<(), BusError> + Send + Sync + 'static,
    ) -> SubscriptionId {
        // `into` is caller code: run it before taking the lock.
        self.add(pattern.into(), Kind::Sync(Arc::new(handler)))
    }

    pub(super) fn add(&self, pattern: String, kind: Kind) -> SubscriptionId {
        let mut st = self.0.lock();
        let id = SubscriptionId(st.next_id);
        st.next_id += 1;
        if st.closed {
            // Dropped after the lock: a handler drop is caller code.
            drop(st);
            drop(kind);
            return id;
        }
        st.subs.push(Arc::new(Sub {
            id,
            pattern,
            kind,
            removed: AtomicBool::new(false),
        }));
        id
    }

    /// Removes a subscription. Returns whether it was found.
    ///
    /// Whether or not it is found (close may have dropped it, or another
    /// unsubscribe won), waits until no in-flight publish can still call
    /// the handler, as Go's closure always takes the write lock. A call
    /// from a handler on this bus does not wait; see the
    /// [type docs](SharedBus).
    pub fn unsubscribe(&self, id: SubscriptionId) -> bool {
        let inner = &*self.0;
        let reentrant = own_frames(inner.id) > 0;
        let mut st = inner.lock();
        let sub = st
            .subs
            .iter()
            .position(|s| s.id == id)
            .map(|i| st.subs.remove(i));
        if let Some(sub) = &sub {
            // Under the lock, so no launch admits it from now on.
            if reentrant || matches!(sub.kind, Kind::Async(..)) {
                sub.removed.store(true, Ordering::Release);
            }
        }
        if !reentrant {
            let background = Context::background();
            let drained = |st: &State| !st.holds.contains_key(&id);
            st = inner.wait(st, &background, drained, drain_waiters).0;
        }
        drop(st);
        sub.is_some()
    }

    /// Threads currently blocked in an unsubscribe or close drain. For
    /// tests that must know a drain is under way before releasing a
    /// handler; not part of the supported API.
    #[doc(hidden)]
    pub fn drain_waiters(&self) -> usize {
        self.0.lock().drain_waiters
    }

    /// Publishers blocked on a full pool. For tests; not part of the
    /// supported API.
    #[doc(hidden)]
    pub fn slot_waiters(&self) -> usize {
        self.0.lock().slot_waiters
    }

    /// Async tasks holding a pool slot. For tests; not part of the
    /// supported API.
    #[doc(hidden)]
    pub fn async_in_flight(&self) -> usize {
        self.0.lock().async_in_flight
    }

    /// [`SharedBus::publish_ctx`] with [`Context::background`].
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        self.publish_ctx(&Context::background(), event)
    }

    /// Publishes `event` to the matching handlers, passing them `ctx`.
    /// See [`Bus::publish_ctx`] for the checks and veto rule.
    ///
    /// The reporter and handlers run with no lock held, so they may
    /// re-enter this bus. Matching async handlers are launched after the
    /// sync ones succeed; see [Async dispatch](#async-dispatch).
    pub fn publish_ctx(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        let inner = &*self.0;
        check_topic(inner.enforce, inner.on_invalid.as_ref(), &event.topic)?;
        let subs = {
            let mut st = inner.lock();
            if st.closed {
                return Err(BusError::Closed);
            }
            let subs: Vec<_> = st
                .subs
                .iter()
                .filter(|s| event.topic.matches(&s.pattern))
                .cloned()
                .collect();
            for s in &subs {
                *st.holds.entry(s.id).or_insert(0) += 1;
            }
            st.in_flight += 1;
            subs
        };
        let dispatch = InFlight::enter(inner, subs);
        for s in &dispatch.subs {
            if let Kind::Sync(h) = &s.kind {
                if !s.removed.load(Ordering::Acquire) {
                    h(ctx, event)?;
                }
            }
        }
        let asyncs: Vec<_> = dispatch
            .subs
            .iter()
            .filter(|s| matches!(s.kind, Kind::Async(..)))
            .cloned()
            .collect();
        // The sync phase ends here, as Go's RUnlock does: close and
        // unsubscribe stop waiting for this publish while it waits for
        // pool slots.
        drop(dispatch);
        self.launch(ctx, event, asyncs);
        Ok(())
    }

    /// [`SharedBus::close_ctx`] with [`Context::background`]. Go's
    /// `Close(context.Background())`, which cannot fail either.
    pub fn close(&self) {
        // A background context is never done, so natively the drain
        // cannot fail; on wasm32-unknown-unknown it may be NotDrained.
        let _ = self.close_ctx(&Context::background());
    }

    /// Stops the bus accepting publishes, drops every subscription,
    /// waits for in-flight sync publishes with no deadline, then waits
    /// for async handlers until `ctx` is done. Go's `Close(ctx)`.
    ///
    /// Returns [`BusError::Context`] with Go's `ctx.Err()` text if `ctx`
    /// is done before the async drain finishes; the bus is closed either
    /// way, no sync handler is running, and async handlers already
    /// launched run on. With `ctx` already done and nothing to drain it
    /// returns `Ok`, where Go's `select` picks at random. On
    /// wasm32-unknown-unknown it returns [`BusError::NotDrained`] rather
    /// than block. Safe to call more than once; a later call waits for
    /// what an earlier one left. A call from an async handler does not
    /// wait for that handler. See the [type docs](SharedBus) for calls
    /// from a sync handler.
    pub fn close_ctx(&self, ctx: &Context) -> Result<(), BusError> {
        let inner = &*self.0;
        let own = own_frames(inner.id);
        let own_async = own_tasks(inner.id);
        // Registered before the lock: a cancel between our check and our
        // wait then blocks on the lock until we wait, and wakes us.
        let weak = Arc::downgrade(&self.0);
        let _watch = ctx.on_cancel(Arc::new(move || {
            if let Some(inner) = weak.upgrade() {
                drop(inner.lock());
                inner.idle.notify_all();
            }
        }));
        let mut st = inner.lock();
        st.closed = true;
        let subs = std::mem::take(&mut st.subs);
        // Publishers blocked on a full pool see `closed` and bail.
        inner.idle.notify_all();
        // Go holds `mu.Lock()` here: the sync drain ignores `ctx`.
        let background = Context::background();
        let (st, res) = inner.wait(st, &background, |st| st.in_flight <= own, drain_waiters);
        // A close from an async handler does not wait for its own task.
        let (st, res) = match res {
            Ok(()) => inner.wait(st, ctx, |st| st.async_in_flight <= own_async, drain_waiters),
            Err(e) => (st, Err(e)),
        };
        let left = st.async_in_flight.saturating_sub(own_async);
        drop(st);
        drop(subs);
        res.map_err(|e| match e {
            Undrained::Ctx(e) => BusError::Context(e),
            Undrained::WouldBlock => BusError::NotDrained(left),
        })
    }

    /// Reports whether the bus is closed.
    pub fn is_closed(&self) -> bool {
        self.0.lock().closed
    }
}

impl Inner {
    // No user code runs under the lock and every update completes before
    // it is released, so a poisoned lock never holds a half-applied
    // change: recover the guard.
    pub(super) fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(PoisonError::into_inner)
    }

    /// Gives back an async task's pool slot and wakes waiters.
    pub(super) fn finish_async(&self) {
        self.lock().async_in_flight -= 1;
        self.idle.notify_all();
    }
}

pub(super) fn drain_waiters(st: &mut State) -> &mut usize {
    &mut st.drain_waiters
}

/// One admitted publish. Leaving it (on return, veto or panic) releases
/// the snapshot's holds and wakes waiters; the snapshot's handlers are
/// dropped afterwards, outside the lock.
struct InFlight<'a> {
    inner: &'a Inner,
    subs: Vec<Arc<Sub>>,
}

impl<'a> InFlight<'a> {
    fn enter(inner: &'a Inner, subs: Vec<Arc<Sub>>) -> Self {
        DISPATCHING.with(|d| d.borrow_mut().push(inner.id));
        InFlight { inner, subs }
    }
}

impl Drop for InFlight<'_> {
    fn drop(&mut self) {
        // Frames nest, so this publish's frame is the innermost.
        let _ = DISPATCHING.try_with(|d| d.borrow_mut().pop());
        {
            let mut st = self.inner.lock();
            for s in &self.subs {
                if let Some(n) = st.holds.get_mut(&s.id) {
                    *n -= 1;
                    if *n == 0 {
                        st.holds.remove(&s.id);
                    }
                }
            }
            st.in_flight -= 1;
        }
        self.inner.idle.notify_all();
    }
}
