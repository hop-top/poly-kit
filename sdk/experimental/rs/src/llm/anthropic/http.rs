//! Sending a Messages API request: headers, retries and error mapping.
//!
//! Retries follow the Anthropic SDKs (Go's adapter gets them from the
//! Go SDK): up to two more attempts on 408, 409, 429, 5xx and transport
//! failures, `x-should-retry` overriding the status; the wait is
//! `retry-after-ms` / `retry-after` when sent, else `util::retry`
//! backoff (500ms base, 8s cap, full jitter). An offline refusal is
//! never retried. Pinned by `contracts/llm-v1` (`anthropic_error`).

use std::time::Duration;

use serde_json::Value;

use crate::llm::LlmError;
use crate::netpolicy::NetError;
use crate::util::retry::{RetryConfig, SplitMix64};

use super::{Adapter, API_VERSION, SCHEME};

const MESSAGE_CAP: usize = 512;

impl Adapter {
    /// POST `body`, retrying as the module docs say. Returns a 2xx
    /// response with its body unread.
    pub(crate) async fn send(
        &self,
        body: &Value,
        stream: bool,
        model: &str,
    ) -> Result<reqwest::Response, LlmError> {
        let policy = RetryConfig {
            max_attempts: self.max_retries + 1,
            base_delay: Duration::from_millis(500),
            max_delay: Duration::from_secs(8),
            jitter: true,
        };
        let mut rng = SplitMix64::from_entropy();
        for attempt in 0u32.. {
            let mut rb = self
                .client
                .post(self.url.clone())
                .header("anthropic-version", API_VERSION)
                .header("content-type", "application/json");
            if stream {
                rb = rb.header("accept", "text/event-stream");
            }
            for (name, value) in &self.credentials {
                rb = rb.header(name, value);
            }
            let (err, retry, after) = match rb.json(body).send().await {
                Err(NetError::Offline(o)) => return Err(o.into()),
                Err(NetError::Transport(e)) => (self.transport_error(e), true, None),
                Ok(resp) if resp.status().is_success() => return Ok(resp),
                Ok(resp) => {
                    let status = resp.status().as_u16();
                    let retry = should_retry(status, resp.headers());
                    let after = retry_after(resp.headers());
                    // An error body is read no further than the limit, and
                    // never quoted with a credential in it.
                    let text = self.read_body(resp).await.unwrap_or_default();
                    let text = (self.scrub)(&String::from_utf8_lossy(&text));
                    (map_status(status, after, &text, model), retry, after)
                }
            };
            let wait = match policy.next_delay(attempt, &mut rng) {
                Some(backoff) if retry => after.unwrap_or(backoff),
                _ => return Err(err),
            };
            let Some(sleep) = &self.sleeper else {
                return Err(err);
            };
            if !sleep(wait).await {
                return Err(LlmError::Cancelled);
            }
        }
        unreachable!("attempt counter exhausted")
    }

    /// The body, refused past `limits.max_body_bytes`.
    pub(crate) async fn read_body(&self, mut resp: reqwest::Response) -> Result<Vec<u8>, LlmError> {
        let limit = self.limits.max_body_bytes;
        let mut out = Vec::new();
        while let Some(chunk) = resp.chunk().await.map_err(|e| self.transport_error(e))? {
            if (out.len() + chunk.len()) as u64 > limit {
                return Err(LlmError::ResponseTooLarge {
                    provider: SCHEME.into(),
                    what: "body",
                    limit,
                });
            }
            out.extend_from_slice(&chunk);
        }
        Ok(out)
    }

    /// A transport failure named by method and stripped destination:
    /// never the base URL's userinfo or query.
    pub(crate) fn transport_error(&self, e: reqwest::Error) -> LlmError {
        let e = e.without_url();
        let mut message = format!("{SCHEME}: POST {}: {e}", self.destination);
        let mut src = std::error::Error::source(&e);
        while let Some(s) = src {
            message.push_str(&format!(": {s}"));
            src = s.source();
        }
        LlmError::Transport { message }
    }
}

