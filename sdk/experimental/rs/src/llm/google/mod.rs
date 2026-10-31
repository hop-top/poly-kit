//! The Google Gemini adapter (Go `ai/llm/google`), over the
//! `generateContent` API through the guarded `api` client: no SDK crate.
//! Registers the `gemini` and `google` schemes; supports complete,
//! stream, tool calls and streaming with tools. Errors name the
//! provider `gemini`.
//!
//! The key goes in the `x-goog-api-key` header, never the URL. It comes
//! from the resolved config; a blank one falls back, unless hermetic,
//! to `GOOGLE_API_KEY`, then `GEMINI_API_KEY`, then `LLM_API_KEY` (blank
//! values skipped), the order Google's SDK reads them, so
//! [`Builder::env`] can stand in for the process environment. No key is
//! an error at build. The model is required; the base URL defaults to
//! `https://generativelanguage.googleapis.com/v1beta`. Like Go, the
//! adapter does not retry; redirects are not followed (Go follows
//! them). A stream line is bounded at 1 MiB (`max_event_bytes`).
//!
//! Tool names follow [`GEMINI_TOOL_NAMES`]. Gemini 3's thought
//! signatures ride in `ToolCall::provider_data["google"]` and are echoed
//! on the matching `functionCall` part; a current-turn call without one
//! is sent with the documented skip value. Native only.

mod errors;
mod stream;
#[cfg(test)]
mod tests;
mod wire;

use std::sync::Arc;

use crate::llm::wirehttp::{scrubber, Http, Limits};
use crate::llm::{
    BoxFuture, Capability, Declaration, LlmError, Provider, ProviderKey, Registry, Request,
    ResolvedConfig, Response, TokenStream, ToolDef, ToolNameMode, ToolNames, ToolResponse,
    GEMINI_TOOL_NAMES,
};
use crate::netpolicy::GuardedClient;

pub const SCHEMES: &[&str] = &["gemini", "google"];
pub const DEFAULT_BASE_URL: &str = "https://generativelanguage.googleapis.com/v1beta";
/// One stream line holds at most this much unless `max_event_bytes`
/// says otherwise.
const DEFAULT_MAX_LINE: u64 = 1 << 20;

type EnvFn = Box<dyn Fn(&str) -> Option<String> + Send + Sync>;

/// The adapter. `Debug` never prints the key.
pub struct Adapter {
    http: Http,
    base_url: String,
    model: String,
    key: String,
    tool_names: ToolNameMode,
}

impl std::fmt::Debug for Adapter {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Adapter")
            .field("base_url", &crate::llm::redact_uri(&self.base_url))
            .field("model", &self.model)
            .finish_non_exhaustive()
    }
}

/// Configures an [`Adapter`] beyond its [`ResolvedConfig`].
pub struct Builder {
    cfg: ResolvedConfig,
    env: EnvFn,
    client: Option<GuardedClient>,
}

impl Builder {
    /// Read no key from the environment: only the config's.
    pub fn hermetic(mut self) -> Self {
        self.cfg.hermetic = true;
        self
    }

    /// Read the key variables from `env` instead of the process.
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

