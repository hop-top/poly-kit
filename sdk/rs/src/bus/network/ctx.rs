//! Waiting on a bus [`Context`] from async code.

use std::sync::Arc;

use tokio::sync::Notify;

use crate::bus::context::{Context, ContextError};

/// Resolves when `ctx` is done (canceled or past its deadline), with
/// why. Never resolves for a context that can never be done.
pub(crate) async fn done(ctx: &Context) -> ContextError {
    if let Some(e) = ctx.err() {
        return e;
    }
    let notify = Arc::new(Notify::new());
    let n = Arc::clone(&notify);
    let _watch = ctx.on_cancel(Arc::new(move || n.notify_one()));
    loop {
        if let Some(e) = ctx.err() {
            return e;
        }
        match ctx.deadline() {
            Some(d) => {
                tokio::select! {
                    () = notify.notified() => {}
                    () = tokio::time::sleep_until(tokio::time::Instant::from_std(d)) => {}
                }
            }
            None => notify.notified().await,
        }
    }
}
