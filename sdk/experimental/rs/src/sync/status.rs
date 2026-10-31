//! Remote health. Go's status.go.

use std::time::{Duration, SystemTime};

use serde::de::DeserializeOwned;
use serde::Serialize;

use crate::domain::Entity;

use super::replicator::Replicator;
use super::transport::TransportError;

/// The health of one remote. Go's `RemoteStatus`.
///
/// Declares no JSON shape, as in Go: an engine that exposes it projects
/// the fields into its own response.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RemoteStatus {
    /// The remote's name.
    pub name: String,
    /// Whether the transport was reachable on the last attempt.
    pub connected: bool,
    /// When the last round finished; `None` before the first. Go's
    /// zero `LastSync`.
    pub last_sync: Option<SystemTime>,
    /// Diffs queued and not yet passed over for this remote.
    pub pending_diffs: usize,
    /// The last transport error, if the last attempt failed.
    pub last_error: Option<TransportError>,
    /// Time since `last_sync` on the replicator's wall clock; zero
    /// before the first round.
    pub lag: Duration,
}

impl<T> Replicator<T>
where
    T: Entity + Serialize + DeserializeOwned + 'static,
{
    /// The health of every remote the replicator has started, ordered
    /// by name. Go's `Status`.
    pub fn status(&self) -> Vec<RemoteStatus> {
        let inner = &self.inner;
        let now = inner.wall.wall_time();
        let st = inner.state();
        let tail = st.base + st.queue.len() as u64;

        inner
            .remotes
            .list()
            .into_iter()
            .filter_map(|rem| {
                let s = st.statuses.get(&rem.name)?;
                let cursor = st.cursors.get(&rem.name).copied().unwrap_or(0).max(st.base);
                Some(RemoteStatus {
                    name: rem.name,
                    connected: s.connected,
                    last_sync: s.last_sync,
                    pending_diffs: usize::try_from(tail - cursor).unwrap_or(usize::MAX),
                    last_error: s.last_error.clone(),
                    lag: s.last_sync.map_or(Duration::ZERO, |t| {
                        now.duration_since(t).unwrap_or_default()
                    }),
                })
            })
            .collect()
    }
}