    pub fn build(self) -> Result<Adapter, LlmError> {
        let p = &self.cfg.provider;
        let base = if p.base_url.is_empty() {
            DEFAULT_BASE_URL.to_string()
        } else {
            p.base_url.trim_end_matches('/').to_string()
        };
        if p.model.is_empty() {
            return Err(LlmError::invalid("gemini: model is required"));
        }
        let mut key = p.api_key.clone();
        if key.trim().is_empty() && !self.cfg.hermetic {
            key = ["GOOGLE_API_KEY", "GEMINI_API_KEY", "LLM_API_KEY"]
                .iter()
                .filter_map(|n| (self.env)(n))
                .find(|v| !v.trim().is_empty())
                .unwrap_or_default();
        }
        if key.trim().is_empty() {
            return Err(LlmError::invalid(
                "gemini: API key is required (set via config, URI param, GOOGLE_API_KEY, GEMINI_API_KEY, or LLM_API_KEY)",
            ));
        }
        let tool_names = ToolNameMode::from_params(&p.params)
            .map_err(|e| LlmError::invalid(format!("gemini: {e}")))?;
        let probe = reqwest::Url::parse(&format!("{base}/models/{}:generateContent", p.model))
            .map_err(|e| {
                LlmError::invalid(format!(
                    "gemini: invalid base URL: parse {:?}: {e}",
                    crate::llm::redact_uri(&base)
                ))
            })?;
        let mut limits = Limits::from_params("gemini", &p.params)?;
        if !p.params.contains_key("max_event_bytes") {
            limits.max_event_bytes = DEFAULT_MAX_LINE;
        }
        let client = match self.client {
            Some(c) => c,
            None => GuardedClient::build(
                self.cfg.net_policy,
                reqwest::Client::builder().redirect(reqwest::redirect::Policy::none()),
            )
            .map_err(|e| LlmError::invalid(format!("gemini: http client: {e}")))?,
        };
        Ok(Adapter {
            http: Http {
                provider: "gemini".into(),
                client,
                destination: crate::netpolicy::destination(&probe),
                max_retries: 0,
                sleeper: None,
                limits,
                scrub: scrubber(vec![key.clone()]),
            },
            base_url: base,
            model: p.model.clone(),
            key,
            tool_names,
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
        }
    }

    fn model_for<'a>(&'a self, req: &'a Request) -> &'a str {
        if req.model.is_empty() {
            &self.model
        } else {
            &req.model
        }
    }

    fn url(&self, model: &str, method: &str) -> Result<reqwest::Url, LlmError> {
        reqwest::Url::parse(&format!("{}/models/{model}:{method}", self.base_url)).map_err(|e| {
            LlmError::invalid(format!(
                "gemini: invalid base URL: parse {:?}: {e}",
                crate::llm::redact_uri(&self.base_url)
            ))
        })
    }

    fn prepare(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<(serde_json::Value, ToolNames), LlmError> {
        let tools = tools.unwrap_or_default();
        let names = ToolNames::new(GEMINI_TOOL_NAMES, self.tool_names, tools)?;
        let body = wire::body(req, tools, &names)?;
        Ok((body, names))
    }

    async fn send(
        &self,
        req: &Request,
        body: &serde_json::Value,
        method: &str,
        stream: bool,
    ) -> Result<reqwest::Response, LlmError> {
        let model = self.model_for(req);
        let url = self.url(model, method)?;
        let headers = [("x-goog-api-key".to_string(), self.key.clone())];
        let map = |status, _after, text: &str| errors::map_status(status, text, model);
        self.http.post(&url, &headers, body, stream, &map).await
    }

    async fn generate(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
    ) -> Result<wire::Parsed, LlmError> {
        let (body, names) = self.prepare(req, tools)?;
        let resp = self.send(req, &body, "generateContent", false).await?;
        let bytes = self.http.read_body(resp).await?;
        let v: serde_json::Value = serde_json::from_slice(&bytes)
            .map_err(|e| LlmError::Protocol(format!("gemini: decode response: {e}")))?;
        if v["error"].is_object() {
            return Err(errors::map_api_error(&v["error"], self.model_for(req)));
        }
        Ok(wire::parse(&v, &names))
    }

    async fn open_stream(
        &self,
        req: &Request,
        tools: Option<&[ToolDef]>,
        with_tools: bool,
    ) -> Result<Box<dyn TokenStream>, LlmError> {
        let (body, names) = self.prepare(req, tools)?;
        let resp = self
            .send(req, &body, "streamGenerateContent?alt=sse", true)
            .await?;
        let destination = self.http.destination.clone();
        let parts = stream::StreamParts {
            model: self.model_for(req).to_string(),
            max_event: self.http.limits.max_event_bytes,
            transport: Box::new(move |e| {
                crate::llm::wirehttp::transport_error("gemini", &destination, e)
            }),
            scrub: self.http.scrub.clone(),
        };
        Ok(if with_tools || req.stream_events {
            Box::new(stream::ToolStream::new(
                resp,
                parts,
                names,
                req.stream_events,
            ))
        } else {
            Box::new(stream::PlainStream::new(resp, parts))
        })
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
        Box::pin(async move { Ok(self.generate(req, None).await?.response()) })
    }

    fn stream<'a>(
        &'a self,
        req: &'a Request,
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(self.open_stream(req, None, false))
    }

    fn call_with_tools<'a>(
        &'a self,
        req: &'a Request,
        tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<ToolResponse, LlmError>> {
        Box::pin(async move { Ok(self.generate(req, Some(tools)).await?.tool_response()) })
    }

    fn stream_with_tools<'a>(
        &'a self,
        req: &'a Request,
        tools: &'a [ToolDef],
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(self.open_stream(req, Some(tools), true))
    }
}

/// Register `gemini` and `google` on `registry`. The key is
/// `GOOGLE_API_KEY`, then `GEMINI_API_KEY`.
pub fn register(registry: &Registry) -> Result<(), LlmError> {
    for s in SCHEMES {
        registry.register(
            s,
            Arc::new(|cfg| Ok(Box::new(Adapter::new(cfg)?) as Box<dyn Provider>)),
            Declaration {
                key: Some(ProviderKey {
                    env_vars: vec!["GOOGLE_API_KEY".into(), "GEMINI_API_KEY".into()],
                    optional: false,
                }),
                endpoints: vec![DEFAULT_BASE_URL.to_string()],
                ..Declaration::default()
            },
        )?;
    }
    Ok(())
}
