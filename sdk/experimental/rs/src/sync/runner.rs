//! The replicator's timer threads: one per remote. Compiled off
//! wasm32-unknown-unknown only; there a round is driven by the caller
//! with `Replicator::sync_once`.

use std::sync::{Arc, Condvar, Mutex, MutexGuard};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use serde::de::DeserializeOwned;
use serde::Serialize;

use crate::bus::Context;
use crate::domain::Entity;

use super::remote::Remote;
use super::replicator::Inner;

/// How often a waiting loop looks at its context: `Context` has no
/// blocking wait, so a cancel is seen within one slice.
const POLL: Duration = Duration::from_millis(20);

/// Stop signal and the loop threads.
#[derive(Default)]
pub(crate) struct Runner {
    done: Mutex<bool>,
    wake: Condvar,
    threads: Mutex<Vec<JoinHandle<()>>>,
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(|e| e.into_inner())
}

impl Runner {
    /// Tells every loop to exit, wakes them and waits for them.
    pub(crate) fn stop(&self) {
        *lock(&self.done) = true;
        self.wake.notify_all();
        let handles: Vec<_> = lock(&self.threads).drain(..).collect();
        for h in handles {
            let _ = h.join();
        }
    }

    /// Waits one interval. `false` when stopped or `ctx` is done.
    fn wait_tick(&self, interval: Duration, ctx: &Context) -> bool {
        let deadline = Instant::now() + interval;
        let mut done = lock(&self.done);
        loop {
            if *done || ctx.err().is_some() {
                return false;
            }
            let now = Instant::now();
            if now >= deadline {
                return true;
            }
            done = self
                .wake
                .wait_timeout(done, (deadline - now).min(POLL))
                .unwrap_or_else(|e| e.into_inner())
                .0;
        }
    }
}

impl<T> Inner<T>
where
    T: Entity + Serialize + DeserializeOwned + 'static,
{
    /// Starts the loop for `rem`: one sync round per interval, the first
    /// after one interval, until stop, `ctx` is done or the remote is
    /// removed (or replaced by another of the same name).
    pub(crate) fn spawn_loop(self: &Arc<Self>, ctx: Context, rem: Arc<Remote>) {
        let inner = Arc::clone(self);
        let name = format!("sync-{}", rem.name);
        let spawned = thread::Builder::new().name(name).spawn({
            let rem = Arc::clone(&rem);
            move || inner.run(&ctx, &rem)
        });
        match spawned {
            Ok(h) => lock(&self.runner.threads).push(h),
            Err(e) => self.logger.error(
                "sync: start loop failed",
                &[("remote", &rem.name), ("err", &e.to_string())],
            ),
        }
    }

    fn run(&self, ctx: &Context, rem: &Arc<Remote>) {
        while self.runner.wait_tick(self.interval, ctx) {
            match self.remotes.handle(&rem.name) {
                Some(current) if Arc::ptr_eq(&current, rem) => self.sync_remote(ctx, rem),
                _ => return,
            }
        }
    }
}
