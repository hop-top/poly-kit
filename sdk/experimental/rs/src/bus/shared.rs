//! [`SharedBus`]: the thread-safe bus, with draining unsubscribe and
//! close.

use std::cell::RefCell;
use std::sync::atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering};
use std::sync::{Arc, Condvar, Mutex, MutexGuard, PoisonError};

use super::context::{Context, ContextError};
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
///   so on return its handler is neither running nor about to run. Go
///   waits for every in-flight sync dispatch, matching or not; this
///   returns while an unrelated handler may still be running. That is
///   observable, but promises nothing Go's unsubscribe does not.
/// - **Close** marks the bus closed, drops the subscriptions, and waits
///   for every in-flight sync publish with no deadline, as Go's
///   `mu.Lock()` does: when it returns, no sync handler is running,
///   whatever `ctx` says. `ctx` bounds only the async drain that
///   follows, as in Go; async dispatch has not landed, so today that
///   drain is empty and [`SharedBus::close_ctx`] always succeeds.
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
/// to the caller and the waits above never block: close and unsubscribe
/// return at once. No clock is read there.
///
/// # Async dispatch
///
/// `async_in_flight` is the drain point async dispatch joins: an async
/// handler task will count itself in after it acquires a pool slot,
/// and close waits for it under `ctx` after the sync drain.
#[derive(Clone)]
pub struct SharedBus(Arc<Inner>);

struct Inner {
    /// Tells this bus's frames apart in [`DISPATCHING`].
    id: u64,
    enforce: Mode,
    on_invalid: Option<SharedErrFn>,
    state: Mutex<State>,
    /// Signalled when an in-flight publish finishes, or when a context
    /// a waiter watches is canceled.
    idle: Condvar,
}

struct State {
    subs: Vec<Arc<Sub>>,
    next_id: u64,
    closed: bool,
    /// Admitted publishes whose handlers have not all returned.
    in_flight: usize,
    /// Async handler tasks still running. Always zero until async
    /// dispatch lands; close's `ctx` bounds the wait on it.
    async_in_flight: usize,
}

