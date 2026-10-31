//! The NVIDIA Triton Inference Server client (Go `ai/llm/triton`),
//! registered as the `triton` scheme. Go's adapter speaks the KServe v2
//! inference protocol over HTTP (REST), not gRPC, and so does this one:
//! no protobuf, no gRPC crate.
//!
//! The [`Scorer`] is the abstraction: a float32 input vector in, a scalar
//! score out (the routing classifiers' seam). [`Client`] is also a
//! [`Provider`] with no capabilities, as in Go, so `triton://model`
//! resolves through the registry. `TRITON_API_KEY` is optional and sent
//! as `Authorization: Bearer` when not blank (the gateway in front of
//! Triton checks it; Triton itself takes none). Not retried; redirects
//! are not followed. Native only.

use std::sync::Arc;

use serde_json::{json, Value};

use crate::llm::wirehttp::{cap, scrubber, Http, Limits};
use crate::llm::{
    BoxFuture, Capability, Declaration, LlmError, Provider, ProviderKey, Registry, ResolvedConfig,
};
use crate::netpolicy::GuardedClient;

pub const SCHEME: &str = "triton";
pub const DEFAULT_BASE_URL: &str = "http://localhost:8000";

/// Scores a float32 input vector.
pub trait Scorer: Send + Sync {
    fn score<'a>(&'a self, input: &'a [f32]) -> BoxFuture<'a, Result<f64, LlmError>>;
}

/// Triton's HTTP inference API (KServe v2). `Debug` never prints the key.
pub struct Client {
    http: Http,
    base_url: String,
    model: String,
    credentials: Vec<(String, String)>,
}

impl std::fmt::Debug for Client {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Client")
            .field("base_url", &self.http.destination)
            .field("model", &self.model)
            .field("has_key", &!self.credentials.is_empty())
            .finish_non_exhaustive()
    }
}

impl Client {
    /// A client for `triton://[host:port/]model_name`; the base URL
    /// defaults to `http://localhost:8000`.
    pub fn new(cfg: ResolvedConfig) -> Result<Self, LlmError> {
        Self::with_client(cfg, None)
    }

    /// [`Client::new`] sending through `client` (timeouts, proxies).
    pub fn with_client(cfg: ResolvedConfig, client: Option<GuardedClient>) -> Result<Self, LlmError> {
        let p = &cfg.provider;
        let base = if p.base_url.is_empty() {
            DEFAULT_BASE_URL.to_string()
        } else {
            p.base_url.trim_end_matches('/').to_string()
        };
        let model = if p.model.is_empty() {
            cfg.uri.model.clone()
        } else {
            p.model.clone()
        };
        if model.is_empty() {
            return Err(LlmError::invalid("triton: model name is required"));
        }
        let url = reqwest::Url::parse(&format!("{base}/v2/models/{model}/infer")).map_err(|e| {
            LlmError::invalid(format!(
                "triton: invalid base URL: parse {:?}: {e}",
                crate::llm::redact_uri(&base)
            ))
        })?;
        let mut credentials = Vec::new();
        let mut secrets = Vec::new();
        if !p.api_key.trim().is_empty() {
            credentials.push(("authorization".to_string(), format!("Bearer {}", p.api_key)));
            secrets.push(p.api_key.clone());
        }
        let client = match client {
            Some(c) => c,
            None => GuardedClient::build(
                cfg.net_policy,
                reqwest::Client::builder().redirect(reqwest::redirect::Policy::none()),
            )
            .map_err(|e| LlmError::invalid(format!("triton: http client: {e}")))?,
        };
        let http = Http {
            provider: SCHEME.into(),
            client,
            destination: crate::netpolicy::destination(&url),
            max_retries: 0,
            sleeper: None,
            limits: Limits::from_params(SCHEME, &p.params)?,
            scrub: scrubber(secrets),
        };
        Ok(Client {
            http,
            base_url: base,
            model,
            credentials,
        })
    }

    /// The configured model name.
    pub fn model_name(&self) -> &str {
        &self.model
    }

    /// The configured base URL, credentials masked.
    pub fn base_url(&self) -> String {
        crate::llm::redact_uri(&self.base_url)
    }

    fn url(&self) -> reqwest::Url {
        // Parsed at construction.
        reqwest::Url::parse(&format!("{}/v2/models/{}/infer", self.base_url, self.model))
            .expect("checked at construction")
    }

    async fn infer(&self, input: &[f32]) -> Result<f64, LlmError> {
        let mut data = Vec::with_capacity(input.len());
        for x in input {
            // The shortest f32 text as f64, so 1.1 stays 1.1.
            let n = x
                .to_string()
                .parse::<f64>()
                .ok()
                .and_then(serde_json::Number::from_f64)
                .ok_or_else(|| LlmError::invalid("triton: marshal request: non-finite input"))?;
            data.push(Value::Number(n));
        }
        let body = json!({
            "inputs": [{
                "name": "input",
                "shape": [1, input.len()],
                "datatype": "FP32",
                "data": [data],
            }],
        });
        let map = |status: u16, _after, text: &str| {
            let message = format!("triton: HTTP {status}: {}", cap(text));
            if status >= 500 {
                LlmError::HttpStatus { status, message }
            } else {
                LlmError::Api {
                    provider: SCHEME.into(),
                    status,
                    error_type: String::new(),
                    message,
                }
            }
        };
        let resp = self
            .http
            .post(&self.url(), &self.credentials, &body, false, &map)
            .await?;
        let bytes = self.http.read_body(resp).await?;
        let v: Value = serde_json::from_slice(&bytes)
            .map_err(|e| LlmError::Protocol(format!("triton: decode response: {e}")))?;
        let out = v["outputs"]
            .as_array()
            .and_then(|o| o.first())
            .ok_or_else(|| LlmError::Protocol("triton: no outputs in response".into()))?;
        out["data"]
            .as_array()
            .and_then(|d| d.first())
            .and_then(Value::as_f64)
            .ok_or_else(|| LlmError::Protocol("triton: empty output data".into()))
    }
}

impl Scorer for Client {
    fn score<'a>(&'a self, input: &'a [f32]) -> BoxFuture<'a, Result<f64, LlmError>> {
        Box::pin(self.infer(input))
    }
}

/// Registered as a provider with no capabilities, as in Go: it scores,
/// it does not complete.
impl Provider for Client {
    fn capabilities(&self) -> &'static [Capability] {
        &[]
    }
}

/// Register the `triton` scheme on `registry`. The aim catalog has no
/// triton entry; the key is optional.
pub fn register(registry: &Registry) -> Result<(), LlmError> {
    registry.register(
        SCHEME,
        Arc::new(|cfg| Ok(Box::new(Client::new(cfg)?) as Box<dyn Provider>)),
        Declaration {
            key: Some(ProviderKey {
                env_vars: vec!["TRITON_API_KEY".into()],
                optional: true,
            }),
            ..Declaration::default()
        },
    )
}
