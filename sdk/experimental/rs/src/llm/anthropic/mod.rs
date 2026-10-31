//! The Anthropic Messages API adapter (Go `ai/llm/anthropic`), over
//! the HTTP API through the guarded `api` client: no SDK crate.
//!
//! Registers the `anthropic` scheme. Supports complete, stream, tool
//! calls and streaming with tools. The key comes from the resolved
//! config only; `ANTHROPIC_BASE_URL` (below a configured base URL),
//! `ANTHROPIC_AUTH_TOKEN` (sent as a bearer token when not blank) and
//! `ANTHROPIC_WORKSPACE_ID` are read as in Go, and llm.yaml and
//! `LLM_BASE_URL` decide whether the latter two may go to the endpoint.
//! [`Builder::hermetic`] (or [`ResolvedConfig::hermetic`]) reads none of
//! them. Native only.
//!
//! Tool names follow [`ANTHROPIC_TOOL_NAMES`]: sanitized on the way out
//! and restored on returned calls, per the `tool_names` provider param
//! ([`ToolNameMode`]); see [`ToolNames`].

mod http;
mod sse;
mod stream;
mod wire;

use std::sync::Arc;

use crate::llm::{
    BoxFuture, Capability, Declaration, LlmError, Provider, Registry, Request, ResolvedConfig,
    Response, TokenStream, ToolDef, ToolNameMode, ToolNames, ToolResponse, ANTHROPIC_TOOL_NAMES,
};
use crate::netpolicy::GuardedClient;

pub const SCHEME: &str = "anthropic";
pub const DEFAULT_BASE_URL: &str = "https://api.anthropic.com";
pub const API_VERSION: &str = "2023-06-01";
/// `max_tokens` when a request leaves it 0.
pub const DEFAULT_MAX_TOKENS: u32 = 1024;
/// Retries after the first attempt, as the Anthropic SDKs default.
pub const DEFAULT_MAX_RETRIES: u32 = 2;

pub use crate::llm::{Limits, Sleeper};

type EnvFn = Box<dyn Fn(&str) -> Option<String> + Send + Sync>;

/// The adapter. `Debug` never prints the key or token.
pub struct Adapter {
    client: GuardedClient,
    url: reqwest::Url,
    destination: String,
    /// Every credential header, chosen once at build behind
    /// `credentials_trusted`; `send` adds exactly these.
    credentials: Vec<(&'static str, String)>,
    model: String,
    max_retries: u32,
    sleeper: Option<Sleeper>,
    limits: Limits,
    scrub: stream::Scrub,
    /// The `tool_names` param: how names the API rejects are handled.
    tool_names: ToolNameMode,
}

impl std::fmt::Debug for Adapter {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Adapter")
            .field("url", &self.destination)
            .field("model", &self.model)
            .field("credentials", &self.credential_headers())
            .field("max_retries", &self.max_retries)
            .finish_non_exhaustive()
    }
}

/// Configures an [`Adapter`] beyond its [`ResolvedConfig`].
pub struct Builder {
    cfg: ResolvedConfig,
    env: EnvFn,
    client: Option<GuardedClient>,
    sleeper: Option<Sleeper>,
    max_retries: u32,
    file: Option<crate::llm::ConfigFile>,
    limits: Option<Limits>,
}

use crate::llm::wirehttp::tokio_sleeper;

impl Builder {
    /// Read nothing ambient (Go `ResolvedConfig.Hermetic`): no
    /// `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`,
    /// `ANTHROPIC_WORKSPACE_ID` or `LLM_BASE_URL`, and no llm.yaml, from
    /// the process or from [`Builder::env`] and [`Builder::config_file`].
    /// The request goes to the configured base URL, else
    /// [`DEFAULT_BASE_URL`], with the configured key only. Same as
    /// [`ResolvedConfig::hermetic`].
    ///
    /// Proxy variables (`HTTPS_PROXY` and friends) are the HTTP
    /// client's, as with Go's `net/http`; pass [`Builder::client`] to
    /// set the transport yourself.
    pub fn hermetic(mut self) -> Self {
        self.cfg.hermetic = true;
        self
    }

    /// An ambient variable: `None` when hermetic.
    fn ambient(&self, name: &str) -> Option<String> {
        if self.cfg.hermetic {
            return None;
        }
        (self.env)(name)
    }

    /// Read `ANTHROPIC_*` from `env` instead of the process environment.
    pub fn env(mut self, env: impl Fn(&str) -> Option<String> + Send + Sync + 'static) -> Self {
        self.env = Box::new(env);
        self
    }

