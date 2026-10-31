//! The OpenAI Chat Completions adapter (Go `ai/llm/openai`), over the
//! HTTP API through the guarded `api` client: no SDK crate.
//!
//! Registers the schemes `openai`, `openrouter`, `xai`, `lmstudio`,
//! `groq`, `together`, `fireworks`, `deepseek` and `mistral`. `lmstudio`
//! defaults to LM Studio's local server, `http://localhost:1234/v1`, and
//! appends `/v1` to any base URL that lacks it. An `openai` host-form
//! URI (`openai://host:port/model`) names a self-hosted
//! OpenAI-compatible server and reaches it at `http://host:port/v1`; an
//! `openai` `base_url` is used as given. Supports complete, stream, tool
//! calls and streaming with tools. The key comes from the resolved
//! config only; a blank key sends no `authorization` header. For the
//! `openai` scheme, `OPENAI_ORG_ID` and `OPENAI_PROJECT_ID` become the
//! `OpenAI-Organization` and `OpenAI-Project` headers, as the Go SDK
//! does; [`Builder::hermetic`] (or [`ResolvedConfig::hermetic`]) reads
//! neither. Native only.
//!
//! Tool names follow [`OPENAI_TOOL_NAMES`]: sanitized on the way out and
//! restored on returned calls, per the `tool_names` provider param
//! ([`ToolNameMode`]); see [`ToolNames`]. Not ported: Go's image
//! generation, transcription and speech calls (the [`Provider`] seam has
//! no such capabilities), and aim-catalog protocol claims.

mod errors;
mod stream;
#[cfg(test)]
mod tests;
mod wire;

use std::sync::Arc;

use crate::llm::wirehttp::{scrubber, tokio_sleeper, Http};
use crate::llm::{
    BoxFuture, Capability, Declaration, Limits, LlmError, Provider, ProviderKey, Registry, Request,
    ResolvedConfig, Response, Sleeper, TokenStream, ToolDef, ToolNameMode, ToolNames, ToolResponse,
    OPENAI_TOOL_NAMES,
};
use crate::netpolicy::GuardedClient;

pub const SCHEME: &str = "openai";
pub const DEFAULT_BASE_URL: &str = "https://api.openai.com/v1";
/// Retries after the first attempt, as the OpenAI SDKs default.
pub const DEFAULT_MAX_RETRIES: u32 = 2;

/// Every scheme this adapter registers.
pub const SCHEMES: &[&str] = &[
    "openai",
    "openrouter",
    "xai",
    "lmstudio",
    "groq",
    "together",
    "fireworks",
    "deepseek",
    "mistral",
];

/// A scheme's default API base URL, declared at registration. Schemes
/// not listed fall back to OpenAI's. Kit keeps its own even where a
/// catalog has one: a scheme must reach its host with nothing cached.
pub fn default_base_url(scheme: &str) -> Option<&'static str> {
    Some(match scheme {
        "openrouter" => "https://openrouter.ai/api/v1",
        "xai" => "https://api.x.ai/v1",
        "groq" => "https://api.groq.com/openai/v1",
        "together" => "https://api.together.xyz/v1",
        "fireworks" => "https://api.fireworks.ai/inference/v1",
        "deepseek" => "https://api.deepseek.com",
        "mistral" => "https://api.mistral.ai/v1",
        // LM Studio's local server; name another host or port with a
        // host-form URI or ?base_url=.
        "lmstudio" => "http://localhost:1234/v1",
        _ => return None,
    })
}

/// The adapter. `Debug` never prints the key.
pub struct Adapter {
    http: Http,
    url: reqwest::Url,
    scheme: String,
    model: String,
    /// Every header the adapter adds: `authorization` (none for a blank
    /// key) and the organization and project IDs.
    credentials: Vec<(String, String)>,
    /// The `tool_names` param: how names the API rejects are handled.
    tool_names: ToolNameMode,
}

impl std::fmt::Debug for Adapter {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Adapter")
            .field("scheme", &self.scheme)
            .field("url", &self.http.destination)
            .field("model", &self.model)
            .field("credentials", &self.credential_headers())
            .field("max_retries", &self.http.max_retries)
            .finish_non_exhaustive()
    }
}

type EnvFn = Box<dyn Fn(&str) -> Option<String> + Send + Sync>;

/// Configures an [`Adapter`] beyond its [`ResolvedConfig`].
pub struct Builder {
    cfg: ResolvedConfig,
    env: EnvFn,
    client: Option<GuardedClient>,
    sleeper: Option<Sleeper>,
    max_retries: u32,
    limits: Option<Limits>,
    file: Option<crate::llm::ConfigFile>,
}

