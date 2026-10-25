//! Request-scoped [`Context`]: cancellation, a deadline and typed values.
//!
//! The Rust form of the `context.Context` that Go threads through
//! `Publish`, every handler and `Close`. The bus reads it in three
//! places: publish refuses a context that is already done, handlers
//! receive the publisher's context, and [`SharedBus::close_ctx`] bounds
//! its drain by it. Go's network adapter carries a peer's origin as a
//! context value; here that is [`Context::with_value`].
//!
//! It needs no async runtime. Cancellation is a flag plus wake-up
//! callbacks, and deadlines are checked lazily against the clock, so a
//! context works the same under threads, tokio or none at all.
//!
//! # Differences from Go
//!
//! - Values are keyed by type, not by an arbitrary key value. A private
//!   newtype gives the same isolation Go gets from an unexported key
//!   type.
//! - No timer goroutine: a deadline is noticed when [`Context::err`] is
//!   called, or when a waiter's timeout fires. Every observable result
//!   is the same.
//! - When several ancestors are done, the nearest one's error wins. Go
//!   reports whichever finished first; the two differ only when a
//!   cancel and a deadline race on different ancestors.
//!
//! # wasm32-unknown-unknown
//!
//! That target has no clock, so [`Context::with_deadline`] and
//! [`Context::with_timeout`] are not compiled for it, and a context
//! there never has a deadline. Cancellation and values work.
//!
//! [`SharedBus::close_ctx`]: super::SharedBus::close_ctx

use std::any::Any;
use std::fmt;
use std::sync::{Arc, Mutex, MutexGuard, PoisonError};
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
use std::time::Duration;
use std::time::Instant;

/// Why a [`Context`] is done. The texts are Go's `context.Canceled`
/// and `context.DeadlineExceeded`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum ContextError {
    /// The context was canceled through its [`CancelHandle`].
    Canceled,
    /// The context's deadline passed.
    DeadlineExceeded,
}

impl fmt::Display for ContextError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            ContextError::Canceled => "context canceled",
            ContextError::DeadlineExceeded => "context deadline exceeded",
        })
    }
}

impl std::error::Error for ContextError {}

/// Carries cancellation, a deadline and values across API boundaries.
///
/// Cheap to clone; clones share state. Derived contexts are built with
/// the `with_*` methods and never affect their parent. The default is
/// [`Context::background`].
#[derive(Clone, Default)]
pub struct Context(Option<Arc<Node>>);

struct Node {
    parent: Context,
    kind: Kind,
}

enum Kind {
    Cancel {
        state: Arc<CancelState>,
        deadline: Option<Instant>,
    },
    Value(Box<dyn Any + Send + Sync>),
}

type Waker = Arc<dyn Fn() + Send + Sync>;

#[derive(Default)]
struct CancelState(Mutex<CancelInner>);

#[derive(Default)]
struct CancelInner {
    err: Option<ContextError>,
    wakers: Vec<(u64, Waker)>,
    next_waker: u64,
}

impl CancelState {
    fn lock(&self) -> MutexGuard<'_, CancelInner> {
        // Every update completes before any user code runs.
        self.0.lock().unwrap_or_else(PoisonError::into_inner)
    }

    fn err(&self) -> Option<ContextError> {
        self.lock().err
    }

    fn finish(&self, err: ContextError) {
        let wakers = {
            let mut inner = self.lock();
            if inner.err.is_some() {
                return;
            }
            inner.err = Some(err);
            std::mem::take(&mut inner.wakers)
        };
        // Outside the lock: a waker may take the waiter's own lock.
        for (_, w) in wakers {
            w();
        }
    }
}

/// Cancels the [`Context`] it was returned with, and every context
/// derived from it. The analogue of Go's `CancelFunc`.
///
/// Dropping the handle does not cancel.
#[derive(Clone)]
pub struct CancelHandle {
    state: Arc<CancelState>,
    deadline: Option<Instant>,
}

impl CancelHandle {
    /// Cancels the context. Idempotent. A context whose deadline has
    /// already passed stays [`ContextError::DeadlineExceeded`], as in
    /// Go, where the deadline timer fires first.
    pub fn cancel(&self) {
        let err = if deadline_passed(self.deadline) {
            ContextError::DeadlineExceeded
        } else {
            ContextError::Canceled
        };
        self.state.finish(err);
    }
}

impl fmt::Debug for CancelHandle {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("CancelHandle")
            .field("err", &self.state.err())
            .finish_non_exhaustive()
    }
}

