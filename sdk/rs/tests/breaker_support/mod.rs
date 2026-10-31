//! Helpers shared by the `breaker*.rs` tests.
//!
//! - [`FakeClock`]: a manually advanced [`Clock`].
//! - [`Logs`]: a [`Logger`] keeping every record.
//! - [`Events`] + [`Inline`]: a publisher keeping every event, and a
//!   [`Spawn`] running each publish before `spawn` returns, so tests
//!   see events without waiting.

#![allow(dead_code)]

use std::sync::{Arc, Mutex};
use std::task::{Context as TaskCx, Waker};
use std::time::{Duration, SystemTime};

use hop_top_kit::breaker::{Clock, Logger, Record};
use hop_top_kit::bus::{Context, Spawn, Task};
use hop_top_kit::domain::{EventPublisher, Payload, PublishError};

/// The fake clock's start: 2026-01-01T00:00:00Z.
pub fn epoch() -> SystemTime {
    SystemTime::UNIX_EPOCH + Duration::from_secs(1_767_225_600)
}

pub struct FakeClock(Mutex<SystemTime>);

impl FakeClock {
    pub fn new() -> Arc<Self> {
        Arc::new(FakeClock(Mutex::new(epoch())))
    }

    pub fn advance(&self, d: Duration) {
        *self.0.lock().unwrap() += d;
    }
}

impl Clock for FakeClock {
    fn now(&self) -> SystemTime {
        *self.0.lock().unwrap()
    }
}

#[derive(Default)]
pub struct Logs(Mutex<Vec<Record>>);

impl Logs {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }

    pub fn records(&self) -> Vec<Record> {
        self.0.lock().unwrap().clone()
    }

    pub fn messages(&self) -> Vec<&'static str> {
        self.records().iter().map(|r| r.message).collect()
    }
}

impl Logger for Logs {
    fn log(&self, record: &Record) {
        self.0.lock().unwrap().push(record.clone());
    }
}

#[derive(Default)]
pub struct Events(Mutex<Vec<(String, String, Payload)>>);

impl Events {
    pub fn new() -> Arc<Self> {
        Arc::new(Self::default())
    }

    /// `(topic, source, payload)` in publish order.
    pub fn all(&self) -> Vec<(String, String, Payload)> {
        self.0.lock().unwrap().clone()
    }
}

impl EventPublisher for Events {
    fn publish(
        &self,
        _: &Context,
        topic: &str,
        source: &str,
        payload: &Payload,
    ) -> Result<(), PublishError> {
        self.0
            .lock()
            .unwrap()
            .push((topic.into(), source.into(), payload.clone()));
        Ok(())
    }
}

/// Polls each task once; the breaker's publish tasks never await.
pub struct Inline;

impl Spawn for Inline {
    fn spawn(&self, mut task: Task) {
        let mut cx = TaskCx::from_waker(Waker::noop());
        assert!(
            task.as_mut().poll(&mut cx).is_ready(),
            "publish task pending"
        );
    }
}
