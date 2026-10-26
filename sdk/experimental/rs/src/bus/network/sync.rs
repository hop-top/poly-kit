//! Waiting primitives usable from both sides of the runtime boundary: a
//! task awaits them, a caller's thread blocks on them.

use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Condvar, Mutex, MutexGuard, PoisonError};

use tokio::sync::Notify;

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    // Every critical section here is a counter or flag update.
    m.lock().unwrap_or_else(PoisonError::into_inner)
}

/// A one-shot flag: Go's closed `done` channel.
#[derive(Default)]
pub(crate) struct Signal {
    fired: AtomicBool,
    notify: Notify,
    gate: Mutex<bool>,
    cv: Condvar,
}

impl Signal {
    /// Fires the signal; returns whether this call fired it.
    pub(crate) fn fire(&self) -> bool {
        if self.fired.swap(true, Ordering::SeqCst) {
            return false;
        }
        self.notify.notify_waiters();
        *lock(&self.gate) = true;
        self.cv.notify_all();
        true
    }

    pub(crate) fn is_fired(&self) -> bool {
        self.fired.load(Ordering::SeqCst)
    }

    /// Resolves once the signal fires.
    pub(crate) async fn wait(&self) {
        loop {
            let notified = self.notify.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            if self.is_fired() {
                return;
            }
            notified.await;
        }
    }

    /// Blocks the calling thread until the signal fires.
    pub(crate) fn wait_blocking(&self) {
        let mut fired = lock(&self.gate);
        while !*fired {
            fired = self.cv.wait(fired).unwrap_or_else(PoisonError::into_inner);
        }
    }
}

/// Go's `sync.WaitGroup` over the adapter's tasks: readers, reconnect
/// loops and listeners.
#[derive(Default)]
pub(crate) struct WaitGroup {
    count: Mutex<usize>,
    cv: Condvar,
    notify: Notify,
}

impl WaitGroup {
    pub(crate) fn add(&self) {
        *lock(&self.count) += 1;
    }

    pub(crate) fn done(&self) {
        let mut n = lock(&self.count);
        *n = n.saturating_sub(1);
        if *n == 0 {
            self.cv.notify_all();
            self.notify.notify_waiters();
        }
    }

    /// How many tasks are running.
    #[cfg(test)]
    pub(crate) fn count(&self) -> usize {
        *lock(&self.count)
    }

    /// Blocks the calling thread until every task is done.
    pub(crate) fn wait_blocking(&self) {
        let mut n = lock(&self.count);
        while *n > 0 {
            n = self.cv.wait(n).unwrap_or_else(PoisonError::into_inner);
        }
    }

    /// Resolves once every task is done.
    pub(crate) async fn wait(&self) {
        loop {
            let notified = self.notify.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            if *lock(&self.count) == 0 {
                return;
            }
            notified.await;
        }
    }
}
