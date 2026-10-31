//! The scheme registry (Go `Registry`, `Register`, `Resolve`): URI
//! schemes to adapter factories, with what each adapter declares about
//! its key and default endpoint.

use std::collections::BTreeMap;
use std::sync::{Arc, OnceLock, RwLock};

use crate::netpolicy::NetPolicy;

use super::config::{ProviderConfig, ResolvedConfig};
use super::error::LlmError;
use super::provider::Provider;
use super::uri::{parse_uri, redact_uri};

/// Builds a provider from a resolved configuration.
pub type Factory = Arc<dyn Fn(ResolvedConfig) -> Result<Box<dyn Provider>, LlmError> + Send + Sync>;

/// The credential a scheme takes.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct ProviderKey {
    /// Variables (and secret-store names) holding the key, highest
    /// precedence first. Empty: the scheme takes no key.
    pub env_vars: Vec<String>,
    /// A local runtime: a missing key is no error, and `LLM_API_KEY` is
    /// never lent to it.
    pub optional: bool,
}

/// What an adapter states about its provider when it registers.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Declaration {
    /// The scheme's key; `None` takes the `<SCHEME>_API_KEY` convention.
    pub key: Option<ProviderKey>,
    /// The default endpoint, passed as `base_url` when the URI names
    /// neither a host nor a `base_url` param. Empty leaves the adapter's.
    pub base_url: String,
    /// Further base URLs that may receive the scheme's key when a URI
    /// names them, such as the adapter's own default. Compared by origin.
    pub endpoints: Vec<String>,
}

#[derive(Clone)]
struct Entry {
    factory: Factory,
    decl: Declaration,
}

/// Maps URI schemes to adapter factories.
///
/// Go's aim-catalog routing (aliases, protocol claims, catalog key
/// facts) is not ported: a scheme resolves only under its registered
/// name.
pub struct Registry {
    entries: RwLock<BTreeMap<String, Entry>>,
    policy: RwLock<NetPolicy>,
}

impl Default for Registry {
    fn default() -> Self {
        Self::new()
    }
}

impl std::fmt::Debug for Registry {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Registry")
            .field("schemes", &self.schemes())
            .field("policy", &self.policy())
            .finish()
    }
}

impl Registry {
    /// An empty registry under the online policy.
    pub fn new() -> Self {
        Registry {
            entries: RwLock::default(),
            policy: RwLock::default(),
        }
    }

    /// A registry holding every adapter compiled in.
    pub fn with_builtins() -> Self {
        let r = Self::new();
        #[cfg(feature = "llm-anthropic")]
        super::anthropic::register(&r).expect("empty registry");
        #[cfg(feature = "llm-openai")]
        super::openai::register(&r).expect("empty registry");
        #[cfg(feature = "llm-ollama")]
        super::ollama::register(&r).expect("empty registry");
        r
    }

    /// Add a factory for `scheme`. A scheme registers once.
    pub fn register(
        &self,
        scheme: &str,
        factory: Factory,
        decl: Declaration,
    ) -> Result<(), LlmError> {
        let mut entries = self.entries.write().expect("registry lock");
        if entries.contains_key(scheme) {
            return Err(LlmError::invalid(format!(
                "llm: adapter already registered for scheme {scheme:?}"
            )));
        }
        entries.insert(scheme.to_string(), Entry { factory, decl });
        Ok(())
    }

    /// Registered schemes, sorted.
    pub fn schemes(&self) -> Vec<String> {
        self.entries
            .read()
            .expect("registry lock")
            .keys()
            .cloned()
            .collect()
    }

    /// The network policy handed to every factory (`--offline`).
    pub fn policy(&self) -> NetPolicy {
        *self.policy.read().expect("registry lock")
    }

    pub fn set_policy(&self, policy: NetPolicy) {
        *self.policy.write().expect("registry lock") = policy;
    }

