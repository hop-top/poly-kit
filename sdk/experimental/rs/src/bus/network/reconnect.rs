//! Reconnecting dropped outbound links: Go's `goReconnect` and
//! `reconnectLoop`.

use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

use super::adapter::{Inner, State};
use super::runtime;
use crate::util::retry::SplitMix64;

/// Test seam: holds a reconnect loop at its exit until released.
#[cfg(test)]
pub(crate) struct ExitGate {
    pub(crate) entered: std::sync::mpsc::Sender<()>,
    pub(crate) release: Arc<tokio::sync::Semaphore>,
}

/// Test seam for the backoff timer, called with each wait's attempt:
/// `true` lets the wait elapse at once, `false` holds it until it is
/// woken or the adapter closes.
#[cfg(test)]
pub(crate) type BackoffWait = Arc<dyn Fn(u32) -> bool + Send + Sync>;

/// The bound on one reconnect dial and handshake.
const DIAL_TIMEOUT: Duration = Duration::from_secs(10);

impl Inner {
    /// Starts a reconnect loop for `addr` that dials at once: Go's
    /// `goReconnect`. Close waits for it.
    pub(crate) fn go_reconnect(self: &Arc<Self>, addr: &str) {
        self.request_redial(addr, false);
    }

    /// Starts a reconnect loop for an address whose link went down: Go's
    /// `goReconnectAfterDrop`. It never dials at once: it first waits the
    /// backoff for the address's run of short-lived links.
    pub(crate) fn go_reconnect_after_drop(self: &Arc<Self>, addr: &str) {
        self.request_redial(addr, true);
    }

    /// The stability window: a link that stayed up this long resets its
    /// address's backoff. It is the maximum backoff, so a link counts as
    /// healthy once it outlived the longest wait the policy would impose,
    /// and the window scales with the configured cap. Go's `stableAfter`.
    pub(crate) fn stable_after(&self) -> Duration {
        self.reconnect.max()
    }

    /// The one entry to an address's redial state machine: Go's
    /// `requestRedial`. `dial`, `with_network`'s retry after a failed
    /// first connect, and the redial after a link drop all go through it;
    /// `wait` says whether the run waits the backoff before it dials (a
    /// drop) or dials now (a dial request).
    ///
    /// Per registered address, under the state lock:
    ///
    /// | state | event | next |
    /// |---|---|---|
    /// | idle / up | request | running (loop started; waits if `wait`) |
    /// | running | request | running + mark (`wait` = AND of the requests); a dial request (`wait` false) also wakes the wait |
    /// | backoff-wait | wake (dial, connect, disconnect) | wait ends; exit check (mark: dial now, attempts from 0; forgotten: loop gone) |
    /// | backoff-wait | request(wait) (a drop) | no wake: the full backoff stands |
    /// | running | dial fails | running (attempt + 1, waits) |
    /// | running | dial succeeds / gives up | exit check |
    /// | exit check | mark, address kept, open | running (mark consumed; waits as marked) |
    /// | exit check | no mark, or forgotten / closed | idle / up (loop gone) |
    /// | up | link drops | request(wait); `flaps` + 1, or reset after [`stable_after`](Self::stable_after) |
    /// | any | disconnect | address forgotten, mark cleared, wait woken; the loop exits at its check |
    /// | any | connect | dials inline; a running loop gets mark(now) and a wake |
    /// | any | close | closed, marks cleared; loops stop, nothing dials |
    ///
    /// An explicit connect dials inline and owns no loop: a link it makes
    /// joins the table at up.
    ///
    /// A request that finds a loop running never just returns: the loop
    /// consumes its mark under this lock before it exits, so every
    /// request is served by the running loop or starts one.
    pub(crate) fn request_redial(self: &Arc<Self>, addr: &str, wait: bool) {
        let first = {
            let mut st = self.state();
            if self.closed.load(Ordering::SeqCst) || !st.peers.contains_key(addr) {
                return;
            }
            if st.reconnecting.contains(addr) {
                self.mark_locked(&mut st, addr, wait);
                return;
            }
            st.reconnecting.insert(addr.to_string());
            st.wakes
                .insert(addr.to_string(), Arc::new(tokio::sync::Notify::new()));
            self.tasks.add();
            wait.then(|| st.peers.get(addr).map_or(0, |p| p.flaps))
        };
        let inner = Arc::clone(self);
        let addr = addr.to_string();
        runtime::handle().spawn(async move {
            let mut from = first;
            loop {
                inner.reconnect_loop(&addr, from).await;
                inner.hold_at_exit().await;
                // Under the lock request_redial takes: a request either left
                // its mark before this point or starts a loop of its own
                // after it.
                let mut st = inner.state();
                let mark = st.pending_redial.remove(&addr);
                match (mark, st.peers.get(&addr)) {
                    (Some(wait), Some(p)) if !inner.closed.load(Ordering::SeqCst) => {
                        from = wait.then_some(p.flaps);
                        // The mark carried any wake: a fresh signal drops it.
                        st.wakes
                            .insert(addr.clone(), Arc::new(tokio::sync::Notify::new()));
                    }
                    _ => {
                        st.reconnecting.remove(&addr);
                        st.wakes.remove(&addr);
                        break;
                    }
                }
            }
            inner.tasks.done();
        });
    }

