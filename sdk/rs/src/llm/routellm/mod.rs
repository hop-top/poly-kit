//! The RouteLLM adapter (Go `ai/llm/routellm`): a RouteLLM server spoken
//! to through the [`openai`] adapter. Registers the
//! `routellm` scheme. Feature `llm-routellm`, native only.
//!
//! The model field takes either shape a RouteLLM server advertises on
//! `/v1/models`:
//!
//! - `router_name:threshold`, as `routellm://mf:0.7`, sent as the
//!   server's `router-<name>-<threshold>`: the client picks the router
//!   and the threshold (in `[0, 1]`);
//! - a tier name, as `routellm://private`, sent verbatim: the server
//!   resolves router and threshold from its own tier config.
//!
//! The text after the *last* colon decides: only if it parses as a finite
//! float is the model a router pair; anything else is a tier, colons
//! included. See [`parse_model_field`].
//!
//! The base URL, highest first: the provider's `base_url`; the
//! `ROUTELLM_BASE_URL` variable; the llm.yaml block's `routellm.base_url`
//! extra; `http://localhost:6060`. `/v1` is appended unless the URL
//! already ends in it. Hermetic reads no `ROUTELLM_*` variable. The size
//! caps and `tool_names` params reach the inner adapter, which reports
//! errors under the `routellm` scheme.
//!
//! Not ported: Go's `ConfigWatcher` (mtime polling of a YAML file).

mod config;
#[cfg(test)]
mod tests;

use std::sync::Arc;

use crate::llm::openai;
use crate::llm::{
    BoxFuture, Capability, Declaration, LlmError, Provider, ProviderConfig, ProviderKey, Registry,
    Request, ResolvedConfig, Response, Sleeper, TokenStream, ToolDef, ToolResponse,
    TOOL_NAMES_PARAM,
};
use crate::netpolicy::GuardedClient;

pub use config::{EvaConfig, RouterConfig};

pub const SCHEME: &str = "routellm";
pub const DEFAULT_BASE_URL: &str = "http://localhost:6060";

/// The classified model field: a client-selected router and threshold,
/// or a tier the server resolves.
#[derive(Debug, Clone, PartialEq)]
pub enum ModelField {
    Router { name: String, threshold: f64 },
    Tier(String),
}

/// Classify `model` as a `router_name:threshold` pair or a tier name.
///
/// A server advertises both shapes (`private`, `coding_fast`,
/// `router-mf-0.5`), and a tier name may hold anything, colons included.
/// The shapes are told apart by the one structurally constrained part:
/// the text after the last colon parses as a finite float for a router
/// pair. A leading colon (empty router name) or trailing one (empty
/// threshold) stays a tier, as do `NaN` and `Inf` suffixes. An unknown
/// tier fails at the server with its own model-not-found error.
pub fn parse_model_field(model: &str) -> Result<ModelField, LlmError> {
    if model.is_empty() {
        return Err(LlmError::invalid(
            "model field is required (router_name:threshold or tier name)",
        ));
    }
    if let Some(idx) = model.rfind(':') {
        if idx > 0 && idx < model.len() - 1 {
            if let Some(threshold) = parse_float(&model[idx + 1..]) {
                return Ok(ModelField::Router {
                    name: model[..idx].to_string(),
                    threshold,
                });
            }
        }
    }
    Ok(ModelField::Tier(model.to_string()))
}

/// A finite float as Go's `ParseFloat` reads it, bar the spellings Rust
/// takes that Go does not (`+inf`, `infinity` are non-finite anyway).
fn parse_float(s: &str) -> Option<f64> {
    // Rust accepts a leading '+', digits with '.', exponents; Go also
    // accepts underscores only with base prefixes, which are not floats
    // here. Reject non-finite results.
    s.parse::<f64>().ok().filter(|f| f.is_finite())
}