    /// The registered declaration of `scheme`, `None` when unknown.
    pub(crate) fn declaration(&self, scheme: &str) -> Option<Declaration> {
        self.entries
            .read()
            .expect("registry lock")
            .get(scheme)
            .map(|e| e.decl.clone())
    }

    /// [`Registry::resolve_with`] and default options: the adapter may
    /// read its ambient inputs.
    pub fn resolve(&self, uri: &str) -> Result<Box<dyn Provider>, LlmError> {
        self.resolve_with(uri, ResolveOptions::default())
    }

    /// Parse `uri`, find its adapter and build a provider (Go `Resolve`).
    ///
    /// The key is the URI's `api_key` param alone; a blank one is
    /// dropped, param included. Base URL, highest first: the `base_url`
    /// param, the URI's host (`http://host:port`), the declaration's.
    /// Use [`Registry::apply_api_key`] or [`Registry::load_config`] to
    /// bring in keys from llm.yaml, a store or the environment.
    ///
    /// [`ResolveOptions::hermetic`] hands the adapter
    /// [`ResolvedConfig::hermetic`]: it then reads no environment
    /// variable and no llm.yaml, only what the URI says.
    pub fn resolve_with(
        &self,
        uri: &str,
        opts: ResolveOptions,
    ) -> Result<Box<dyn Provider>, LlmError> {
        let mut parsed = parse_uri(uri).map_err(|e| {
            LlmError::invalid(format!("llm: invalid URI {:?}: {e}", redact_uri(uri)))
        })?;
        let entry = self
            .entries
            .read()
            .expect("registry lock")
            .get(&parsed.scheme)
            .cloned()
            .ok_or_else(|| LlmError::ProviderNotFound {
                scheme: parsed.scheme.clone(),
            })?;
        parsed.drop_blank_key();
        let mut provider = ProviderConfig {
            model: parsed.model.clone(),
            params: parsed.params.clone(),
            ..ProviderConfig::default()
        };
        if !parsed.host.is_empty() {
            provider.base_url = format!("http://{}", parsed.host);
        }
        if let Some(k) = parsed.params.get("api_key") {
            provider.api_key = k.clone();
        }
        if let Some(b) = parsed.params.get("base_url") {
            provider.base_url = b.clone();
        }
        if provider.base_url.is_empty() {
            provider.base_url = entry.decl.base_url.clone();
        }
        // A key never crosses the network in clear.
        super::endpoint::check_key_endpoint(&parsed.scheme, &provider.api_key, &provider.base_url)?;
        (entry.factory)(ResolvedConfig {
            uri: parsed,
            provider,
            fallbacks: Vec::new(),
            net_policy: self.policy(),
            hermetic: opts.hermetic,
        })
    }
}

/// Options of one [`Registry::resolve_with`] call (Go `ResolveOption`).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
#[non_exhaustive]
pub struct ResolveOptions {
    /// See [`ResolveOptions::hermetic`].
    pub hermetic: bool,
}

impl ResolveOptions {
    /// Read nothing ambient (Go `Hermetic`): the provider comes from the
    /// URI alone. The adapter gets [`ResolvedConfig::hermetic`], so it
    /// reads no environment variable and no llm.yaml: credentials and
    /// endpoints come only from the URI (`api_key`, `base_url`, host).
    /// For embedding kit in an app that owns its configuration.
    ///
    /// [`Registry::load_config`], [`Registry::apply_api_key`] and
    /// [`Registry::resolve_api_key`] exist to read llm.yaml, the
    /// environment and a secret store; a hermetic caller does not call
    /// them.
    pub fn hermetic() -> Self {
        ResolveOptions { hermetic: true }
    }
}

/// The process-wide registry, with every compiled-in adapter (Go
/// `DefaultRegistry`).
pub fn default_registry() -> &'static Registry {
    static DEFAULT: OnceLock<Registry> = OnceLock::new();
    DEFAULT.get_or_init(Registry::with_builtins)
}
