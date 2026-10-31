//! What the HTTP adapters share: SSE decoding, size limits, the retry
//! timer, and one guarded POST with retries and error mapping. An
//! adapter supplies its URL, credential headers and a status mapper;
//! this module owns the loop. Feature `llm-http`, native only.
//!
//! Retries follow the OpenAI and Anthropic SDKs: more attempts on 408,
//! 409, 429, 5xx and transport failures, `x-should-retry` overriding the
//! status; the wait is `retry-after-ms` / `retry-after` when it asks for
//! (0, 60s], else `util::retry` backoff (500ms base, 8s cap, full
//! jitter). An offline refusal is never retried.

pub(crate) mod sse;

use std::collections::BTreeMap;
use std::sync::Arc;
use std::time::Duration;

use serde_json::Value;

use super::{BoxFuture, LlmError};
use crate::netpolicy::{GuardedClient, NetError};
use crate::util::retry::{RetryConfig, SplitMix64};

/// Waits the given delay; resolves `false` when cancelled.
pub type Sleeper = Arc<dyn Fn(Duration) -> BoxFuture<'static, bool> + Send + Sync>;

/// Removes credential values from text bound for an error.
pub(crate) type Scrub = Arc<dyn Fn(&str) -> String + Send + Sync>;

/// Maps a non-2xx response (status, wait the server asked for, body
/// text) to an error.
pub(crate) type StatusMapper<'a> = &'a (dyn Fn(u16, Option<Duration>, &str) -> LlmError + Sync);

/// Bounds on what a response may hold, to bound memory. Provider params
/// `max_body_bytes` and `max_event_bytes` set them from a URI or config;
/// an adapter builder's `limits` outranks both.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Limits {
    /// A whole non-streamed body (and an error body, read no further).
    pub max_body_bytes: u64,
    /// One SSE event of a stream.
    pub max_event_bytes: u64,
}

impl Default for Limits {
    fn default() -> Self {
        Limits {
            max_body_bytes: 32 << 20,
            max_event_bytes: sse::DEFAULT_MAX_EVENT,
        }
    }
}

impl Limits {
    /// The limits `params` set; a value that is not a positive byte
    /// count fails, naming `provider` and the param.
    pub fn from_params(
        provider: &str,
        params: &BTreeMap<String, String>,
    ) -> Result<Self, LlmError> {
        let mut l = Limits::default();
        for (name, dst) in [
            ("max_body_bytes", &mut l.max_body_bytes),
            ("max_event_bytes", &mut l.max_event_bytes),
        ] {
            if let Some(v) = params.get(name) {
                *dst = v.trim().parse().ok().filter(|n| *n > 0).ok_or_else(|| {
                    LlmError::invalid(format!("{provider}: {name} must be a positive byte count"))
                })?;
            }
        }
        Ok(l)
    }
}

/// The default retry timer: tokio's, which reqwest already runs on.
pub(crate) fn tokio_sleeper() -> Sleeper {
    Arc::new(|d| {
        Box::pin(async move {
            tokio::time::sleep(d).await;
            true
        })
    })
}

/// A scrubber replacing every non-blank `secrets` value with `REDACTED`.
pub(crate) fn scrubber(secrets: Vec<String>) -> Scrub {
    let secrets: Vec<String> = secrets
        .into_iter()
        .filter(|s| !s.trim().is_empty())
        .collect();
    Arc::new(move |text: &str| {
        secrets
            .iter()
            .fold(text.to_string(), |t, s| t.replace(s.as_str(), "REDACTED"))
    })
}

/// The sending half of an adapter.
pub(crate) struct Http {
    /// Names the adapter in errors (the scheme).
    pub provider: String,
    pub client: GuardedClient,
    /// The scheme://host[:port] errors name, never userinfo or query.
    pub destination: String,
    pub max_retries: u32,
    pub sleeper: Option<Sleeper>,
    pub limits: Limits,
    pub scrub: Scrub,
}

const MESSAGE_CAP: usize = 512;

impl Http {
    /// POST `body` as JSON to `url` with `headers`, retrying as the
    /// module docs say. Returns a 2xx response with its body unread.
    pub(crate) async fn post(
        &self,
        url: &reqwest::Url,
        headers: &[(String, String)],
        body: &Value,
        stream: bool,
        map: StatusMapper<'_>,
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
                .post(url.clone())
                .header("content-type", "application/json");
            if stream {
                rb = rb.header("accept", "text/event-stream");
            }
            for (name, value) in headers {
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
                    (map(status, after, &text), retry, after)
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
                    provider: self.provider.clone(),
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
        transport_error(&self.provider, &self.destination, e)
    }
}

/// See [`Http::transport_error`].
pub(crate) fn transport_error(provider: &str, destination: &str, e: reqwest::Error) -> LlmError {
    let e = e.without_url();
    let mut message = format!("{provider}: POST {destination}: {e}");
    let mut src = std::error::Error::source(&e);
    while let Some(s) = src {
        message.push_str(&format!(": {s}"));
        src = s.source();
    }
    LlmError::Transport { message }
}

fn should_retry(status: u16, headers: &reqwest::header::HeaderMap) -> bool {
    match headers.get("x-should-retry").and_then(|v| v.to_str().ok()) {
        Some("true") => true,
        Some("false") => false,
        _ => matches!(status, 408 | 409 | 429) || status >= 500,
    }
}

/// The longest wait a server may ask for, as the SDKs cap it.
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

/// `s` cut to a message-sized prefix.
pub(crate) fn cap(s: &str) -> String {
    match s.char_indices().nth(MESSAGE_CAP) {
        Some((i, _)) => format!("{}...", &s[..i]),
        None => s.to_string(),
    }
}

/// The redirect refusal every adapter reports the same way.
pub(crate) fn redirect(provider: &str, status: u16) -> LlmError {
    LlmError::HttpStatus {
        status,
        message: format!("{provider}: redirect not followed"),
    }
}
