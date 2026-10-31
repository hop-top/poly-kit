//! The explicit handler map: command path -> handler.

use std::collections::HashMap;
use std::fmt;
use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;

use crate::serve::CancelToken;

use super::wire::{CommandResult, HandlerError, Invocation};

/// What a handler returns.
pub type HandlerFuture = Pin<Box<dyn Future<Output = Result<CommandResult, HandlerError>> + Send>>;

/// Runs one command. `cancel` fires when the caller hangs up or the
/// service stops; a handler should return promptly, and is abandoned
/// if it does not.
pub trait Handler: Send + Sync {
    fn call(&self, inv: Invocation, cancel: CancelToken) -> HandlerFuture;
}

impl<F, Fut> Handler for F
where
    F: Fn(Invocation, CancelToken) -> Fut + Send + Sync,
    Fut: Future<Output = Result<CommandResult, HandlerError>> + Send + 'static,
{
    fn call(&self, inv: Invocation, cancel: CancelToken) -> HandlerFuture {
        Box::pin(self(inv, cancel))
    }
}

/// Refused registration.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RouteError {
    /// A route needs at least one path segment.
    EmptyPath,
    /// The path is already registered; replacing silently would make
    /// the winner depend on registration order.
    Duplicate(String),
    /// Annotating a path no handler is registered for.
    Unknown(String),
}

impl fmt::Display for RouteError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            RouteError::EmptyPath => f.write_str("socket: route path is empty"),
            RouteError::Duplicate(p) => write!(f, "socket: route {p:?} is already registered"),
            RouteError::Unknown(p) => write!(f, "socket: route {p:?} is not registered"),
        }
    }
}

impl std::error::Error for RouteError {}

/// What a command declares about itself, as a permission gate reads it
/// (`kit/side-effect` and `kit/permissions`). Empty when undeclared.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CommandMeta {
    /// The side-effect tier (`read`, `write-local`, `destructive`, ...).
    pub side_effect: String,
    /// The scopes the command requires, comma-joined.
    pub permissions: String,
}

/// Command path to handler. The key is the path joined by single
/// spaces, the `name` discovery uses.
#[derive(Clone, Default)]
pub struct Handlers {
    routes: HashMap<String, Arc<dyn Handler>>,
    meta: HashMap<String, CommandMeta>,
}

impl Handlers {
    pub fn new() -> Self {
        Self::default()
    }

    /// Registers `handler` for `path`.
    pub fn register<H: Handler + 'static>(
        &mut self,
        path: &[&str],
        handler: H,
    ) -> Result<&mut Self, RouteError> {
        if path.is_empty() {
            return Err(RouteError::EmptyPath);
        }
        let key = path.join(" ");
        if self.routes.contains_key(&key) {
            return Err(RouteError::Duplicate(key));
        }
        self.routes.insert(key, Arc::new(handler));
        Ok(self)
    }

    /// Declares what the command at `path` is: its side-effect tier and
    /// the scopes it requires, which a permission gate reads. A handler
    /// never annotated declares nothing.
    pub fn annotate(
        &mut self,
        path: &[&str],
        side_effect: &str,
        permissions: &str,
    ) -> Result<&mut Self, RouteError> {
        let key = path.join(" ");
        if !self.routes.contains_key(&key) {
            return Err(RouteError::Unknown(key));
        }
        self.meta.insert(
            key,
            CommandMeta {
                side_effect: side_effect.into(),
                permissions: permissions.into(),
            },
        );
        Ok(self)
    }

    pub(crate) fn meta(&self, path: &[String]) -> CommandMeta {
        self.meta.get(&path.join(" ")).cloned().unwrap_or_default()
    }

    pub(crate) fn get(&self, path: &[String]) -> Option<Arc<dyn Handler>> {
        self.routes.get(&path.join(" ")).cloned()
    }
}