/// Go's `strconv.FormatFloat(f, 'f', -1, 64)`: shortest digits, no
/// exponent.
fn format_threshold(f: f64) -> String {
    let s = format!("{f}");
    if s.contains('e') || s.contains('E') {
        // Display for f64 never emits an exponent; kept for safety.
        return format!("{f:.17}")
            .trim_end_matches('0')
            .trim_end_matches('.')
            .to_string();
    }
    s
}

type EnvFn = Box<dyn Fn(&str) -> Option<String> + Send + Sync>;

/// The adapter: an inner openai adapter pointed at a RouteLLM server.
pub struct Adapter {
    inner: openai::Adapter,
    config: RouterConfig,
    field: ModelField,
    base_url: String,
}

impl std::fmt::Debug for Adapter {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Adapter")
            .field("inner", &self.inner)
            .field("field", &self.field)
            .finish_non_exhaustive()
    }
}

/// Configures an [`Adapter`] beyond its [`ResolvedConfig`].
pub struct Builder {
    cfg: ResolvedConfig,
    env: EnvFn,
    client: Option<GuardedClient>,
    sleeper: Option<Sleeper>,
    max_retries: Option<u32>,
}

impl Builder {
    /// Read no `ROUTELLM_*` variable, and no ambient input of the inner
    /// adapter.
    pub fn hermetic(mut self) -> Self {
        self.cfg.hermetic = true;
        self
    }

    /// Read `ROUTELLM_*` from `env` instead of the process environment.
    pub fn env(mut self, env: impl Fn(&str) -> Option<String> + Send + Sync + 'static) -> Self {
        self.env = Box::new(env);
        self
    }

    /// Send through `client`; see [`openai::Builder::client`].
    pub fn client(mut self, client: GuardedClient) -> Self {
        self.client = Some(client);
        self
    }

    /// The inner adapter's retry timer.
    pub fn sleeper(mut self, sleeper: Sleeper) -> Self {
        self.sleeper = Some(sleeper);
        self
    }

    /// The inner adapter's retries after the first attempt.
    pub fn max_retries(mut self, n: u32) -> Self {
        self.max_retries = Some(n);
        self
    }

    pub fn build(self) -> Result<Adapter, LlmError> {
        let cfg = &self.cfg;
        let hermetic = cfg.hermetic;
        let rcfg = RouterConfig::parse(&cfg.provider.extras, |n| {
            if hermetic {
                None
            } else {
                (self.env)(n)
            }
        })
        .map_err(|e| LlmError::invalid(format!("routellm: {e}")))?;
        let model = if cfg.provider.model.is_empty() {
            cfg.uri.model.clone()
        } else {
            cfg.provider.model.clone()
        };
        let field =
            parse_model_field(&model).map_err(|e| LlmError::invalid(format!("routellm: {e}")))?;
        // What the server expects: a tier verbatim, a pair as
        // "router-<name>-<threshold>".
        let server_model = match &field {
            ModelField::Tier(t) => t.clone(),
            ModelField::Router { name, threshold } => {
                if !(0.0..=1.0).contains(threshold) {
                    return Err(LlmError::invalid(format!(
                        "routellm: threshold {threshold:.4} out of range [0, 1]"
                    )));
                }
                format!("router-{name}-{}", format_threshold(*threshold))
            }
        };
        // Explicit provider > router config (env over extras) > default.
        let mut base_url = cfg.provider.base_url.clone();
        if base_url.is_empty() {
            base_url = rcfg.base_url.clone();
        }
        if base_url.is_empty() {
            base_url = DEFAULT_BASE_URL.to_string();
        }
        if !base_url.ends_with("/v1") {
            base_url.push_str("/v1");
        }
        let mut provider = ProviderConfig {
            api_key: cfg.provider.api_key.clone(),
            base_url: base_url.clone(),
            model: server_model,
            ..ProviderConfig::default()
        };
        // The inner adapter applies the tool-name mode and the size caps
        // the URI asks for.
        for k in [TOOL_NAMES_PARAM, "max_body_bytes", "max_event_bytes"] {
            if let Some(v) = cfg.provider.params.get(k) {
                provider.params.insert(k.to_string(), v.clone());
            }
        }
        let inner_cfg = ResolvedConfig {
            uri: cfg.uri.clone(),
            provider,
            fallbacks: Vec::new(),
            net_policy: cfg.net_policy.clone(),
            hermetic: cfg.hermetic,
        };
        let mut b = openai::Adapter::builder(inner_cfg);
        if let Some(c) = self.client {
            b = b.client(c);
        }
        if let Some(s) = self.sleeper {
            b = b.sleeper(s);
        }
        if let Some(n) = self.max_retries {
            b = b.max_retries(n);
        }
        let inner = b
            .build()
            .map_err(|e| LlmError::invalid(format!("routellm: create inner adapter: {e}")))?;
        Ok(Adapter {
            inner,
            config: rcfg,
            field,
            base_url,
        })
    }
}