impl Context {
    /// The empty context: never canceled, no deadline, no values. Go's
    /// `context.Background()`.
    pub fn background() -> Self {
        Context(None)
    }

    /// A child that is canceled when the returned handle is, or when
    /// this context is. Go's `context.WithCancel`.
    pub fn with_cancel(&self) -> (Context, CancelHandle) {
        self.cancel_node(None)
    }

    /// A child that is done at `deadline`, when the handle cancels it,
    /// or when this context is. Go's `context.WithDeadline`.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    pub fn with_deadline(&self, deadline: Instant) -> (Context, CancelHandle) {
        self.cancel_node(Some(deadline))
    }

    /// [`Context::with_deadline`] at `timeout` from now. Go's
    /// `context.WithTimeout`.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    pub fn with_timeout(&self, timeout: Duration) -> (Context, CancelHandle) {
        self.with_deadline(now() + timeout)
    }

    /// A child carrying `value`, found by [`Context::value`] with the
    /// same type. Go's `context.WithValue`.
    pub fn with_value<T: Any + Send + Sync>(&self, value: T) -> Context {
        self.child(Kind::Value(Box::new(value)))
    }

    /// The nearest value of type `T` on this context or its ancestors.
    pub fn value<T: Any + Send + Sync>(&self) -> Option<&T> {
        self.nodes().find_map(|n| match &n.kind {
            Kind::Value(v) => (**v).downcast_ref::<T>(),
            Kind::Cancel { .. } => None,
        })
    }

    /// `None` while the context is live; otherwise why it is done. Go's
    /// `ctx.Err()`.
    pub fn err(&self) -> Option<ContextError> {
        self.nodes().find_map(|n| match &n.kind {
            Kind::Cancel { state, deadline } => state
                .err()
                .or_else(|| deadline_passed(*deadline).then_some(ContextError::DeadlineExceeded)),
            Kind::Value(_) => None,
        })
    }

    /// The earliest deadline on this context or its ancestors. Go's
    /// `ctx.Deadline()`.
    pub fn deadline(&self) -> Option<Instant> {
        self.nodes()
            .filter_map(|n| match &n.kind {
                Kind::Cancel { deadline, .. } => *deadline,
                Kind::Value(_) => None,
            })
            .min()
    }

    /// Calls `wake` once if any cancelable ancestor is canceled while
    /// the returned guard lives. Deadlines do not wake; a waiter bounds
    /// its own wait by [`Context::deadline`].
    pub(crate) fn on_cancel(&self, wake: Waker) -> CancelWatch {
        let mut regs = Vec::new();
        for n in self.nodes() {
            if let Kind::Cancel { state, .. } = &n.kind {
                let mut inner = state.lock();
                if inner.err.is_some() {
                    continue;
                }
                let id = inner.next_waker;
                inner.next_waker += 1;
                inner.wakers.push((id, Arc::clone(&wake)));
                regs.push((Arc::clone(state), id));
            }
        }
        CancelWatch(regs)
    }

    fn cancel_node(&self, deadline: Option<Instant>) -> (Context, CancelHandle) {
        let state = Arc::new(CancelState::default());
        let ctx = self.child(Kind::Cancel {
            state: Arc::clone(&state),
            deadline,
        });
        (ctx, CancelHandle { state, deadline })
    }

    fn child(&self, kind: Kind) -> Context {
        Context(Some(Arc::new(Node {
            parent: self.clone(),
            kind,
        })))
    }

    fn nodes(&self) -> impl Iterator<Item = &Node> {
        std::iter::successors(self.0.as_deref(), |n| n.parent.0.as_deref())
    }
}

impl fmt::Debug for Context {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Context")
            .field("err", &self.err())
            .field("deadline", &self.deadline())
            .finish_non_exhaustive()
    }
}

/// Unregisters the wake-ups added by [`Context::on_cancel`] on drop.
pub(crate) struct CancelWatch(Vec<(Arc<CancelState>, u64)>);

impl Drop for CancelWatch {
    fn drop(&mut self) {
        for (state, id) in &self.0 {
            state.lock().wakers.retain(|(w, _)| w != id);
        }
    }
}

/// The current instant. The bus's only clock read; not compiled for
/// wasm32-unknown-unknown, where it would panic.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[allow(clippy::disallowed_methods)]
pub(crate) fn now() -> Instant {
    Instant::now()
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn deadline_passed(deadline: Option<Instant>) -> bool {
    deadline.is_some_and(|d| now() >= d)
}

// No deadline can be set on this target, so none can pass.
#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
fn deadline_passed(_: Option<Instant>) -> bool {
    false
}