fn should_retry(status: u16, headers: &reqwest::header::HeaderMap) -> bool {
    match headers.get("x-should-retry").and_then(|v| v.to_str().ok()) {
        Some("true") => true,
        Some("false") => false,
        _ => matches!(status, 408 | 409 | 429) || status >= 500,
    }
}

/// The longest wait a server may ask for, as the Anthropic SDKs cap it.
const MAX_RETRY_AFTER: Duration = Duration::from_secs(60);

/// `retry-after-ms`, else `retry-after` in seconds, when it asks for a
/// wait in (0, 60s]. Anything else (zero, negative, NaN, infinite, huge,
/// unparsable, an HTTP date) is `None`: the caller backs off instead, so
/// a hostile or broken server cannot stall the client.
fn retry_after(headers: &reqwest::header::HeaderMap) -> Option<Duration> {
    let secs = |name: &str, per_sec: f64| -> Option<Duration> {
        let v: f64 = headers.get(name)?.to_str().ok()?.trim().parse().ok()?;
        let s = v / per_sec;
        (s > 0.0 && s <= MAX_RETRY_AFTER.as_secs_f64()).then(|| Duration::from_secs_f64(s))
    };
    secs("retry-after-ms", 1000.0).or_else(|| secs("retry-after", 1.0))
}

fn cap(s: &str) -> String {
    match s.char_indices().nth(MESSAGE_CAP) {
        Some((i, _)) => format!("{}...", &s[..i]),
        None => s.to_string(),
    }
}

/// `{"type":"error","error":{"type":..,"message":..}}`, or the raw text.
fn error_body(text: &str) -> (String, String) {
    match serde_json::from_str::<Value>(text) {
        Ok(v) if v["error"].is_object() => (
            v["error"]["type"].as_str().unwrap_or_default().to_string(),
            cap(v["error"]["message"].as_str().unwrap_or_default()),
        ),
        _ => (String::new(), cap(text.trim())),
    }
}

/// Map an error response to the llm taxonomy, as Go's `mapError`.
pub(crate) fn map_status(
    status: u16,
    retry_after: Option<Duration>,
    text: &str,
    model: &str,
) -> LlmError {
    let (error_type, message) = error_body(text);
    classify(status, retry_after, error_type, message, model)
}

fn classify(
    status: u16,
    retry_after: Option<Duration>,
    error_type: String,
    message: String,
    model: &str,
) -> LlmError {
    match status {
        401 | 403 => LlmError::Auth {
            provider: SCHEME.into(),
            message: format!("{error_type}: {message}"),
        },
        300..=399 => LlmError::HttpStatus {
            status,
            message: format!("{SCHEME}: redirect not followed"),
        },
        429 => LlmError::RateLimit {
            provider: SCHEME.into(),
            retry_after,
        },
        404 => LlmError::Model {
            model: model.into(),
            provider: SCHEME.into(),
        },
        s if s >= 500 => LlmError::HttpStatus {
            status: s,
            message: format!("{SCHEME}: {error_type}: {message}"),
        },
        s => LlmError::Api {
            provider: SCHEME.into(),
            status: s,
            error_type,
            message,
        },
    }
}

/// Map an SSE `error` event by its error type, as the status that type
/// stands for: `overloaded_error` is a 529, `rate_limit_error` a 429.
/// Go returns these unclassified (not fallbackable).
pub(crate) fn map_event_error(data: &str, model: &str) -> LlmError {
    let (error_type, message) = error_body(data);
    let status = match error_type.as_str() {
        "invalid_request_error" => 400,
        "authentication_error" => 401,
        "billing_error" => 402,
        "permission_error" => 403,
        "not_found_error" => 404,
        "request_too_large" => 413,
        "rate_limit_error" => 429,
        "api_error" => 500,
        "timeout_error" => 504,
        "overloaded_error" => 529,
        _ => 0,
    };
    classify(status, None, error_type, message, model)
}
