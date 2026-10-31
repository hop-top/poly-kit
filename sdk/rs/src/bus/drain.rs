//! How a [`SharedBus`] waits: for a pool slot, and for in-flight work to
//! drain on unsubscribe and close.
//!
//! The policy is a value, [`Threading`], that the target only selects.
//! Native buses block on the bus condvar. wasm32-unknown-unknown has one
//! thread, so blocking would hang it: the pool is uncapped, and a drain
//! that does not hold yet returns [`Undrained::WouldBlock`], which close
//! reports as [`BusError::NotDrained`]. Both policies run on every
//! target, so native tests cover the wasm one.
//!
//! [`SharedBus`]: super::SharedBus
//! [`BusError::NotDrained`]: super::BusError::NotDrained

use std::sync::MutexGuard;
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
use std::sync::PoisonError;

use super::context::{Context, ContextError};
use super::shared::{Inner, State};

/// Whether a bus's waits may block.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum Threading {
    /// Native: waits block, and the pool is capped at `max_async`.
    Multi,
    /// One thread (wasm32-unknown-unknown): nothing blocks. The pool is
    /// uncapped and an unmet drain reports [`Undrained::WouldBlock`].
    Single,
}

impl Threading {
    /// The policy for the target this crate is built for.
    pub(super) const TARGET: Threading = if cfg!(all(target_arch = "wasm32", target_os = "unknown"))
    {
        Threading::Single
    } else {
        Threading::Multi
    };

    /// Whether a task may take a pool slot with `in_flight` tasks
    /// already counted and a pool of `max`.
    pub(super) fn admits(self, in_flight: usize, max: usize) -> bool {
        match self {
            Threading::Multi => in_flight < max,
            Threading::Single => true,
        }
    }
}

/// Why a drain wait ended before its condition held.
pub(super) enum Undrained {
    Ctx(ContextError),
    /// The wait would block the only thread.
    WouldBlock,
}

/// One step of a drain wait: `Ok` once `done`, the context's error once
/// it is done, else [`Undrained::WouldBlock`], which a [`Threading::Multi`]
/// wait turns into a block and a [`Threading::Single`] one returns.
fn check(done: bool, ctx: &Context) -> Result<(), Undrained> {
    if done {
        return Ok(());
    }
    match ctx.err() {
        Some(e) => Err(Undrained::Ctx(e)),
        None => Err(Undrained::WouldBlock),
    }
}

impl Inner {
    /// Waits until `done` holds or `ctx` is done, counted in `waiters`
    /// while blocked. A [`Threading::Single`] bus never blocks: every
    /// in-flight publish there is the caller's own, which `done`
    /// discounts, so sync waits hold; async tasks run only once the
    /// caller yields, so an unmet wait for them returns
    /// [`Undrained::WouldBlock`].
    pub(super) fn wait<'a>(
        &'a self,
        mut st: MutexGuard<'a, State>,
        ctx: &Context,
        done: impl Fn(&State) -> bool,
        waiters: fn(&mut State) -> &mut usize,
    ) -> (MutexGuard<'a, State>, Result<(), Undrained>) {
        loop {
            match check(done(&st), ctx) {
                Err(Undrained::WouldBlock) if self.threading == Threading::Multi => {}
                res => return (st, res),
            }
            *waiters(&mut st) += 1;
            st = self.block(st, ctx);
            *waiters(&mut st) -= 1;
        }
    }

    /// One condvar wait, bounded by `ctx`'s deadline.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    fn block<'a>(&'a self, st: MutexGuard<'a, State>, ctx: &Context) -> MutexGuard<'a, State> {
        match ctx.deadline() {
            Some(d) => {
                let left = d.saturating_duration_since(super::context::now());
                let woke = self.idle.wait_timeout(st, left);
                woke.unwrap_or_else(PoisonError::into_inner).0
            }
            None => self.idle.wait(st).unwrap_or_else(PoisonError::into_inner),
        }
    }

    /// Unreachable: [`Threading::TARGET`] is [`Threading::Single`] here,
    /// and a single-threaded wait returns instead of blocking.
    #[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
    fn block<'a>(&'a self, _st: MutexGuard<'a, State>, _ctx: &Context) -> MutexGuard<'a, State> {
        unreachable!("a single-threaded bus never blocks")
    }
}

#[cfg(test)]
#[path = "drain_tests.rs"]
mod tests;
