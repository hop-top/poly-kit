//! The [`Client`] facade (Go `llm.Client`): a primary provider, a
//! fallback chain walked on fallbackable errors, and lifecycle events
//! on a [`DynBus`]. Events are best effort: a publish failure never
//! affects the call.

use std::time::Instant;

/// When a call started; `None` on wasm32-unknown-unknown, which has no
/// clock (durations report 0 there).
type Start = Option<Instant>;

fn now() -> Start {
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    return Some(Instant::now());
    #[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
    return None;
}

use serde_json::{json, Value};

use crate::bus::topic::validate_topic;
use crate::bus::{DynBus, Event};

use super::error::LlmError;
use super::provider::{BoxFuture, Capability, Provider, TokenStream};
use super::types::{Request, Response, ToolDef, ToolResponse};

const SOURCE: &str = "kit.ai.client";

/// Every topic the client publishes on (Go `Topics`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Topics {
    pub request_start: String,
    pub request_end: String,
    pub request_error: String,
    pub fallback: String,
    pub route: String,
    pub eva_result: String,
}

impl Default for Topics {
    fn default() -> Self {
        Topics {
            request_start: "kit.ai.request.started".into(),
            request_end: "kit.ai.response.received".into(),
            request_error: "kit.ai.request.errored".into(),
            fallback: "kit.ai.fallback.applied".into(),
            route: "kit.ai.route.selected".into(),
            eva_result: "kit.ai.eva.evaluated".into(),
        }
    }
}

impl Topics {
    /// The default topics under `prefix` (`source.category`), keeping
    /// each `object.action` (Go `WithTopicPrefix`).
    pub fn with_prefix(prefix: &str) -> Result<Self, LlmError> {
        if prefix.is_empty() {
            return Err(LlmError::invalid("llm: topic prefix is empty"));
        }
        if prefix.ends_with('.') {
            return Err(LlmError::invalid(format!(
                "llm: topic prefix {prefix:?} must not end with '.'"
            )));
        }
        let n = prefix.split('.').count();
        if n != 2 {
            return Err(LlmError::invalid(format!(
                "llm: topic prefix {prefix:?} has {n} segments; expected 2 (source.category)"
            )));
        }
        let d = Topics::default();
        let re = |t: &str| {
            let suffix: Vec<&str> = t.rsplitn(3, '.').take(2).collect();
            format!("{prefix}.{}.{}", suffix[1], suffix[0])
        };
        let t = Topics {
            request_start: re(&d.request_start),
            request_end: re(&d.request_end),
            request_error: re(&d.request_error),
            fallback: re(&d.fallback),
            route: re(&d.route),
            eva_result: re(&d.eva_result),
        };
        t.validate()
            .map_err(|e| LlmError::invalid(format!("llm: topic prefix {prefix:?}: {e}")))?;
        Ok(t)
    }

    fn validate(&self) -> Result<(), String> {
        for t in [
            &self.request_start,
            &self.request_end,
            &self.request_error,
            &self.fallback,
            &self.route,
            &self.eva_result,
        ] {
            validate_topic(&t.as_str().into())?;
        }
        Ok(())
    }
}

/// A primary provider with fallbacks and bus events.
pub struct Client {
    chain: Vec<Box<dyn Provider>>,
    bus: Option<DynBus>,
    topics: Topics,
}

impl Client {
    pub fn new(primary: Box<dyn Provider>) -> Self {
        Client {
            chain: vec![primary],
            bus: None,
            topics: Topics::default(),
        }
    }

    /// Append a provider tried when every earlier one failed with a
    /// fallbackable error.
    pub fn with_fallback(mut self, p: Box<dyn Provider>) -> Self {
        self.chain.push(p);
        self
    }

    /// Publish lifecycle events on `bus`.
    pub fn with_bus(mut self, bus: DynBus) -> Self {
        self.bus = Some(bus);
        self
    }

    /// Override topics; each is validated.
    pub fn with_topics(mut self, topics: Topics) -> Result<Self, LlmError> {
        topics
            .validate()
            .map_err(|e| LlmError::invalid(format!("llm: topics: {e}")))?;
        self.topics = topics;
        Ok(self)
    }

    pub fn topics(&self) -> &Topics {
        &self.topics
    }

