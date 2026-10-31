//! The Ollama chat adapter (Go `ai/llm/ollama`), over `/api/chat`
//! through the guarded `api` client: no SDK crate. Registers the
//! `ollama` scheme; supports complete and stream.
//!
//! A local runtime: the key is optional (`OLLAMA_API_KEY`, sent as
//! `Authorization: Bearer` when not blank). The model is required and
//! the base URL defaults to `http://localhost:11434`; a host-form URI
//! (`ollama://host:port/model`) stays at the server's root. Like Go, the
//! adapter does not retry, sends `temperature` only (as `options`) and
//! reads no environment variable. Redirects are not followed, so a key
//! never moves to a host the server names. Not ported: Go's image
//! generation (`/api/generate`). Native only.

mod stream;
#[cfg(test)]
mod tests;
mod wire;

use std::sync::Arc;

use crate::llm::wirehttp::{scrubber, Http, Limits};
use crate::llm::{
    BoxFuture, Capability, Declaration, LlmError, Provider, ProviderKey, Registry, Request,
    ResolvedConfig, Response, TokenStream,
};
use crate::netpolicy::GuardedClient;

pub const SCHEME: &str = "ollama";
pub const DEFAULT_BASE_URL: &str = "http://localhost:11434";
const CHAT_ENDPOINT: &str = "/api/chat";

/// The adapter. `Debug` never prints the key.
pub struct Adapter {
    http: Http,
    url: reqwest::Url,
    model: String,
    credentials: Vec<(String, String)>,
}

impl std::fmt::Debug for Adapter {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Adapter")
            .field("url", &self.http.destination)
            .field("model", &self.model)
            .field("has_key", &!self.credentials.is_empty())
            .finish_non_exhaustive()
    }
}

/// Configures an [`Adapter`] beyond its [`ResolvedConfig`].
pub struct Builder {
    cfg: ResolvedConfig,
    client: Option<GuardedClient>,
}

impl Builder {
    /// Send through `client` (timeouts, proxies). Its policy applies,
    /// not the config's.
    pub fn client(mut self, client: GuardedClient) -> Self {
        self.client = Some(client);
        self
    }

    pub fn build(self) -> Result<Adapter, LlmError> {
        let p = &self.cfg.provider;
        let base = if p.base_url.is_empty() {
            DEFAULT_BASE_URL
        } else {
            &p.base_url
        };
        if p.model.is_empty() {
            return Err(LlmError::invalid("ollama: model is required"));
        }
        let url = format!("{}{CHAT_ENDPOINT}", base.trim_end_matches('/'));
        let url = reqwest::Url::parse(&url).map_err(|e| {
            LlmError::invalid(format!(
                "ollama: invalid base URL: parse {:?}: {e}",
                crate::llm::redact_uri(base)
            ))
        })?;
        let limits = Limits::from_params(SCHEME, &p.params)?;
        let mut credentials = Vec::new();
        let mut secrets = Vec::new();
        if !p.api_key.trim().is_empty() {
            credentials.push(("authorization".to_string(), format!("Bearer {}", p.api_key)));
            secrets.push(p.api_key.clone());
        }
        let client = match self.client {
            Some(c) => c,
            None => GuardedClient::build(
                self.cfg.net_policy,
                reqwest::Client::builder().redirect(reqwest::redirect::Policy::none()),
            )
            .map_err(|e| LlmError::invalid(format!("ollama: http client: {e}")))?,
        };
        Ok(Adapter {
            http: Http {
                provider: SCHEME.into(),
                client,
                destination: crate::netpolicy::destination(&url),
                max_retries: 0,
                sleeper: None,
                limits,
                scrub: scrubber(secrets),
            },
            url,
            model: p.model.clone(),
            credentials,
        })
    }
}

impl Adapter {
    pub fn new(cfg: ResolvedConfig) -> Result<Self, LlmError> {
        Self::builder(cfg).build()
    }

