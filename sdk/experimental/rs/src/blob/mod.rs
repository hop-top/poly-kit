//! Blob storage primitive — key/value object storage over a pluggable backend.
//!
//! Mirrors the Go `hop.top/kit/go/storage/blob` API shape:
//!
//! - [`Object`] — key + size + content type metadata.
//! - [`Store`] — the backend trait (`put` / `get` / `delete` / `list` /
//!   `exists`).
//! - [`local::LocalStore`] — filesystem-backed backend rooted at a directory.
//!
//! Only the local backend is ported. The Go S3 backend is intentionally
//! out of scope for the Rust SDK.
//!
//! # Write atomicity
//!
//! [`local::LocalStore::put`] stages contents in a sibling temp file, syncs
//! it, then renames it over the destination. A concurrent [`Store::get`]
//! therefore never observes a partial blob, and an interrupted write leaves
//! any previous value intact. This matches the Go backend byte for byte in
//! semantics.

pub mod local;

use std::io::Read;

use thiserror::Error;

/// Metadata about a stored blob.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Object {
    /// Slash-separated key the blob is stored under.
    pub key: String,
    /// Size in bytes.
    pub size: u64,
    /// Content type, empty when the backend does not track one.
    pub content_type: String,
}

/// Errors returned by blob store operations.
#[derive(Debug, Error)]
pub enum BlobError {
    /// The key resolves outside the store root.
    #[error("blob/local: key {0:?} escapes store root")]
    KeyEscapesRoot(String),

    /// The requested key does not exist.
    #[error("blob/local: key {0:?} not found")]
    NotFound(String),

    /// An underlying filesystem operation failed.
    #[error("blob/local: {op}: {source}")]
    Io {
        /// Short name of the failed operation (`create tmp`, `rename`, …).
        op: &'static str,
        /// Underlying I/O error.
        #[source]
        source: std::io::Error,
    },
}

impl BlobError {
    pub(crate) fn io(op: &'static str, source: std::io::Error) -> Self {
        BlobError::Io { op, source }
    }
}

/// Backend-agnostic blob storage interface.
pub trait Store {
    /// Reader type handed back by [`Store::get`].
    type Reader: Read;

    /// Write the full contents of `r` to `key`.
    fn put<R: Read>(&self, key: &str, r: R, content_type: &str) -> Result<(), BlobError>;

    /// Open the blob at `key` for reading.
    fn get(&self, key: &str) -> Result<Self::Reader, BlobError>;

    /// Remove the blob at `key`.
    fn delete(&self, key: &str) -> Result<(), BlobError>;

    /// Return every object whose key starts with `prefix`.
    fn list(&self, prefix: &str) -> Result<Vec<Object>, BlobError>;

    /// Report whether a blob exists at `key`.
    fn exists(&self, key: &str) -> Result<bool, BlobError>;
}