    /// The primary provider.
    pub fn provider(&self) -> &dyn Provider {
        self.chain[0].as_ref()
    }

    /// The primary provider's capabilities.
    pub fn capabilities(&self) -> &'static [Capability] {
        self.chain[0].capabilities()
    }

    fn publish(&self, topic: &str, payload: Value) {
        if let Some(bus) = &self.bus {
            let _ = bus.publish(&event(topic, payload));
        }
    }

    /// Walk the chain: skip providers lacking `cap`, return the first
    /// success, stop on a non-fallbackable error.
    async fn run<'a, T>(
        &'a self,
        req: &'a Request,
        cap: Capability,
        call: impl Fn(&'a dyn Provider) -> BoxFuture<'a, Result<T, LlmError>>,
        on_ok: impl Fn(&T, Start) -> Option<Value>,
    ) -> Result<T, LlmError> {
        self.publish(&self.topics.request_start, json!({ "request": req }));
        let mut errs = Vec::new();
        let last = self.chain.len() - 1;
        for (i, p) in self.chain.iter().enumerate() {
            if !p.capabilities().contains(&cap) {
                errs.push(LlmError::CapabilityNotSupported {
                    capability: cap.as_str().to_string(),
                    provider: format!("provider[{i}]"),
                });
                continue;
            }
            let start = now();
            match call(p.as_ref()).await {
                Ok(v) => {
                    if let Some(payload) = on_ok(&v, start) {
                        self.publish(&self.topics.request_end, payload);
                    }
                    return Ok(v);
                }
                Err(e) if !e.is_fallbackable() => {
                    self.publish(
                        &self.topics.request_error,
                        json!({ "error": e.to_string() }),
                    );
                    return Err(e);
                }
                Err(e) => {
                    if i < last {
                        self.publish(
                            &self.topics.fallback,
                            json!({ "from": i, "to": i + 1, "error": e.to_string() }),
                        );
                    }
                    errs.push(e);
                }
            }
        }
        if errs
            .iter()
            .all(|e| matches!(e, LlmError::CapabilityNotSupported { .. }))
        {
            return Err(errs.pop().expect("chain is never empty"));
        }
        let exhausted = LlmError::FallbackExhausted(errs);
        self.publish(
            &self.topics.request_error,
            json!({ "error": exhausted.to_string() }),
        );
        Err(exhausted)
    }

    pub async fn complete(&self, req: &Request) -> Result<Response, LlmError> {
        self.run(req, Capability::Complete, |p| p.complete(req), end_payload)
            .await
    }

    /// Fallback applies to opening the stream only; an error mid-stream
    /// is the caller's.
    pub async fn stream(&self, req: &Request) -> Result<Box<dyn TokenStream>, LlmError> {
        self.run(req, Capability::Stream, |p| p.stream(req), |_, _| None)
            .await
    }

    pub async fn call_with_tools(
        &self,
        req: &Request,
        tools: &[ToolDef],
    ) -> Result<ToolResponse, LlmError> {
        self.run(
            req,
            Capability::ToolCall,
            |p| p.call_with_tools(req, tools),
            |r: &ToolResponse, start| {
                end_payload(
                    &Response {
                        content: r.content.clone(),
                        ..Response::default()
                    },
                    start,
                )
            },
        )
        .await
    }

    pub async fn stream_with_tools(
        &self,
        req: &Request,
        tools: &[ToolDef],
    ) -> Result<Box<dyn TokenStream>, LlmError> {
        self.run(
            req,
            Capability::StreamTools,
            |p| p.stream_with_tools(req, tools),
            |_, _| None,
        )
        .await
    }
}

/// An event stamped now. wasm32-unknown-unknown has no clock: there it
/// carries the Unix epoch and no id.
fn event(topic: &str, payload: Value) -> Event {
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    return Event::new(topic, SOURCE, payload);
    #[cfg(all(target_arch = "wasm32", target_os = "unknown"))]
    return Event::new_at(topic, SOURCE, payload, std::time::UNIX_EPOCH);
}

fn end_payload(resp: &Response, start: Start) -> Option<Value> {
    Some(json!({
        "response": resp,
        "duration_ms": start.map_or(0, |s| s.elapsed().as_millis() as u64),
    }))
}