impl Builder {
    /// The llm.yaml whose `base_url` counts as a trusted endpoint for
    /// the organization and project headers; the default reads it from
    /// disk when it is needed.
    pub fn config_file(mut self, file: crate::llm::ConfigFile) -> Self {
        self.file = Some(file);
        self
    }

    /// Read nothing ambient (Go `ResolvedConfig.Hermetic`): no
    /// `OPENAI_ORG_ID` or `OPENAI_PROJECT_ID`, from the process or from
    /// [`Builder::env`]. Same as [`ResolvedConfig::hermetic`]. Proxy
    /// variables stay the HTTP client's, as with Go's `net/http`.
    pub fn hermetic(mut self) -> Self {
        self.cfg.hermetic = true;
        self
    }

    /// Read `OPENAI_ORG_ID` and `OPENAI_PROJECT_ID` from `env` instead
    /// of the process environment.
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

    pub fn build(self) -> Result<Adapter, LlmError> {
        let cfg = &self.cfg;
        let p = &cfg.provider;
        let scheme = if cfg.uri.scheme.is_empty() {
            SCHEME.to_string()
        } else {
            cfg.uri.scheme.clone()
        };
        let limits = match self.limits {
            Some(l) => l,
            None => Limits::from_params(&scheme, &p.params)?,
        };
        let mut base = p.base_url.clone();
        if base.is_empty() {
            base = default_base_url(&scheme)
                .unwrap_or(DEFAULT_BASE_URL)
                .to_string();
        }
        if scheme == "lmstudio" || scheme == "openai" && is_host_form(cfg) {
            base = api_root_v1(&base);
        }
        let url = format!("{}/chat/completions", base.trim_end_matches('/'));
        let url = reqwest::Url::parse(&url).map_err(|e| {
            LlmError::invalid(format!(
                "openai: invalid base URL: parse {:?}: {e}",
                crate::llm::redact_uri(&base)
            ))
        })?;
        let model = if p.model.is_empty() {
            cfg.uri.model.clone()
        } else {
            p.model.clone()
        };
        let tool_names = ToolNameMode::from_params(&p.params)
            .map_err(|e| LlmError::invalid(format!("openai: {e}")))?;
        // A blank key is no key: no authorization header, rather than
        // "Bearer " with nothing after it.
        let mut credentials = Vec::new();
        let mut secrets = Vec::new();
        if !p.api_key.trim().is_empty() {
            credentials.push(("authorization".to_string(), format!("Bearer {}", p.api_key)));
            secrets.push(p.api_key.clone());
        }
        // No key goes over plain http to a remote host or to a malformed
        // endpoint.
        crate::llm::check_endpoint_url(&scheme, &p.api_key, &base, &url)?;
        let ids_trusted = || {
            let file = match &self.file {
                Some(f) => f.clone(),
                None => crate::llm::ConfigFile::load(),
            };
            crate::llm::wirehttp::env_key_trusted(
                &url,
                DEFAULT_BASE_URL,
                (self.env)("LLM_BASE_URL"),
                &file,
                &[SCHEME],
            )
        };
        if scheme == SCHEME && !cfg.hermetic && ids_trusted() {
            for (var, header) in [
                ("OPENAI_ORG_ID", "openai-organization"),
                ("OPENAI_PROJECT_ID", "openai-project"),
            ] {
                if let Some(v) = (self.env)(var).filter(|v| !v.is_empty()) {
                    credentials.push((header.to_string(), v));
                }
            }
        }
        // Redirects are not followed: a credential never moves to a host
        // the server names. Proxies come from the environment only.
        let client = match self.client {
            Some(c) => c,
            None => GuardedClient::build(
                cfg.net_policy.clone(),
                reqwest::Client::builder(),
                crate::netpolicy::Redirects::None,
                reqwest::header::HeaderMap::new(),
            )
            .map_err(|e| LlmError::invalid(format!("openai: http client: {e}")))?,
        };
        Ok(Adapter {
            http: Http {
                provider: scheme.clone(),
                client,
                destination: crate::netpolicy::destination(&url),
                max_retries: self.max_retries,
                sleeper: self.sleeper,
                limits,
                scrub: scrubber(secrets),
            },
            url,
            scheme,
            model,
            credentials,
            tool_names,
        })
    }
}

/// Whether `cfg`'s base URL is the one a host-form URI
/// (`scheme://host:port/model`) derives, `http://host:port`, rather than
/// one named outright by a `base_url` param (which outranks the host)
/// or, when the URI has no host, by `LLM_BASE_URL` or a config file.
fn is_host_form(cfg: &ResolvedConfig) -> bool {
    !cfg.uri.host.is_empty()
        && !cfg.uri.params.contains_key("base_url")
        && cfg.provider.base_url == format!("http://{}", cfg.uri.host)
}

