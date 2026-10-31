//! The repository port: what a store of entities must offer. Go's
//! `Entity`, `Query`, `Repository[T]` and the `ErrNotFound`,
//! `ErrConflict` and `ErrValidation` sentinels.
//!
//! The port is dependency-free and does no I/O itself: an implementation
//! (in memory, SQLite, a remote API) lives with its adopter.
//!
//! # Where this differs from Go
//!
//! - Go's `ctx context.Context` is a [`Context`], first after `&self`.
//! - Methods take `&self` and the trait is `Send + Sync`: an
//!   implementation brings its own interior mutability, as Go's
//!   mutex-guarded repositories do. A `&dyn Repository<T>` is usable
//!   from several threads.
//! - Go's `Get` returns `*T`, `List` `[]T`; here they return owned `T`
//!   and `Vec<T>`, and `Create`/`Update` borrow the entity.
//! - Go's `errors.Is(err, ErrNotFound)` is [`RepoError::is_not_found`]
//!   (likewise `is_conflict`, `is_validation`).

use std::error::Error;
use std::fmt;

use crate::bus::Context;

/// The base constraint for domain objects: identifiable by a string id.
/// Go's `Entity` (`GetID`).
pub trait Entity {
    /// The entity's id, unique within its repository.
    fn id(&self) -> &str;
}

/// Common list and search parameters. Go's `Query`; the default is no
/// limit, no offset, no sort and no search.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Query {
    /// Caps the number of results. Zero means no limit.
    pub limit: usize,
    /// Skips the first N results (for pagination).
    pub offset: usize,
    /// The column or field to order by.
    pub sort: String,
    /// A free-text filter, applied via `LIKE` or similar.
    pub search: String,
}

/// Why a repository operation failed. The three Go sentinels, plus
/// [`RepoError::Other`] for anything an implementation reports of its
/// own (I/O, encoding, a remote refusal).
#[derive(Debug)]
pub enum RepoError {
    /// The requested entity does not exist. Go's `ErrNotFound`.
    NotFound,
    /// A uniqueness or concurrency conflict. Go's `ErrConflict`.
    Conflict,
    /// One or more validation rules failed. Go's `ErrValidation`.
    Validation,
    /// Any other failure, carried as is.
    Other(Box<dyn Error + Send + Sync + 'static>),
}

impl RepoError {
    /// A [`RepoError::Other`] with a message and no source.
    pub fn other(msg: impl Into<String>) -> Self {
        RepoError::Other(msg.into().into())
    }

    /// A [`RepoError::Other`] wrapping `err`, kept as its `source()`.
    pub fn from_source(err: impl Error + Send + Sync + 'static) -> Self {
        RepoError::Other(Box::new(err))
    }

    /// Go's `errors.Is(err, ErrNotFound)`.
    pub fn is_not_found(&self) -> bool {
        matches!(self, RepoError::NotFound)
    }

    /// Go's `errors.Is(err, ErrConflict)`.
    pub fn is_conflict(&self) -> bool {
        matches!(self, RepoError::Conflict)
    }

    /// Go's `errors.Is(err, ErrValidation)`.
    pub fn is_validation(&self) -> bool {
        matches!(self, RepoError::Validation)
    }
}

impl fmt::Display for RepoError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            RepoError::NotFound => f.write_str("domain: not found"),
            RepoError::Conflict => f.write_str("domain: conflict"),
            RepoError::Validation => f.write_str("domain: validation error"),
            RepoError::Other(e) => fmt::Display::fmt(e, f),
        }
    }
}

impl Error for RepoError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            RepoError::Other(e) => Some(&**e),
            _ => None,
        }
    }
}

/// Generic CRUD over any [`Entity`]. Go's `Repository[T]`.
pub trait Repository<T: Entity>: Send + Sync {
    /// Persists a new entity. [`RepoError::Conflict`] if the id exists.
    fn create(&self, ctx: &Context, entity: &T) -> Result<(), RepoError>;

    /// Retrieves an entity by id. [`RepoError::NotFound`] if absent.
    fn get(&self, ctx: &Context, id: &str) -> Result<T, RepoError>;

    /// Returns the entities matching `q`.
    fn list(&self, ctx: &Context, q: &Query) -> Result<Vec<T>, RepoError>;

    /// Replaces an existing entity. [`RepoError::NotFound`] if absent.
    fn update(&self, ctx: &Context, entity: &T) -> Result<(), RepoError>;

    /// Removes an entity by id. [`RepoError::NotFound`] if absent.
    fn delete(&self, ctx: &Context, id: &str) -> Result<(), RepoError>;
}
