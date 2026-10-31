//! Blob storage primitive — key/value object storage over a pluggable backend.
//!
//! Mirrors the Go `hop.top/kit/go/storage/blob` API shape:
//!
//! - [`Object`] — key + size + content type metadata.
//! - [`Store`] — the backend trait (`put` / `get` / `delete` / `list` /
//!   `exists`).
//! - [`Stater`] — optional single-object metadata lookup (`stat`), a
//!   separate trait so [`Store`] is unchanged.
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

    /// The key falls in the store's reserved metadata namespace.
    #[error("blob/local: key {0:?} uses reserved namespace \".blob-meta\"")]
    ReservedKey(String),

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

    /// Remove the blob at `key`. Idempotent: a missing key is `Ok(())`.
    fn delete(&self, key: &str) -> Result<(), BlobError>;

    /// Return every object whose key starts with `prefix`.
    fn list(&self, prefix: &str) -> Result<Vec<Object>, BlobError>;

    /// Report whether a blob exists at `key`.
    fn exists(&self, key: &str) -> Result<bool, BlobError>;
}

/// Optional capability of a [`Store`]: report the metadata of one object,
/// including its content type, without listing.
///
/// Deliberately not part of [`Store`], so backends that cannot or need not
/// support it are not forced to. The returned [`Object`] has the same shape
/// [`Store::list`] yields, but `content_type` is the exact type of the
/// latest `put` (empty when none was given). A missing key is
/// [`BlobError::NotFound`], as with [`Store::get`].
///
/// Rust has no runtime trait probing, so there is no single helper that
/// "uses `Stater` when present": bound on `S: Stater` when you need it, and
/// call [`stat_by_list`] for a plain [`Store`].
pub trait Stater {
    /// Return the metadata of the blob at `key`.
    fn stat(&self, key: &str) -> Result<Object, BlobError>;
}

/// Fallback for stores without [`Stater`]: `list(key)` and pick the exact
/// match. `content_type` is only as reliable as the store's `list` (possibly
/// empty), and the cost is a listing of every key sharing the prefix.
/// A missing key is [`BlobError::NotFound`].
pub fn stat_by_list<S: Store>(store: &S, key: &str) -> Result<Object, BlobError> {
    store
        .list(key)?
        .into_iter()
        .find(|o| o.key == key)
        .ok_or_else(|| BlobError::NotFound(key.to_string()))
}