/// `base` ending in `/v1`; one already ending in `/v1`, with or without
/// a trailing slash, is kept.
///
/// lmstudio applies it to every base URL: LM Studio serves its API under
/// `/v1` only. openai applies it to the host form only: a host form
/// cannot carry a path, and the servers it names serve the API under
/// `/v1`; a `base_url` is the API root as given, since gateways mount it
/// under other prefixes.
fn api_root_v1(base: &str) -> String {
    let trimmed = base.trim_end_matches('/');
    if trimmed.ends_with("/v1") {
        base.to_string()
    } else {
        format!("{trimmed}/v1")
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
            sleeper: Some(tokio_sleeper()),
            max_retries: DEFAULT_MAX_RETRIES,
            limits: None,
            file: None,
        }
    }

    /// The chat completions URL requests go to, credentials masked.
    pub fn endpoint(&self) -> String {
        crate::llm::redact_uri(self.url.as_str())
    }

    /// The names of the credential headers this adapter sends, never
    /// their values.
    pub fn credential_headers(&self) -> Vec<&str> {
        self.credentials.iter().map(|(n, _)| n.as_str()).collect()
    }

    fn model_for<'a>(&'a self, req: &'a Request) -> &'a str {
        if req.model.is_empty() {
            &self.model
        } else {
            &req.model
        }
    }

    fn tool_names(&self, tools: Option<&[ToolDef]>) -> Result<ToolNames, LlmError> {
        ToolNames::new(
            OPENAI_TOOL_NAMES,
            self.tool_names,
            tools.unwrap_or_default(),
        )
    }

    async fn send(
        &self,
        body: &serde_json::Value,
        stream: bool,
        model: &str,
    ) -> Result<reqwest::Response, LlmError> {
        let map = |status, after, text: &str| {
            errors::map_status(&self.scheme, status, after, text, model)
        };
        self.http
            .post(&self.url, &self.credentials, body, stream, &map)
            .await
    }

    async fn completion(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<wire::Parsed, LlmError> {
        let names = self.tool_names(tools)?;
        let body = wire::body(&self.scheme, &self.model, req, tools, false, &names)?;
        let resp = self.send(&body, false, self.model_for(req)).await?;
        let text = self.http.read_body(resp).await?;
        let v: serde_json::Value = serde_json::from_slice(&text)
            .map_err(|e| LlmError::Protocol(format!("openai: decode response: {e}")))?;
        let mut parsed = wire::parse_completion(&v);
        names.restore_calls(&mut parsed.tool_calls);
        Ok(parsed)
    }

    async fn open_stream(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<Box<dyn TokenStream>, LlmError> {
        let names = self.tool_names(tools)?;
        let body = wire::body(&self.scheme, &self.model, req, tools, true, &names)?;
        let model = self.model_for(req).to_string();
        let resp = self.send(&body, true, &model).await?;
        let (provider, destination) = (self.scheme.clone(), self.http.destination.clone());
        let transport = move |e: reqwest::Error| {
            crate::llm::wirehttp::transport_error(&provider, &destination, e)
        };
        Ok(Box::new(stream::ChatStream::new(
            resp,
            stream::StreamParts {
                scheme: self.scheme.clone(),
                model,
                names,
                events: req.stream_events,
                max_event: self.http.limits.max_event_bytes,
                scrub: self.http.scrub.clone(),
                transport: Box::new(transport),
            },
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
        Box::pin(async move { Ok(self.completion(req, None).await?.response()) })
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
        Box::pin(async move { Ok(self.completion(req, Some(tools)).await?.tool_response()) })
    }

    fn stream_with_tools<'a>(
        &'a self,
        req: &'a Request,
        tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(self.open_stream(req, Some(tools)))
    }
}

/// What the adapter declares about `scheme`.
fn declaration(scheme: &str) -> Declaration {
    let mut d = Declaration {
        base_url: default_base_url(scheme).unwrap_or_default().to_string(),
        ..Declaration::default()
    };
    match scheme {
        // OpenAI's own default may receive the scheme's key.
        "openai" => d.endpoints = vec![DEFAULT_BASE_URL.to_string()],
        // LM Studio's local server takes no key: none is sent even when
        // LMSTUDIO_API_KEY is set.
        "lmstudio" => {
            d.key = Some(ProviderKey {
                env_vars: Vec::new(),
                optional: true,
            })
        }
        _ => {}
    }
    d
}

/// Register every scheme of [`SCHEMES`] on `registry`.
pub fn register(registry: &Registry) -> Result<(), LlmError> {
    for s in SCHEMES {
        registry.register(
            s,
            Arc::new(|cfg| Ok(Box::new(Adapter::new(cfg)?) as Box<dyn Provider>)),
            declaration(s),
        )?;
    }
    Ok(())
}
