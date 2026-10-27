//! The Anthropic Messages API adapter (Go `ai/llm/anthropic`), over
//! the HTTP API through the guarded `api` client: no SDK crate.
//!
//! Registers the `anthropic` scheme. Supports complete, stream, tool
//! calls and streaming with tools. The key comes from the resolved
//! config only; `ANTHROPIC_BASE_URL` (below a configured base URL),
//! `ANTHROPIC_AUTH_TOKEN` (sent as a bearer token when not blank) and
//! `ANTHROPIC_WORKSPACE_ID` are read as in Go. Native only.

mod http;
mod sse;
mod stream;
mod wire;

use std::sync::Arc;
use std::time::Duration;

use crate::llm::{
    BoxFuture, Capability, Declaration, LlmError, Provider, Registry, Request, ResolvedConfig,
    Response, TokenStream, ToolDef, ToolResponse,
};
use crate::netpolicy::GuardedClient;

pub const SCHEME: &str = "anthropic";
pub const DEFAULT_BASE_URL: &str = "https://api.anthropic.com";
pub const API_VERSION: &str = "2023-06-01";
/// `max_tokens` when a request leaves it 0.
pub const DEFAULT_MAX_TOKENS: u32 = 1024;
/// Retries after the first attempt, as the Anthropic SDKs default.
pub const DEFAULT_MAX_RETRIES: u32 = 2;

/// Waits the given delay; resolves `false` when cancelled.
pub type Sleeper = Arc<dyn Fn(Duration) -> BoxFuture<'static, bool> + Send + Sync>;

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
}

/// The default retry timer: tokio's, which reqwest already runs on.
fn tokio_sleeper() -> Sleeper {
    Arc::new(|d| {
        Box::pin(async move {
            tokio::time::sleep(d).await;
            true
        })
    })
}

impl Builder {
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
        if let Some(t) = (self.env)("ANTHROPIC_AUTH_TOKEN").filter(|t| !t.trim().is_empty()) {
            crate::llm::check_endpoint_url(SCHEME, &t, base, url)?;
            env_creds.push(("authorization", format!("Bearer {t}")));
        }
        if let Some(w) = (self.env)("ANTHROPIC_WORKSPACE_ID").filter(|w| !w.is_empty()) {
            env_creds.push(("anthropic-workspace-id", w));
        }
        let mut trusted = vec![DEFAULT_BASE_URL.to_string()];
        trusted.extend((self.env)("ANTHROPIC_BASE_URL"));
        trusted.extend((self.env)("LLM_BASE_URL"));
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
        } else if let Some(v) = (self.env)("ANTHROPIC_BASE_URL").filter(|v| !v.is_empty()) {
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
        let credentials = self.credentials(&base, &url)?;
        let client = match self.client {
            Some(c) => c,
            None => GuardedClient::new(self.cfg.net_policy)
                .map_err(|e| LlmError::invalid(format!("anthropic: http client: {e}")))?,
        };
        Ok(Adapter {
            destination: crate::netpolicy::destination(&url),
            url,
            client,
            credentials,
            model: p.model.clone(),
            max_retries: self.max_retries,
            sleeper: self.sleeper,
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

    async fn message(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<wire::Parsed, LlmError> {
        let body = wire::body(&self.model, req, tools, false)?;
        let resp = self.send(&body, false, self.model_for(req)).await?;
        let text = resp.text().await.map_err(|e| self.transport_error(e))?;
        let v: serde_json::Value = serde_json::from_str(&text)
            .map_err(|e| LlmError::Protocol(format!("anthropic: decode response: {e}")))?;
        Ok(wire::parse_message(&v))
    }

    async fn open_stream(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<Box<dyn TokenStream>, LlmError> {
        let body = wire::body(&self.model, req, tools, true)?;
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