struct Sub {
    id: SubscriptionId,
    pattern: String,
    handler: SharedHandler,
    /// In-flight publishes whose snapshot holds this subscription.
    /// Changed only under the state lock.
    holds: AtomicUsize,
    /// Set by a re-entrant unsubscribe; snapshots skip it from then on.
    removed: AtomicBool,
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
                    handler: s.handler,
                    holds: AtomicUsize::new(0),
                    removed: AtomicBool::new(false),
                })
            })
            .collect();
        SharedBus(Arc::new(Inner {
            id: NEXT_BUS.fetch_add(1, Ordering::Relaxed),
            enforce: bus.enforce,
            on_invalid: bus.on_invalid,
            state: Mutex::new(State {
                subs,
                next_id: bus.next_id,
                closed: bus.closed,
                in_flight: 0,
                async_in_flight: 0,
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
        let pattern = pattern.into();
        let handler: SharedHandler = Arc::new(handler);
        let mut st = self.0.lock();
        let id = SubscriptionId(st.next_id);
        st.next_id += 1;
        st.subs.push(Arc::new(Sub {
            id,
            pattern,
            handler,
            holds: AtomicUsize::new(0),
            removed: AtomicBool::new(false),
        }));
        id
    }

    /// Removes a subscription. Returns whether it was found.
    ///
    /// Waits until no in-flight publish can still call the handler,
    /// except when called from a handler on this bus; see the
    /// [type docs](SharedBus).
    pub fn unsubscribe(&self, id: SubscriptionId) -> bool {
        let inner = &*self.0;
        let reentrant = own_frames(inner.id) > 0;
        let mut st = inner.lock();
        let Some(i) = st.subs.iter().position(|s| s.id == id) else {
            return false;
        };
        let sub = st.subs.remove(i);
        if reentrant {
            sub.removed.store(true, Ordering::Release);
        } else {
            let background = Context::background();
            let drained = |_: &State| sub.holds.load(Ordering::Relaxed) == 0;
            st = inner.wait(st, &background, drained).0;
        }
        drop(st);
        drop(sub);
        true
    }

    /// [`SharedBus::publish_ctx`] with [`Context::background`].
    pub fn publish(&self, event: &Event) -> Result<(), BusError> {
        self.publish_ctx(&Context::background(), event)
    }

    /// Publishes `event` to the matching handlers, passing them `ctx`.
    /// See [`Bus::publish_ctx`] for the checks and veto rule.
    ///
    /// The reporter and handlers run with no lock held, so they may
    /// re-enter this bus.
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
                s.holds.fetch_add(1, Ordering::Relaxed);
            }
            st.in_flight += 1;
            subs
        };
        let dispatch = InFlight::enter(inner, subs);
        for s in &dispatch.subs {
            if !s.removed.load(Ordering::Acquire) {
                (s.handler)(ctx, event)?;
            }
        }
        Ok(())
    }

    /// [`SharedBus::close_ctx`] with [`Context::background`]. Go's
    /// `Close(context.Background())`, which cannot fail either.
    pub fn close(&self) {
        // A background context is never done, so the drain cannot fail.
        let _ = self.close_ctx(&Context::background());
    }

    /// Stops the bus accepting publishes, drops every subscription,
    /// waits for in-flight sync publishes with no deadline, then waits
    /// for async handlers until `ctx` is done. Go's `Close(ctx)`.
    ///
    /// Returns [`BusError::Context`] with Go's `ctx.Err()` text if `ctx`
    /// is done before the async drain finishes; the bus is closed either
    /// way, and no sync handler is running. Async dispatch has not
    /// landed, so today this always returns `Ok`. Safe to call more than
    /// once. See the [type docs](SharedBus) for calls from a handler.
    pub fn close_ctx(&self, ctx: &Context) -> Result<(), BusError> {
        let inner = &*self.0;
        let own = own_frames(inner.id);
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
        // Go holds `mu.Lock()` here: the sync drain ignores `ctx`.
        let background = Context::background();
        let st = inner.wait(st, &background, |st| st.in_flight <= own).0;
        let (st, drained) = inner.wait(st, ctx, |st| st.async_in_flight == 0);
        drop(st);
        drop(subs);
        drained.map_err(BusError::from)
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
    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(PoisonError::into_inner)
    }

    /// Blocks until `done` holds or `ctx` is done.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    fn wait<'a>(
        &'a self,
        mut st: MutexGuard<'a, State>,
        ctx: &Context,
        done: impl Fn(&State) -> bool,
    ) -> (MutexGuard<'a, State>, Result<(), ContextError>) {
        loop {
            if done(&st) {
                return (st, Ok(()));
            }
            if let Some(e) = ctx.err() {
                return (st, Err(e));
            }
            st = match ctx.deadline() {
                Some(d) => {
                    let left = d.saturating_duration_since(super::context::now());
                    let woke = self.idle.wait_timeout(st, left);
                    woke.unwrap_or_else(PoisonError::into_inner).0
                }
                None => self.idle.wait(st).unwrap_or_else(PoisonError::into_inner),
            };
        }
    }

    /// One thread: every in-flight publish is the caller's own, which
    /// `done` already discounts, so there is never anything to wait for
    /// and blocking would hang the only thread.
    #[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
    fn wait<'a>(
        &'a self,
        st: MutexGuard<'a, State>,
        ctx: &Context,
        done: impl Fn(&State) -> bool,
    ) -> (MutexGuard<'a, State>, Result<(), ContextError>) {
        if done(&st) {
            return (st, Ok(()));
        }
        debug_assert!(ctx.err().is_some(), "bus: wait would block the only thread");
        let res = ctx.err().map_or(Ok(()), Err);
        (st, res)
    }
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
                s.holds.fetch_sub(1, Ordering::Relaxed);
            }
            st.in_flight -= 1;
        }
        self.inner.idle.notify_all();
    }
}