    /// Send through `client` (timeouts, proxies). Its policy applies,
    /// not the config's.
    pub fn client(mut self, client: GuardedClient) -> Self {
        self.client = Some(client);
        self
    }

    /// The retry timer; the default is tokio's.
    pub fn sleeper(mut self, sleeper: Sleeper) -> Self {
        self.sleeper = Some(sleeper);
        self
    }

    /// The llm.yaml whose `providers.anthropic.base_url` counts as a
    /// trusted origin; the default reads it from disk at build.
    pub fn config_file(mut self, file: crate::llm::ConfigFile) -> Self {
        self.file = Some(file);
        self
    }

    /// Response size limits; outranks the `max_*_bytes` params.
    pub fn limits(mut self, limits: Limits) -> Self {
        self.limits = Some(limits);
        self
    }

    /// Retries after the first attempt; 0 disables them.
    pub fn max_retries(mut self, n: u32) -> Self {
        self.max_retries = n;
        self
    }

    /// The credential headers for `base`. The configured key is the
    /// caller's; the environment's token and workspace ID go only to a
    /// trusted origin (the default endpoint, `ANTHROPIC_BASE_URL`, llm.yaml
    /// `base_url`, `LLM_BASE_URL`, loopback). None goes over plain http
    /// to a remote host. Every credential passes through here.
    fn credentials(
        &self,
        base: &str,
        url: &reqwest::Url,
    ) -> Result<Vec<(&'static str, String)>, LlmError> {
        let key = self.cfg.provider.api_key.clone();
        crate::llm::check_endpoint_url(SCHEME, &key, base, url)?;
        let mut out = vec![("x-api-key", key)];
        let mut env_creds = Vec::new();
        if let Some(t) = self
            .ambient("ANTHROPIC_AUTH_TOKEN")
            .filter(|t| !t.trim().is_empty())
        {
            crate::llm::check_endpoint_url(SCHEME, &t, base, url)?;
            env_creds.push(("authorization", format!("Bearer {t}")));
        }
        if let Some(w) = self
            .ambient("ANTHROPIC_WORKSPACE_ID")
            .filter(|w| !w.is_empty())
        {
            env_creds.push(("anthropic-workspace-id", w));
        }
        // Without an environment credential there is nothing to trust:
        // llm.yaml and LLM_BASE_URL are not read.
        if env_creds.is_empty() {
            return Ok(out);
        }
        let mut trusted = vec![DEFAULT_BASE_URL.to_string()];
        trusted.extend(self.ambient("ANTHROPIC_BASE_URL"));
        trusted.extend(self.ambient("LLM_BASE_URL"));
        let file = match &self.file {
            Some(f) => f.clone(),
            None => crate::llm::ConfigFile::load(),
        };
        if let Some((_, b)) = file.block(&[SCHEME]) {
            trusted.push(b.base_url.clone());
        }
        if crate::llm::credentials_trusted(url, &trusted) {
            out.extend(env_creds);
        }
        Ok(out)
    }

    pub fn build(self) -> Result<Adapter, LlmError> {
        let p = &self.cfg.provider;
        if p.api_key.trim().is_empty() {
            return Err(LlmError::invalid("anthropic: API key required"));
        }
        let (what, base) = if !p.base_url.is_empty() {
            ("base URL", p.base_url.clone())
        } else if let Some(v) = self.ambient("ANTHROPIC_BASE_URL").filter(|v| !v.is_empty()) {
            ("ANTHROPIC_BASE_URL", v)
        } else {
            ("base URL", DEFAULT_BASE_URL.to_string())
        };
        let url = format!("{}/v1/messages", base.trim_end_matches('/'));
        let url = reqwest::Url::parse(&url).map_err(|e| {
            LlmError::invalid(format!(
                "anthropic: invalid {what}: parse {:?}: {e}",
                crate::llm::redact_uri(&base)
            ))
        })?;
        let tool_names = ToolNameMode::from_params(&p.params)
            .map_err(|e| LlmError::invalid(format!("anthropic: {e}")))?;
        let credentials = self.credentials(&base, &url)?;
        let limits = match self.limits {
            Some(l) => l,
            None => Limits::from_params(SCHEME, &p.params)?,
        };
        // Redirects are not followed: a credential never moves to a host
        // the server names. Proxies come from the environment only.
        let client = match self.client {
            Some(c) => c,
            None => GuardedClient::build(
                self.cfg.net_policy,
                reqwest::Client::builder().redirect(reqwest::redirect::Policy::none()),
            )
            .map_err(|e| LlmError::invalid(format!("anthropic: http client: {e}")))?,
        };
        let secrets: Vec<String> = credentials
            .iter()
            .map(|(_, v)| v.strip_prefix("Bearer ").unwrap_or(v).to_string())
            .filter(|v| !v.trim().is_empty())
            .collect();
        let scrub: stream::Scrub = std::sync::Arc::new(move |text: &str| {
            secrets
                .iter()
                .fold(text.to_string(), |t, s| t.replace(s.as_str(), "REDACTED"))
        });
        Ok(Adapter {
            destination: crate::netpolicy::destination(&url),
            url,
            client,
            credentials,
            model: p.model.clone(),
            max_retries: self.max_retries,
            sleeper: self.sleeper,
            limits,
            scrub,
            tool_names,
        })
    }
}

