//! The retry and dead-letter decorator.

use std::sync::Arc;
use std::time::Duration;

use crate::bus::{BusError, Context, ContextError, Event, Sink};

use super::backoff::{exponential_backoff, BackoffFn};

/// The text of the circuit breaker's refusal (`BrokenCircuit`'s
/// `Display`), which [`is_broken_circuit`] looks for.
pub const BROKEN_CIRCUIT: &str = "breaker: circuit open";

/// Whether `err` is a breaker refusal: Go's
/// `errors.Is(err, breaker.ErrBrokenCircuit)`.
///
/// A bus [`Sink`] reports [`BusError::Handler`], a message, so the
/// refusal is recognized by its text, [`BROKEN_CIRCUIT`], anywhere in
/// the message: a sink that wraps the refusal ("webhook: post: ...")
/// still matches. A sink of your own that gates on a breaker reports it
/// with `BusError::handler(BrokenCircuit.to_string())`.
pub fn is_broken_circuit(err: &BusError) -> bool {
    matches!(err, BusError::Handler(m) if m.contains(BROKEN_CIRCUIT))
}

/// How a retry waits between attempts. Returns `Err` when `ctx` is done
/// first. The default parks the thread, waking early on cancellation.
pub type Sleeper = Arc<dyn Fn(&Context, Duration) -> Result<(), ContextError> + Send + Sync>;

/// Wraps a [`Sink`], retrying `drain` on error: Go's `RetrySink`.
///
/// Attempts are counted as Go counts them: attempt 0 is the first try,
/// with no wait before it; the wait before attempt `n` (`n >= 1`) is
/// `backoff(n - 1)`. The wait wakes early if the context is done, and
/// `drain` then returns the context's error.
///
/// On exhaustion the event goes to the dead-letter sink if one is set
/// (`drain` returns its result), else the last inner error comes back
/// unchanged. A breaker refusal ([`is_broken_circuit`]) is terminal:
/// no further attempts, straight to the dead letter or the error;
/// retrying would defeat the breaker.
pub struct RetrySink {
    inner: Box<dyn Sink>,
    max_attempts: u32,
    backoff: BackoffFn,
    dead_letter: Option<Box<dyn Sink>>,
    sleeper: Sleeper,
}

impl RetrySink {
    /// Wraps `inner`: three attempts, [`exponential_backoff`] of 100ms,
    /// factor 2, with jitter, no dead letter. Go's `NewRetrySink`.
    pub fn new(inner: impl Sink + 'static) -> Self {
        RetrySink {
            inner: Box::new(inner),
            max_attempts: 3,
            backoff: exponential_backoff(Duration::from_millis(100), 2.0, true),
            dead_letter: None,
            sleeper: default_sleeper(),
        }
    }

    /// The total attempts, the first try included: 1 is one try and no
    /// backoff. Below 1 is clamped to 1, as Go does: never silently drop
    /// the event.
    pub fn max_attempts(mut self, n: u32) -> Self {
        self.max_attempts = n.max(1);
        self
    }

    /// The wait between attempts: Go's `WithBackoff`.
    pub fn backoff(mut self, f: impl Fn(u32) -> Duration + Send + Sync + 'static) -> Self {
        self.backoff = Arc::new(f);
        self
    }

    /// Routes an event that exhausted its attempts, or hit a breaker
    /// refusal, to `dl`, with the original context. If `dl` fails, so
    /// does `drain`. `close` closes it after the inner sink; wiring one
    /// sink as both closes it twice.
    pub fn dead_letter(mut self, dl: impl Sink + 'static) -> Self {
        self.dead_letter = Some(Box::new(dl));
        self
    }

    /// Replaces how the wait between attempts happens. The default is
    /// not compiled for wasm32, where it does not wait; give one that
    /// does (a timer future cannot run here: the seam is synchronous).
    pub fn sleeper(
        mut self,
        f: impl Fn(&Context, Duration) -> Result<(), ContextError> + Send + Sync + 'static,
    ) -> Self {
        self.sleeper = Arc::new(f);
        self
    }

    fn dispatch_dead_letter(
        &self,
        ctx: &Context,
        event: &Event,
        last: BusError,
    ) -> Result<(), BusError> {
        match &self.dead_letter {
            Some(dl) => dl.drain(ctx, event),
            None => Err(last),
        }
    }
}

impl Sink for RetrySink {
    fn drain(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        let mut last = None;
        for attempt in 0..self.max_attempts {
            if attempt > 0 {
                let d = (self.backoff)(attempt - 1);
                if !d.is_zero() {
                    (self.sleeper)(ctx, d).map_err(BusError::Context)?;
                }
                if let Some(e) = ctx.err() {
                    return Err(BusError::Context(e));
                }
            }
            match self.inner.drain(ctx, event) {
                Ok(()) => return Ok(()),
                Err(e) if is_broken_circuit(&e) => {
                    return self.dispatch_dead_letter(ctx, event, e);
                }
                Err(e) => last = Some(e),
            }
        }
        match last {
            Some(e) => self.dispatch_dead_letter(ctx, event, e),
            None => Ok(()),
        }
    }

    fn close(&self) -> Result<(), BusError> {
        let first = self.inner.close();
        let second = self.dead_letter.as_ref().map_or(Ok(()), |dl| dl.close());
        first.and(second)
    }
}

#[cfg(not(target_arch = "wasm32"))]
fn default_sleeper() -> Sleeper {
    use std::sync::{Condvar, Mutex, PoisonError};

    Arc::new(|ctx, d| {
        if let Some(e) = ctx.err() {
            return Err(e);
        }
        let woken = Arc::new((Mutex::new(false), Condvar::new()));
        let w = Arc::clone(&woken);
        let _watch = ctx.on_cancel(Arc::new(move || {
            *w.0.lock().unwrap_or_else(PoisonError::into_inner) = true;
            w.1.notify_all();
        }));
        let wait = match ctx.deadline() {
            Some(dl) => d.min(dl.saturating_duration_since(crate::bus::context::now())),
            None => d,
        };
        let guard = woken.0.lock().unwrap_or_else(PoisonError::into_inner);
        drop(
            woken
                .1
                .wait_timeout_while(guard, wait, |done| !*done)
                .unwrap_or_else(PoisonError::into_inner),
        );
        ctx.err().map_or(Ok(()), Err)
    })
}

#[cfg(target_arch = "wasm32")]
fn default_sleeper() -> Sleeper {
    Arc::new(|ctx, _| ctx.err().map_or(Ok(()), Err))
}