    pub fn builder(cfg: ResolvedConfig) -> Builder {
        Builder { cfg, client: None }
    }

    /// The chat URL requests go to, credentials masked.
    pub fn endpoint(&self) -> String {
        crate::llm::redact_uri(self.url.as_str())
    }

    fn model_for<'a>(&'a self, req: &'a Request) -> &'a str {
        if req.model.is_empty() {
            &self.model
        } else {
            &req.model
        }
    }

    async fn send(&self, req: &Request, stream: bool) -> Result<reqwest::Response, LlmError> {
        let body = wire::body(&self.model, req, stream)?;
        let model = self.model_for(req).to_string();
        let map = |status, _after, text: &str| map_status(status, text, &model);
        self.http
            .post(&self.url, &self.credentials, &body, false, &map)
            .await
    }
}

/// Map an error response as Go's `checkStatus`: a body naming a missing
/// model is a model error whatever the status.
fn map_status(status: u16, text: &str, model: &str) -> LlmError {
    let msg = serde_json::from_str::<serde_json::Value>(text)
        .ok()
        .and_then(|v| v["error"].as_str().map(str::to_string))
        .unwrap_or_default();
    let lower = msg.to_lowercase();
    if lower.contains("model") && (lower.contains("not found") || lower.contains("does not exist"))
    {
        return LlmError::Model {
            model: model.into(),
            provider: SCHEME.into(),
        };
    }
    let status_text = status.to_string();
    match status {
        401 => LlmError::Auth {
            provider: SCHEME.into(),
            message: status_text,
        },
        403 => LlmError::PermissionDenied {
            provider: SCHEME.into(),
            message: status_text,
        },
        404 => LlmError::Model {
            model: model.into(),
            provider: SCHEME.into(),
        },
        429 => LlmError::RateLimit {
            provider: SCHEME.into(),
            retry_after: None,
        },
        s if s >= 500 => LlmError::HttpStatus {
            status: s,
            message: status_text,
        },
        s => LlmError::Api {
            provider: SCHEME.into(),
            status: s,
            error_type: "unexpected status".into(),
            message: crate::llm::wirehttp::cap(text),
        },
    }
}

impl Provider for Adapter {
    fn capabilities(&self) -> &'static [Capability] {
        &[Capability::Complete, Capability::Stream]
    }

    fn complete<'a>(&'a self, req: &'a Request) -> BoxFuture<'a, Result<Response, LlmError>> {
        Box::pin(async move {
            let resp = self.send(req, false).await?;
            let text = self.http.read_body(resp).await?;
            let v: serde_json::Value = serde_json::from_slice(&text)
                .map_err(|e| LlmError::Protocol(format!("ollama: decode response: {e}")))?;
            Ok(wire::parse_chat(&v).response())
        })
    }

    fn stream<'a>(
        &'a self,
        req: &'a Request,
    ) -> BoxFuture<'a, Result<Box<dyn TokenStream>, LlmError>> {
        Box::pin(async move {
            let resp = self.send(req, true).await?;
            let destination = self.http.destination.clone();
            let transport = move |e: reqwest::Error| {
                crate::llm::wirehttp::transport_error(SCHEME, &destination, e)
            };
            Ok(Box::new(stream::ChatStream::new(
                resp,
                self.http.limits.max_event_bytes,
                Box::new(transport),
            )) as Box<dyn TokenStream>)
        })
    }
}

/// Register the `ollama` scheme on `registry`.
pub fn register(registry: &Registry) -> Result<(), LlmError> {
    registry.register(
        SCHEME,
        Arc::new(|cfg| Ok(Box::new(Adapter::new(cfg)?) as Box<dyn Provider>)),
        Declaration {
            key: Some(ProviderKey {
                env_vars: vec!["OLLAMA_API_KEY".into()],
                optional: true,
            }),
            ..Declaration::default()
        },
    )
}
