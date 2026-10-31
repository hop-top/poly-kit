//! Named remotes and the transport seam. Go's remote.go and
//! transport.go.

use std::collections::BTreeMap;
use std::fmt;
use std::sync::{Arc, Mutex, MutexGuard};

use crate::bus::Context;

use super::clock::Timestamp;
use super::diff::Diff;
use super::transport::TransportError;

/// The direction of replication for a remote. Go's `SyncMode`; the
/// integers are the wire contract and `Bidirectional` is the default.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Hash)]
pub enum SyncMode {
    /// Push and pull: full peers. Go's `Bidirectional`, 0.
    #[default]
    Bidirectional = 0,
    /// Send diffs, never pull: a backup. Go's `PushOnly`, 1.
    PushOnly = 1,
    /// Receive diffs, never push: a read replica. Go's `PullOnly`, 2.
    PullOnly = 2,
}

impl From<SyncMode> for i32 {
    fn from(m: SyncMode) -> i32 {
        m as i32
    }
}

/// The push, pull and ping interface to a remote. Go's `Transport`.
///
/// Blocking: the replicator calls it from its own threads, or from the
/// caller of [`Replicator::sync_once`](super::Replicator::sync_once).
pub trait Transport: Send + Sync {
    /// Sends `diffs` to the remote.
    fn push(&self, ctx: &Context, diffs: &[Diff]) -> Result<(), TransportError>;

    /// Fetches the diffs stamped after `since`.
    fn pull(&self, ctx: &Context, since: &Timestamp) -> Result<Vec<Diff>, TransportError>;

    /// Checks the remote is reachable.
    fn ping(&self, ctx: &Context) -> Result<(), TransportError>;
}

/// Decides whether a diff is sent to a remote. Go's `Remote.Filter`.
pub type Filter = Arc<dyn Fn(&Diff) -> bool + Send + Sync>;

/// A named replication endpoint with a mode and an optional filter. Go's
/// `Remote`.
#[derive(Clone)]
pub struct Remote {
    /// Unique within a replicator.
    pub name: String,
    /// How diffs reach it and come back.
    pub transport: Arc<dyn Transport>,
    /// The direction of replication.
    pub mode: SyncMode,
    /// When set, only diffs it accepts are pushed. It does not filter
    /// what is pulled.
    pub filter: Option<Filter>,
}

impl Remote {
    /// A bidirectional, unfiltered remote.
    pub fn new(name: impl Into<String>, transport: Arc<dyn Transport>) -> Self {
        Remote {
            name: name.into(),
            transport,
            mode: SyncMode::default(),
            filter: None,
        }
    }

    /// Sets the sync mode.
    pub fn mode(mut self, mode: SyncMode) -> Self {
        self.mode = mode;
        self
    }

    /// Sets the push filter.
    pub fn filter(mut self, f: impl Fn(&Diff) -> bool + Send + Sync + 'static) -> Self {
        self.filter = Some(Arc::new(f));
        self
    }
}

impl fmt::Debug for Remote {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Remote")
            .field("name", &self.name)
            .field("mode", &self.mode)
            .field("filter", &self.filter.is_some())
            .finish_non_exhaustive()
    }
}

/// Why a replicator or remote-set call failed.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum SyncError {
    /// A remote of that name is already registered.
    RemoteExists(String),
    /// No remote of that name is registered.
    RemoteNotFound(String),
    /// The replicator has not been started.
    NotStarted,
}

impl fmt::Display for SyncError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            SyncError::RemoteExists(n) => write!(f, "sync: remote already exists: {n}"),
            SyncError::RemoteNotFound(n) => write!(f, "sync: remote not found: {n}"),
            SyncError::NotStarted => f.write_str("sync: replicator not started"),
        }
    }
}

impl std::error::Error for SyncError {}

/// A thread-safe collection of named remotes. Go's `RemoteSet`.
#[derive(Default)]
pub struct RemoteSet {
    remotes: Mutex<BTreeMap<String, Arc<Remote>>>,
}

impl RemoteSet {
    /// An empty set.
    pub fn new() -> Self {
        Self::default()
    }

    fn guard(&self) -> MutexGuard<'_, BTreeMap<String, Arc<Remote>>> {
        self.remotes.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Registers a remote; [`SyncError::RemoteExists`] if the name is taken.
    pub fn add(&self, r: Remote) -> Result<(), SyncError> {
        let mut m = self.guard();
        if m.contains_key(&r.name) {
            return Err(SyncError::RemoteExists(r.name));
        }
        m.insert(r.name.clone(), Arc::new(r));
        Ok(())
    }

    /// Deletes a remote; [`SyncError::RemoteNotFound`] if absent.
    pub fn remove(&self, name: &str) -> Result<(), SyncError> {
        self.guard()
            .remove(name)
            .map(|_| ())
            .ok_or_else(|| SyncError::RemoteNotFound(name.to_string()))
    }

    /// A copy of the named remote.
    pub fn get(&self, name: &str) -> Option<Remote> {
        self.guard().get(name).map(|r| (**r).clone())
    }

    /// The registered handle itself: identity survives a remove and re-add
    /// of the same name, which the replicator's loops rely on.
    pub(crate) fn handle(&self, name: &str) -> Option<Arc<Remote>> {
        self.guard().get(name).cloned()
    }

    /// Copies of every remote, ordered by name.
    pub fn list(&self) -> Vec<Remote> {
        self.guard().values().map(|r| (**r).clone()).collect()
    }

    /// How many remotes are registered.
    pub fn len(&self) -> usize {
        self.guard().len()
    }

    /// Whether none are.
    pub fn is_empty(&self) -> bool {
        self.guard().is_empty()
    }
}