    /// Hands a request to `addr`'s running loop: the mark (`wait` is the
    /// AND of the pending requests) and, for a user request, a wake that
    /// ends the loop's backoff wait. A drop (`wait`) never wakes: it
    /// honors the full backoff. Go's `markLocked`.
    fn mark_locked(&self, st: &mut State, addr: &str, wait: bool) {
        let wait = wait && st.pending_redial.get(addr).copied().unwrap_or(true);
        st.pending_redial.insert(addr.to_string(), wait);
        if !wait {
            self.wake_locked(st, addr);
        }
    }

    /// Ends `addr`'s loop's backoff wait, if a loop runs. Go's
    /// `wakeLocked`.
    pub(crate) fn wake_locked(&self, st: &State, addr: &str) {
        if let Some(w) = st.wakes.get(addr) {
            w.notify_one();
            #[cfg(test)]
            self.wakes_sent.fetch_add(1, Ordering::SeqCst);
        }
    }

    /// An explicit connect: a loop backing off for `addr` dials now too,
    /// and its dial finds this link if this one wins.
    pub(crate) fn preempt_backoff(&self, addr: &str) {
        let mut st = self.state();
        if st.reconnecting.contains(addr)
            && st.peers.contains_key(addr)
            && !self.closed.load(Ordering::SeqCst)
        {
            self.mark_locked(&mut st, addr, false);
        }
    }

    /// Dials `addr` until connected, forgotten, closed, or out of
    /// attempts, waiting the backoff between attempts. With `wait_from`
    /// it also waits before the first dial, at that attempt, so backoff
    /// persists across short-lived links. Each attempt is bounded by 10 s
    /// and starts from the base context, so an offline policy a connect
    /// recorded still applies. Go's `reconnectLoop`.
    async fn reconnect_loop(self: &Arc<Self>, addr: &str, wait_from: Option<u32>) {
        let ctx = self.base_ctx();
        let mut rng = SplitMix64::from_entropy();
        let mut wait = wait_from.is_some();
        let mut attempt = wait_from.unwrap_or(0);
        for tries in 1u32.. {
            if wait && !self.wait_backoff(addr, attempt, &mut rng).await {
                return;
            }
            wait = true;
            if self.closed.load(Ordering::SeqCst) {
                return;
            }
            let (dial_ctx, _cancel) = ctx.with_timeout(DIAL_TIMEOUT);
            let done = tokio::select! {
                r = self.connect(dial_ctx, addr.to_string(), true) => r.is_ok(),
                () = self.closing.wait() => true,
            };
            let max = self.reconnect.max_attempts;
            if done || (max > 0 && tries >= max) {
                return;
            }
            attempt = attempt.saturating_add(1);
        }
    }

    /// Waits the backoff for `attempt`; false once the adapter closes.
    /// Waits the backoff for `attempt`. Returns false, ending the run,
    /// when the adapter closes or the loop is woken (dial, connect,
    /// disconnect); the exit check then serves the mark or sees the
    /// address gone.
    async fn wait_backoff(&self, addr: &str, attempt: u32, rng: &mut SplitMix64) -> bool {
        let wake = self.state().wakes.get(addr).cloned();
        let woken = async {
            match wake {
                Some(w) => w.notified().await,
                None => std::future::pending().await,
            }
        };
        #[cfg(test)]
        {
            let seam = self
                .backoff_wait
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .clone();
            if let Some(f) = seam {
                if f(attempt) {
                    tokio::task::yield_now().await;
                    return !self.closed.load(Ordering::SeqCst);
                }
                // Held until woken or closed.
                tokio::select! {
                    () = woken => {}
                    () = self.closing.wait() => {}
                }
                return false;
            }
        }
        let delay = self.reconnect.backoff_with(attempt, rng);
        tokio::select! {
            () = tokio::time::sleep(delay) => true,
            () = self.closing.wait() => false,
            () = woken => false,
        }
    }

    /// The exit-gate seam: a no-op unless a test armed it; compiled out
    /// of non-test builds.
    async fn hold_at_exit(&self) {
        #[cfg(test)]
        {
            let gate = self
                .exit_gate
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .take();
            if let Some(g) = gate {
                let _ = g.entered.send(());
                let _ = g.release.acquire().await;
            }
        }
    }
}
