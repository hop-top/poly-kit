//! Rust SDK for `kit serve`: identity, storage, sync and peer discovery
//! through the engine sidecar.
//!
//! Features: `http` (default) for lifecycle, collections, sync, peers and
//! identity; `events` adds the WebSocket event stream.

#![cfg_attr(docsrs, feature(doc_cfg))]

#[cfg(feature = "http")]
mod collection;
#[cfg(feature = "http")]
mod engine;
#[cfg(feature = "http")]
mod error;
#[cfg(feature = "events")]
mod events;
#[cfg(feature = "http")]
mod identity;
#[cfg(feature = "http")]
mod peers;
#[cfg(feature = "http")]
mod sync;

#[cfg(feature = "http")]
pub use error::EngineError;
