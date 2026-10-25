//! Request-scoped [`Context`]: cancellation, a deadline and typed values.
//!
//! The Rust form of the `context.Context` that Go threads through
//! `Publish`, every handler and `Close`. Publish passes it to the
//! handlers without reading it, as Go does, and [`SharedBus::close_ctx`]
//! bounds its async drain by it. Go's network adapter carries a peer's origin as a
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
//! - Go's cancellation is pushed down the tree as it happens; here it is
//!   worked out on demand. Each cause records when it happened (a
//!   cancel, the instant of the call; a deadline, the deadline itself)
//!   and [`Context::err`] reports the earliest along the chain, which is
//!   the error Go would have propagated first.
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

/// Why and when a node became done. The instant is `None` only on
/// wasm32-unknown-unknown, which has no clock and no deadlines, so every
/// cause there is a cancel and the order between them does not matter.
type Cause = (ContextError, Option<Instant>);

#[derive(Default)]
struct CancelState(Mutex<CancelInner>);

#[derive(Default)]
struct CancelInner {
    err: Option<Cause>,
    wakers: Vec<(u64, Waker)>,
    next_waker: u64,
}

impl CancelState {
    fn lock(&self) -> MutexGuard<'_, CancelInner> {
        // Every update completes before any user code runs.
        self.0.lock().unwrap_or_else(PoisonError::into_inner)
    }

    fn err(&self) -> Option<Cause> {
        self.lock().err
    }

    fn finish(&self, err: Cause) {
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
    /// The context this handle cancels.
    ctx: Context,
}

impl CancelHandle {
    /// Cancels the context. Idempotent. A context already done, through
    /// its own deadline or an ancestor, keeps that error: in Go the
    /// cause got there first and this cancel finds the context done.
    pub fn cancel(&self) {
        let cause = self
            .ctx
            .cause()
            .unwrap_or((ContextError::Canceled, stamp_now()));
        self.state.finish(cause);
    }
}

impl fmt::Debug for CancelHandle {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("CancelHandle")
            .field("err", &self.state.err().map(|(e, _)| e))
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
        self.cause().map(|(e, _)| e)
    }

    /// The earliest cause along the chain. On a tie the ancestor wins:
    /// a context derived from one already done is done from the start
    /// with the ancestor's error, as in Go.
    fn cause(&self) -> Option<Cause> {
        self.nodes()
            .flat_map(|n| match &n.kind {
                Kind::Cancel { state, deadline } => {
                    let passed = deadline
                        .filter(|d| deadline_passed(Some(*d)))
                        .map(|d| (ContextError::DeadlineExceeded, Some(d)));
                    [state.err(), passed]
                }
                Kind::Value(_) => [None, None],
            })
            .flatten()
            .reduce(|best, c| if c.1 <= best.1 { c } else { best })
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
        let handle = CancelHandle {
            state,
            ctx: ctx.clone(),
        };
        (ctx, handle)
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
fn stamp_now() -> Option<Instant> {
    Some(now())
}

#[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
fn stamp_now() -> Option<Instant> {
    None
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
