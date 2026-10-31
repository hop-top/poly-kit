//! Fixtures shared by the notify tests: recording and scripted sinks.

#![allow(dead_code)]

use std::sync::{Arc, Mutex, PoisonError};

use hop_top_kit::bus::{BusError, Context, Event, Sink};
use serde_json::{json, Value};

/// A stable event: a fixed topic, source and payload.
pub fn event(topic: &str, payload: Value) -> Event {
    let mut e = Event::new_at(topic, "notify-test", payload, std::time::UNIX_EPOCH);
    e.id = String::new();
    e
}

pub fn with_severity(topic: &str, severity: &str) -> Event {
    event(topic, json!({ "severity": severity }))
}

#[derive(Default)]
pub struct State {
    pub drained: Vec<Event>,
    pub closed: usize,
}

/// Records every event; fails drains from a script and closes with
/// `close_err`. An exhausted script repeats its last entry, so a
/// one-entry script always fails and an empty one always succeeds.
#[derive(Clone, Default)]
pub struct Scripted {
    pub state: Arc<Mutex<State>>,
    script: Arc<Vec<Option<String>>>,
    close_err: Option<String>,
}

impl Scripted {
    pub fn ok() -> Self {
        Self::default()
    }

    pub fn failing(msgs: &[Option<&str>]) -> Self {
        Scripted {
            script: Arc::new(msgs.iter().map(|m| m.map(str::to_string)).collect()),
            ..Self::default()
        }
    }

    pub fn close_err(mut self, msg: &str) -> Self {
        self.close_err = Some(msg.to_string());
        self
    }

    pub fn calls(&self) -> usize {
        self.lock().drained.len()
    }

    pub fn closes(&self) -> usize {
        self.lock().closed
    }

    pub fn last(&self) -> Event {
        self.lock()
            .drained
            .last()
            .cloned()
            .expect("no event drained")
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(PoisonError::into_inner)
    }
}

impl Sink for Scripted {
    fn drain(&self, _: &Context, e: &Event) -> Result<(), BusError> {
        let mut st = self.lock();
        let idx = st.drained.len();
        st.drained.push(e.clone());
        match self.script.get(idx).or(self.script.last()) {
            Some(Some(m)) => Err(BusError::handler(m.clone())),
            _ => Ok(()),
        }
    }

    fn close(&self) -> Result<(), BusError> {
        self.lock().closed += 1;
        match &self.close_err {
            Some(m) => Err(BusError::handler(m.clone())),
            None => Ok(()),
        }
    }
}

pub mod http;

#[cfg(feature = "notify-email")]
pub mod smtp;
