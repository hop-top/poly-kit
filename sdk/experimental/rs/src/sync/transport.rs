//! The in-memory transport, for tests and in-process peers. Go's
//! transport_memory.go.

use std::fmt;
use std::sync::{Mutex, MutexGuard};

use crate::bus::Context;

use super::clock::Timestamp;
use super::diff::Diff;
use super::remote::Transport;

/// Why a transport call failed. Carries the message only, so a status
/// can keep a copy of the last one.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TransportError(String);

impl TransportError {
    /// An error with this message.
    pub fn new(msg: impl Into<String>) -> Self {
        TransportError(msg.into())
    }
}

impl fmt::Display for TransportError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for TransportError {}

impl From<String> for TransportError {
    fn from(s: String) -> Self {
        TransportError(s)
    }
}

impl From<&str> for TransportError {
    fn from(s: &str) -> Self {
        TransportError(s.to_string())
    }
}

/// An in-memory [`Transport`]: pushed diffs are kept, and a pull
/// returns those stamped after `since`. Go's `MemoryTransport`.
#[derive(Debug)]
pub struct MemoryTransport {
    state: Mutex<State>,
}

#[derive(Debug)]
struct State {
    diffs: Vec<Diff>,
    alive: bool,
}

impl Default for MemoryTransport {
    fn default() -> Self {
        Self::new()
    }
}

impl MemoryTransport {
    /// An empty, reachable transport.
    pub fn new() -> Self {
        MemoryTransport {
            state: Mutex::new(State {
                diffs: Vec::new(),
                alive: true,
            }),
        }
    }

    fn guard(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Makes [`Transport::ping`] succeed or fail, for tests.
    pub fn set_alive(&self, alive: bool) {
        self.guard().alive = alive;
    }

    /// A copy of every stored diff.
    pub fn diffs(&self) -> Vec<Diff> {
        self.guard().diffs.clone()
    }
}

impl Transport for MemoryTransport {
    fn push(&self, _: &Context, diffs: &[Diff]) -> Result<(), TransportError> {
        self.guard().diffs.extend_from_slice(diffs);
        Ok(())
    }

    fn pull(&self, _: &Context, since: &Timestamp) -> Result<Vec<Diff>, TransportError> {
        Ok(self
            .guard()
            .diffs
            .iter()
            .filter(|d| since.before(&d.timestamp))
            .cloned()
            .collect())
    }

    fn ping(&self, _: &Context) -> Result<(), TransportError> {
        if self.guard().alive {
            Ok(())
        } else {
            Err(TransportError::new("transport: not reachable"))
        }
    }
}
