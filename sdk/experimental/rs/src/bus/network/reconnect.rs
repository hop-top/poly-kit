//! Reconnecting dropped outbound links: Go's `goReconnect` and
//! `reconnectLoop`.

use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::Duration;

use super::adapter::Inner;
use super::runtime;
use crate::util::retry::{retry_async, SplitMix64};

/// The bound on one reconnect dial and handshake.
const DIAL_TIMEOUT: Duration = Duration::from_secs(10);

impl Inner {
    /// Starts a reconnect loop for `addr` unless one is running, the
    /// address is not registered, or the adapter is closed. Close waits
    /// for it.
    pub(crate) fn go_reconnect(self: &Arc<Self>, addr: &str) {
        {
            let mut st = self.state();
            if self.closed.load(Ordering::SeqCst)
                || st.reconnecting.contains(addr)
                || !st.peers.contains_key(addr)
            {
                return;
            }
            st.reconnecting.insert(addr.to_string());
            self.tasks.add();
        }
        let inner = Arc::clone(self);
        let addr = addr.to_string();
        runtime::handle().spawn(async move {
            inner.reconnect_loop(&addr).await;
            inner.state().reconnecting.remove(&addr);
            inner.tasks.done();
        });
    }

    /// Dials `addr` with backoff until connected, forgotten or closed.
    /// Each attempt is bounded by 10 s and starts from the base context,
    /// so an offline policy a connect recorded still applies.
    async fn reconnect_loop(self: &Arc<Self>, addr: &str) {
        let ctx = self.base_ctx();
        let mut rng = SplitMix64::from_entropy();
        let _ = retry_async(
            &self.reconnect,
            &mut rng,
            || {
                let inner = Arc::clone(self);
                let ctx = ctx.clone();
                let addr = addr.to_string();
                async move {
                    if inner.closed.load(Ordering::SeqCst) {
                        return Ok(());
                    }
                    let (dial_ctx, _cancel) = ctx.with_timeout(DIAL_TIMEOUT);
                    tokio::select! {
                        r = inner.connect(dial_ctx, addr, true) => r,
                        () = inner.closing.wait() => Ok(()),
                    }
                }
            },
            |delay| {
                let inner = Arc::clone(self);
                async move {
                    tokio::select! {
                        () = tokio::time::sleep(delay) => !inner.closed.load(Ordering::SeqCst),
                        () = inner.closing.wait() => false,
                    }
                }
            },
        )
        .await;
    }
}
