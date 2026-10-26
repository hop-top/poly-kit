//! Reconnecting dropped outbound links: Go's `goReconnect` and
//! `reconnectLoop`.

use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

use super::adapter::Inner;
use super::runtime;
use crate::util::retry::SplitMix64;

/// The bound on one reconnect dial and handshake.
const DIAL_TIMEOUT: Duration = Duration::from_secs(10);

impl Inner {
    /// Starts a reconnect loop for `addr` that dials at once: Go's
    /// `goReconnect`. Close waits for it.
    pub(crate) fn go_reconnect(self: &Arc<Self>, addr: &str) {
        self.start_reconnect(addr, false);
    }

    /// Starts a reconnect loop for an address whose link went down: Go's
    /// `goReconnectAfterDrop`. It never dials at once: it first waits the
    /// backoff for the address's run of short-lived links.
    pub(crate) fn go_reconnect_after_drop(self: &Arc<Self>, addr: &str) {
        self.start_reconnect(addr, true);
    }

    /// The stability window: a link that stayed up this long resets its
    /// address's backoff. It is the maximum backoff, so a link counts as
    /// healthy once it outlived the longest wait the policy would impose,
    /// and the window scales with the configured cap. Go's `stableAfter`.
    pub(crate) fn stable_after(&self) -> Duration {
        self.reconnect.max()
    }

    /// Starts a loop unless one is running, the address is not
    /// registered, or the adapter is closed.
    fn start_reconnect(self: &Arc<Self>, addr: &str, wait: bool) {
        let first = {
            let mut st = self.state();
            if self.closed.load(Ordering::SeqCst)
                || st.reconnecting.contains(addr)
                || !st.peers.contains_key(addr)
            {
                return;
            }
            st.reconnecting.insert(addr.to_string());
            self.tasks.add();
            wait.then(|| st.peers.get(addr).map_or(0, |p| p.flaps))
        };
        let inner = Arc::clone(self);
        let addr = addr.to_string();
        runtime::handle().spawn(async move {
            inner.reconnect_loop(&addr, first).await;
            inner.state().reconnecting.remove(&addr);
            inner.tasks.done();
        });
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
            if wait {
                let delay = self.reconnect.backoff_with(attempt, &mut rng);
                tokio::select! {
                    () = tokio::time::sleep(delay) => {}
                    () = self.closing.wait() => return,
                }
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
}
