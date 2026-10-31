//! Local-first entity replication with multi-remote support. Gated
//! behind the `sync` feature, which builds on `domain` for the
//! [`Repository`](crate::domain::Repository) port and on `bus` for change
//! capture; `sync-http` adds the HTTP transport.
//!
//! Ports the Go canonical `hop.top/kit/go/runtime/sync`. The model is
//! remote-centric: a [`Replicator`] owns one repository and keeps
//! connections to N named [`Remote`]s, each with a [`SyncMode`]
//! (bidirectional, push only, pull only) and an optional filter.
//!
//! ```text
//! local change -> bus event -> Diff -> queue -> pushed to eligible remotes
//! remote change -> Transport::pull -> applied to the local repository
//! ```
//!
//! - [`Replicator`]: change capture, the diff queue with a cursor per
//!   remote, rounds (ping, push, pull, trim), [`status`](Replicator::status).
//! - [`Transport`]: push, pull and ping; [`MemoryTransport`] here and
//!   `HttpTransport` under `sync-http`.
//! - [`Clock`]: the hybrid logical clock ordering [`Timestamp`]s across
//!   nodes, over a [`WallClock`] you can fix or mock.
//! - [`Diff`]: an entity-level change as JSON snapshots; [`compute_diff`].
//! - [`last_write_wins`], [`resolve_diff`] and [`MergeFunc`]: conflicts.
//!
//! The core is pure given a caller [`WallClock`], so it builds for
//! wasm32-unknown-unknown; there no thread runs a loop and the caller
//! drives each round with [`Replicator::sync_once`]. Go's durable cursor,
//! `BusPublisher` and the SQLite repository are not ported; neither is
//! `domain/version.VersionedRepository`.
//!
//! # Example
//!
//! ```
//! use std::sync::{Arc, Mutex};
//! use std::collections::BTreeMap;
//! use hop_top_kit::bus::Context;
//! use hop_top_kit::domain::{Entity, Query, RepoError, Repository};
//! use hop_top_kit::sync::{
//!     Diff, MemoryTransport, Operation, Remote, Replicator, SyncMode, Timestamp, Transport,
//! };
//! use serde::{Deserialize, Serialize};
//!
//! #[derive(Clone, Serialize, Deserialize)]
//! struct Note { id: String, text: String }
//! impl Entity for Note { fn id(&self) -> &str { &self.id } }
//!
//! #[derive(Default)]
//! struct Mem(Mutex<BTreeMap<String, Note>>);
//! impl Repository<Note> for Mem {
//!     fn create(&self, _: &Context, e: &Note) -> Result<(), RepoError> {
//!         self.0.lock().unwrap().insert(e.id.clone(), e.clone());
//!         Ok(())
//!     }
//!     fn get(&self, _: &Context, id: &str) -> Result<Note, RepoError> {
//!         self.0.lock().unwrap().get(id).cloned().ok_or(RepoError::NotFound)
//!     }
//!     fn list(&self, _: &Context, _: &Query) -> Result<Vec<Note>, RepoError> { Ok(vec![]) }
//!     fn update(&self, _: &Context, e: &Note) -> Result<(), RepoError> {
//!         match self.0.lock().unwrap().get_mut(&e.id) {
//!             Some(s) => { *s = e.clone(); Ok(()) }
//!             None => Err(RepoError::NotFound),
//!         }
//!     }
//!     fn delete(&self, _: &Context, id: &str) -> Result<(), RepoError> {
//!         self.0.lock().unwrap().remove(id);
//!         Ok(())
//!     }
//! }
//!
//! let ctx = Context::background();
//! let peer = Arc::new(MemoryTransport::new());
//! peer.push(&ctx, &[Diff {
//!     entity_id: "n1".into(),
//!     operation: Operation::Create,
//!     after: Some(br#"{"id":"n1","text":"hello"}"#.to_vec()),
//!     timestamp: Timestamp { physical: 1, logical: 0, node_id: "peer".into() },
//!     ..Diff::default()
//! }]).unwrap();
//!
//! let repo = Arc::new(Mem::default());
//! let rep = Replicator::builder(repo.clone())
//!     .remote(Remote::new("origin", peer).mode(SyncMode::PullOnly))
//!     .build();
//! rep.sync_once(&ctx, "origin").unwrap();
//! assert_eq!(repo.get(&ctx, "n1").unwrap().text, "hello");
//! ```

mod clock;
mod diff;
#[cfg(feature = "sync-http")]
mod http;
mod merge;
mod remote;
mod replicator;
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
mod runner;
mod status;
mod transport;

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub use clock::SystemWallClock;
pub use clock::{Clock, FixedClock, MockWallClock, Timestamp, WallClock};
pub use diff::{compute_diff, Diff, DiffError, Operation};
#[cfg(feature = "sync-http")]
pub use http::HttpTransport;
pub use merge::{last_write_wins, resolve_diff, MergeError, MergeFunc};
pub use remote::{Filter, Remote, RemoteSet, SyncError, SyncMode, Transport};
pub use replicator::{
    Logger, Replicator, ReplicatorBuilder, StderrLogger, DEFAULT_SUBSCRIPTION_PREFIX,
    MAX_QUEUE_SIZE,
};
pub use status::RemoteStatus;
pub use transport::{MemoryTransport, TransportError};