impl Adapter {
    /// An adapter reading `ANTHROPIC_*` from the process environment.
    pub fn new(cfg: ResolvedConfig) -> Result<Self, LlmError> {
        Self::builder(cfg).build()
    }

    pub fn builder(cfg: ResolvedConfig) -> Builder {
        Builder {
            cfg,
            env: Box::new(crate::llm::process_env),
            client: None,
            sleeper: Some(tokio_sleeper()),
            max_retries: DEFAULT_MAX_RETRIES,
            file: None,
            limits: None,
        }
    }

    /// The names of the credential headers this adapter sends, never
    /// their values.
    pub fn credential_headers(&self) -> Vec<&'static str> {
        self.credentials.iter().map(|(name, _)| *name).collect()
    }

    fn model_for<'a>(&'a self, req: &'a Request) -> &'a str {
        if req.model.is_empty() {
            &self.model
        } else {
            &req.model
        }
    }

    /// Checks the request's tool names against the API's rule and maps
    /// them, per the `tool_names` param; see [`ToolNames::new`].
    fn tool_names(&self, tools: Option<&[ToolDef]>) -> Result<ToolNames, LlmError> {
        ToolNames::new(
            ANTHROPIC_TOOL_NAMES,
            self.tool_names,
            tools.unwrap_or_default(),
        )
    }

    async fn message(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<wire::Parsed, LlmError> {
        let names = self.tool_names(tools)?;
        let body = wire::body(&self.model, req, tools, false, &names)?;
        let resp = self.send(&body, false, self.model_for(req)).await?;
        let text = self.read_body(resp).await?;
        let v: serde_json::Value = serde_json::from_slice(&text)
            .map_err(|e| LlmError::Protocol(format!("anthropic: decode response: {e}")))?;
        let mut parsed = wire::parse_message(&v);
        names.restore_calls(&mut parsed.tool_calls);
        Ok(parsed)
    }

    async fn open_stream(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<Box<dyn TokenStream>, LlmError> {
        let names = self.tool_names(tools)?;
        let body = wire::body(&self.model, req, tools, true, &names)?;
        let model = self.model_for(req).to_string();
        let resp = self.send(&body, true, &model).await?;
        let destination = self.destination.clone();
        let transport = move |e: reqwest::Error| {
            let e = e.without_url();
            LlmError::Transport {
                message: format!("{SCHEME}: POST {destination}: {e}"),
            }
        };
        Ok(Box::new(stream::AnthropicStream::new(
            resp,
            model,
            Box::new(transport),
            self.limits.max_event_bytes,
            self.scrub.clone(),
            names,
            req,
        )))
    }
}

impl Provider for Adapter {
    fn capabilities(&self) -> &'static [Capability] {
        &[
            Capability::Complete,
            Capability::Stream,
            Capability::ToolCall,
            Capability::StreamTools,
        ]
    }

    fn complete<'a>(&'a self, req: &'a Request) -> BoxFuture<'a, Result<Response, LlmError>> {
        Box::pin(async move { Ok(self.message(req, None).await?.response()) })
    }

    fn stream<'a>(
        &'a self,
        req: &'a Request,
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(self.open_stream(req, None))
    }

    fn call_with_tools<'a>(
        &'a self,
        req: &'a Request,
        tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<ToolResponse, LlmError>> {
        Box::pin(async move { Ok(self.message(req, Some(tools)).await?.tool_response()) })
    }

    fn stream_with_tools<'a>(
        &'a self,
        req: &'a Request,
        tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(self.open_stream(req, Some(tools)))
    }
}

/// Register the `anthropic` scheme on `registry`. Its key is
/// `ANTHROPIC_API_KEY` by the `<SCHEME>_API_KEY` convention.
pub fn register(registry: &Registry) -> Result<(), LlmError> {
    registry.register(
        SCHEME,
        Arc::new(|cfg| Ok(Box::new(Adapter::new(cfg)?) as Box<dyn Provider>)),
        Declaration {
            endpoints: vec![DEFAULT_BASE_URL.to_string()],
            ..Declaration::default()
        },
    )
}
