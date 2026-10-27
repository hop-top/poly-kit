//! Helpers shared by the `domain_statemachine*.rs` tests.
//!
//! - [`within`]: a watchdog, so a call that blocks forever fails the
//!   test instead of hanging the run.
//! - [`Recording`]: Go's `recordingPublisher`, safe for concurrent use
//!   because the post-transition event is published off the caller's
//!   thread.
//! - [`Manual`]: a [`Spawn`] that queues tasks until the test runs them.

#![allow(dead_code)]

use std::collections::VecDeque;
use std::sync::{mpsc, Arc, Mutex};
use std::task::{Context as TaskCx, Waker};
use std::thread;
use std::time::{Duration, Instant};

use hop_top_kit::bus::{Context, Spawn, Task};
use hop_top_kit::domain::{EventPublisher, Payload, PublishError, Rules, State};

pub const WATCHDOG: Duration = Duration::from_secs(10);

/// Go's 2 s wait for the fire-and-forget post-transition event.
pub const AWAIT: Duration = Duration::from_secs(2);

/// Runs `f` on a thread of its own and fails the test if it does not
/// return within [`WATCHDOG`].
pub fn within<T: Send + 'static>(f: impl FnOnce() -> T + Send + 'static) -> T {
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(f());
    });
    match rx.recv_timeout(WATCHDOG) {
        Ok(v) => v,
        Err(e) => panic!("watchdog: call did not return within {WATCHDOG:?} ({e})"),
    }
}

/// Builds a rules map from string literals.
pub fn rules(pairs: &[(&str, &[&str])]) -> Rules {
    pairs
        .iter()
        .map(|(from, to)| {
            (
                State::from(*from),
                to.iter().map(|s| State::from(*s)).collect(),
            )
        })
        .collect()
}

pub fn states(names: &[&str]) -> Vec<State> {
    names.iter().map(|s| State::from(*s)).collect()
}

/// One observed publish.
#[derive(Clone, Debug)]
pub struct Recorded {
    pub topic: String,
    pub source: String,
    pub payload: Payload,
}

/// Records every publish; never vetoes.
#[derive(Default)]
pub struct Recording {
    events: Mutex<Vec<Recorded>>,
}

impl Recording {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }

    pub fn events(&self) -> Vec<Recorded> {
        self.events.lock().unwrap().clone()
    }

    pub fn topics(&self) -> Vec<String> {
        self.events().into_iter().map(|e| e.topic).collect()
    }

    /// Go's `awaitTopic`: polls until `want` is recorded or [`AWAIT`]
    /// elapses.
    pub fn await_topic(&self, want: &str) {
        let deadline = Instant::now() + AWAIT;
        while Instant::now() < deadline {
            if self.topics().iter().any(|t| t == want) {
                return;
            }
            thread::sleep(Duration::from_millis(5));
        }
        panic!(
            "timed out waiting for topic {want:?}; saw {:?}",
            self.topics()
        );
    }
}

impl EventPublisher for Recording {
    fn publish(
        &self,
        _ctx: &Context,
        topic: &str,
        source: &str,
        payload: &Payload,
    ) -> Result<(), PublishError> {
        self.events.lock().unwrap().push(Recorded {
            topic: topic.to_string(),
            source: source.to_string(),
            payload: payload.clone(),
        });
        Ok(())
    }
}

/// Queues tasks until the test runs them, in spawn order.
#[derive(Clone, Default)]
pub struct Manual(Arc<Mutex<VecDeque<Task>>>);

impl Manual {
    pub fn len(&self) -> usize {
        self.0.lock().unwrap().len()
    }

    /// Polls every queued task to completion on this thread.
    pub fn run_all(&self) {
        loop {
            let next = self.0.lock().unwrap().pop_front();
            let Some(mut task) = next else { return };
            let mut cx = TaskCx::from_waker(Waker::noop());
            while task.as_mut().poll(&mut cx).is_pending() {
                thread::yield_now();
            }
        }
    }
}

impl Spawn for Manual {
    fn spawn(&self, task: Task) {
        self.0.lock().unwrap().push_back(task);
    }
}
