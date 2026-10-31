//! The adapter seam: [`Provider`] and the streaming [`TokenStream`].
//!
//! Go probes capabilities by type assertion; here a provider lists them
//! and leaves the calls it lacks at their defaults. Futures are boxed so
//! providers sit behind `dyn`; no runtime is named. Cancellation is
//! dropping the future or the stream.

use std::future::Future;
use std::pin::Pin;

use super::error::LlmError;
use super::types::{Request, Response, StreamOutcome, Token, ToolDef, ToolResponse};

/// A boxed, sendable future.
pub type BoxFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// A call a provider may support (Go's `Capabilities` strings).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Capability {
    Complete,
    Stream,
    ToolCall,
    /// Streaming with tools: text tokens, then the calls in
    /// [`TokenStream::outcome`]. Not in Go.
    StreamTools,
}

impl Capability {
    pub fn as_str(self) -> &'static str {
        match self {
            Capability::Complete => "complete",
            Capability::Stream => "stream",
            Capability::ToolCall => "tool_call",
            Capability::StreamTools => "stream_tools",
        }
    }
}

fn unsupported<T>(capability: Capability) -> Result<T, LlmError> {
    Err(LlmError::CapabilityNotSupported {
        capability: capability.as_str().to_string(),
        provider: "provider".to_string(),
    })
}

/// An LLM adapter. Implement the calls listed in
/// [`Provider::capabilities`]; the rest default to
/// [`LlmError::CapabilityNotSupported`].
pub trait Provider: Send + Sync {
    fn capabilities(&self) -> &'static [Capability];

    fn complete<'a>(&'a self, _req: &'a Request) -> BoxFuture<'a, Result<Response, LlmError>> {
        Box::pin(async { unsupported(Capability::Complete) })
    }

    fn stream<'a>(
        &'a self,
        _req: &'a Request,
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(async { unsupported(Capability::Stream) })
    }

    fn call_with_tools<'a>(
        &'a self,
        _req: &'a Request,
        _tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<ToolResponse, LlmError>> {
        Box::pin(async { unsupported(Capability::ToolCall) })
    }

    fn stream_with_tools<'a>(
        &'a self,
        _req: &'a Request,
        _tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(async { unsupported(Capability::StreamTools) })
    }
}

/// Streaming tokens, Go's `TokenIterator`.
///
/// Contract: text tokens, then one token with `done` set, then `None`
/// on every later call. An error ends the stream the same way: `None`
/// after it. Dropping the stream closes the connection.
pub trait TokenStream: Send {
    fn next(&mut self) -> BoxFuture<'_, Result<Option<Token>, LlmError>>;

    /// What the stream folded into, once `done` was returned: text,
    /// tool calls, stop reason and usage. `None` before then.
    fn outcome(&self) -> Option<&StreamOutcome> {
        None
    }
}
