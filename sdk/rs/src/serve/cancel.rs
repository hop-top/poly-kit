//! A minimal, cloneable cancellation token.
//!
//! The obvious dependency here is `tokio_util::sync::CancellationToken`,
//! but `tokio-util` is not in this crate's tree and pulling a whole
//! utility crate in for one latch is not worth it. The latch below is
//! the whole capability the supervisor needs: set once, observable by
//! any number of waiters, and every waiter wakes at the same instant —
//! which is exactly the ordering rule the contract fixes ("cancel once
//! so every service observes cancellation at the same instant").
//!
//! `Notify::notify_waiters` alone would race a waiter that has not yet
//! parked, so the flag is checked before and after registering
//! interest, and `notified()` is pinned so the registration happens
//! before the re-check.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;

use tokio::sync::Notify;

#[derive(Debug, Default)]
struct Inner {
    flagged: AtomicBool,
    notify: Notify,
}

/// A set-once latch shared by clone. Cancelling one clone cancels every
/// clone, and every outstanding [`CancelToken::cancelled`] wakes.
#[derive(Clone, Debug, Default)]
pub struct CancelToken(Arc<Inner>);

impl CancelToken {
    /// A token that has not fired.
    pub fn new() -> Self {
        CancelToken::default()
    }

    /// Fires the token. Idempotent: a second call is a no-op, which is
    /// what lets the supervisor cancel unconditionally on every exit
    /// path without tracking whether it already did.
    pub fn cancel(&self) {
        // Release so a waiter that observes the flag with Acquire also
        // observes everything written before the cancel.
        self.0.flagged.store(true, Ordering::Release);
        self.0.notify.notify_waiters();
    }

    /// Whether the token has fired.
    pub fn is_cancelled(&self) -> bool {
        self.0.flagged.load(Ordering::Acquire)
    }

    /// Resolves once the token has fired, immediately if it already
    /// has.
    pub async fn cancelled(&self) {
        loop {
            if self.is_cancelled() {
                return;
            }
            // Register interest before re-checking the flag: a cancel
            // landing between the check above and this await would
            // otherwise be missed forever.
            let notified = self.0.notify.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            if self.is_cancelled() {
                return;
            }
            notified.await;
        }
    }

    /// A child token that fires when this one does, and can also be
    /// fired on its own without affecting the parent.
    ///
    /// Used for the per-stop budget: the supervisor abandons a stop by
    /// firing the child, leaving the run token untouched.
    pub fn child(&self) -> CancelToken {
        let child = CancelToken::new();
        let parent = self.clone();
        let handle = child.clone();
        tokio::spawn(async move {
            parent.cancelled().await;
            handle.cancel();
        });
        child
    }
}