impl Adapter {
    pub fn new(cfg: ResolvedConfig) -> Result<Self, LlmError> {
        Self::builder(cfg).build()
    }

    pub fn builder(cfg: ResolvedConfig) -> Builder {
        Builder {
            cfg,
            env: Box::new(crate::llm::process_env),
            client: None,
            sleeper: None,
            max_retries: None,
        }
    }

    /// Whether the adapter targets a server-resolved tier rather than a
    /// client-selected `router_name:threshold` pair. Use this, not
    /// `threshold() == 0`, which is also a legitimate threshold.
    pub fn is_tier(&self) -> bool {
        matches!(self.field, ModelField::Tier(_))
    }

    /// The router name of a pair; empty for a tier.
    pub fn router_name(&self) -> &str {
        match &self.field {
            ModelField::Router { name, .. } => name,
            ModelField::Tier(_) => "",
        }
    }

    /// The threshold of a pair; 0 for a tier.
    pub fn threshold(&self) -> f64 {
        match &self.field {
            ModelField::Router { threshold, .. } => *threshold,
            ModelField::Tier(_) => 0.0,
        }
    }

    /// The tier name; empty for a pair.
    pub fn tier(&self) -> &str {
        match &self.field {
            ModelField::Tier(t) => t,
            ModelField::Router { .. } => "",
        }
    }

    /// The API root the inner adapter sends to, credentials masked.
    pub fn base_url(&self) -> String {
        crate::llm::redact_uri(&self.base_url)
    }

    /// The parsed router configuration.
    pub fn config(&self) -> &RouterConfig {
        &self.config
    }
}

impl Provider for Adapter {
    fn capabilities(&self) -> &'static [Capability] {
        self.inner.capabilities()
    }

    fn complete<'a>(&'a self, req: &'a Request) -> BoxFuture<'a, Result<Response, LlmError>> {
        self.inner.complete(req)
    }

    fn stream<'a>(
        &'a self,
        req: &'a Request,
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        self.inner.stream(req)
    }

    fn call_with_tools<'a>(
        &'a self,
        req: &'a Request,
        tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<ToolResponse, LlmError>> {
        self.inner.call_with_tools(req, tools)
    }

    fn stream_with_tools<'a>(
        &'a self,
        req: &'a Request,
        tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        self.inner.stream_with_tools(req, tools)
    }
}

/// Register the `routellm` scheme on `registry`: a local router, so
/// `ROUTELLM_API_KEY` is optional and sent when set.
pub fn register(registry: &Registry) -> Result<(), LlmError> {
    registry.register(
        SCHEME,
        Arc::new(|cfg| Ok(Box::new(Adapter::new(cfg)?) as Box<dyn Provider>)),
        Declaration {
            key: Some(ProviderKey {
                env_vars: vec!["ROUTELLM_API_KEY".into()],
                optional: true,
            }),
            ..Declaration::default()
        },
    )
}
