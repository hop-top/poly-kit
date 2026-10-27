//! Provider-neutral LLM access, a port of Go `kit/go/ai/llm`.
//!
//! - [`types`]: [`Message`], [`Request`], [`Response`], tool calls,
//!   streaming [`Token`]s.
//! - [`Provider`] and [`TokenStream`]: the adapter seam. Futures are
//!   boxed and runtime-free; dropping one cancels it.
//! - [`Registry`]: URI schemes (`anthropic://claude-opus-5-5`) to
//!   adapter factories; [`default_registry`] holds the compiled-in ones.
//! - Keys and config: [`Registry::resolve_api_key`],
//!   [`Registry::apply_api_key`], [`Registry::load_config`] read
//!   llm.yaml, the environment and a [`SecretStore`] through
//!   [`Sources`], with Go's precedence.
//! - [`Client`]: fallback chain and bus events.
//! - [`LlmError`]: Go's error taxonomy; [`LlmError::is_fallbackable`].
//! - [`anthropic`] (feature `llm-anthropic`): the Messages API adapter.
//!
//! Keys never reach `Debug` or error text: [`redact_uri`] masks URIs,
//! and config types print `REDACTED` in a key's place.
//!
//! Not ported: Go's aim catalog routing (aliases, protocol claims),
//! the model picker and router, media URL/file sources, and adapters
//! other than Anthropic.

mod client;
mod config;
mod endpoint;
mod error;
mod keys;
mod provider;
mod registry;
pub mod types;
mod uri;

#[cfg(feature = "llm-anthropic")]
pub mod anthropic;

pub use client::{Client, Topics};
pub use config::{
    process_env, ConfigFile, ProviderBlock, ProviderConfig, ResolvedConfig, SecretStore, Sources,
};
pub use endpoint::{
    check_endpoint_url, check_key_endpoint, credentials_trusted, is_loopback_url, parse_endpoint,
};
pub use error::{KeyEndpointReason, LlmError, MissingKeyError};
pub use keys::{KeyResolution, KeySource, KeySourceKind, FALLBACK_ENV_KEY};
pub use provider::{BoxFuture, Capability, Provider, TokenStream};
pub use registry::{default_registry, Declaration, Factory, ProviderKey, Registry};
pub use types::{
    Media, MediaKind, Message, Part, ProviderData, Request, Response, Role, StreamOutcome, Token,
    ToolCall, ToolDef, ToolResponse, Usage,
};
pub use uri::{parse_uri, redact_uri, Uri};

/// [`Registry::load_config`] on the default registry, with llm.yaml from
/// disk, the process environment and no secret store.
#[cfg(not(target_arch = "wasm32"))]
pub fn load_config(uri: &str) -> Result<ResolvedConfig, LlmError> {
    let file = ConfigFile::load();
    default_registry().load_config(
        &Sources {
            file: &file,
            env: &process_env,
            store: None,
        },
        uri,
    )
}

/// [`Registry::apply_api_key`] on the default registry, with llm.yaml
/// from disk and the process environment.
#[cfg(not(target_arch = "wasm32"))]
pub fn apply_api_key(store: Option<&dyn SecretStore>, uri: &str) -> Result<String, LlmError> {
    let file = ConfigFile::load();
    default_registry().apply_api_key(
        &Sources {
            file: &file,
            env: &process_env,
            store,
        },
        uri,
    )
}

/// [`Registry::resolve`] on the default registry.
pub fn resolve(uri: &str) -> Result<Box<dyn Provider>, LlmError> {
    default_registry().resolve(uri)
}
