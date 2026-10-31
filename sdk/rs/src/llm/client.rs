//! The [`Client`] facade (Go `llm.Client`): a primary provider, a
//! fallback chain walked on fallbackable errors, and lifecycle events
//! on a [`DynBus`]. Events are best effort: a publish failure never
//! affects the call.
//!
//! The request and response events carry no content
//! ([`RequestStartPayload`], [`RequestEndPayload`]) unless the client
//! opts in with [`Client::with_request_content`] /
//! [`Client::with_response_content`].

use std::sync::Arc;
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
use super::events::{RequestEndPayload, RequestStartPayload};
use super::provider::{BoxFuture, Capability, Provider, TokenStream};
use super::types::{Request, Response, Role, ToolCall, ToolDef, ToolResponse};

/// A request transform for [`Client::with_request_content`].
type RequestContent = Arc<dyn Fn(Request) -> Request + Send + Sync>;
/// A response transform for [`Client::with_response_content`].
type ResponseContent = Arc<dyn Fn(Response) -> Response + Send + Sync>;

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
    request_content: Option<RequestContent>,
    response_content: Option<ResponseContent>,
}

impl Client {
    pub fn new(primary: Box<dyn Provider>) -> Self {
        Client {
            chain: vec![primary],
            bus: None,
            topics: Topics::default(),
            request_content: None,
            response_content: None,
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

    /// Put request content on the bus (Go `WithRequestContent`): the
    /// `kit.ai.request.started` payload's `request` is `f` applied to a
    /// copy of the request, so `f` may rewrite it freely and the
    /// caller's request is not touched. Pass `|r| r` to publish the
    /// content as is. Without this the payload is metadata only
    /// ([`RequestStartPayload`]). With feature `redact`,
    /// [`redact_request`](super::redact_request) builds an `f` that
    /// masks secrets.
    ///
    /// The content then reaches every subscriber, including sinks that
    /// forward or store events; publish it only to a bus you control.
    pub fn with_request_content(
        mut self,
        f: impl Fn(Request) -> Request + Send + Sync + 'static,
    ) -> Self {
        self.request_content = Some(Arc::new(f));
        self
    }

    /// [`Client::with_request_content`] for the
    /// `kit.ai.response.received` payload's `response`; with feature
    /// `redact`, [`redact_response`](super::redact_response) builds a
    /// masking `f`.
    pub fn with_response_content(
        mut self,
        f: impl Fn(Response) -> Response + Send + Sync + 'static,
    ) -> Self {
        self.response_content = Some(Arc::new(f));
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

    /// Publish `kit.ai.request.started` for a call of kind `cap` offering
    /// `tools` tools.
    fn started(&self, cap: Capability, req: &Request, tools: usize) {
        if self.bus.is_none() {
            return;
        }
        let mut p = RequestStartPayload::new(cap.as_str(), req, tools);
        if let Some(f) = &self.request_content {
            p.request = Some(f(req.clone()));
        }
        self.publish(&self.topics.request_start, to_value(&p));
    }

    /// Publish `kit.ai.response.received`.
    fn ended(&self, cap: Capability, resp: Response, calls: &[ToolCall], start: Start) {
        if self.bus.is_none() {
            return;
        }
        let ms = start.map_or(0, |s| s.elapsed().as_millis() as u64);
        let mut p = RequestEndPayload::new(cap.as_str(), &resp, calls, ms);
        if let Some(f) = &self.response_content {
            p.response = Some(f(resp));
        }
        self.publish(&self.topics.request_end, to_value(&p));
    }

    /// Walk the chain: skip providers lacking `cap`, return the first
    /// success, stop on a non-fallbackable error. `on_ok` publishes the
    /// end event, if the call has one.
    async fn run<'a, T>(
        &'a self,
        req: &'a Request,
        cap: Capability,
        tools: usize,
        call: impl Fn(&'a dyn Provider) -> BoxFuture<'a, Result<T, LlmError>>,
        on_ok: impl Fn(&T, Start),
    ) -> Result<T, LlmError> {
        self.started(cap, req, tools);
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
                    on_ok(&v, start);
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
        let cap = Capability::Complete;
        self.run(
            req,
            cap,
            0,
            |p| p.complete(req),
            |r: &Response, start| self.ended(cap, r.clone(), &[], start),
        )
        .await
    }

    /// Fallback applies to opening the stream only; an error mid-stream
    /// is the caller's. A stream publishes no `kit.ai.response.received`.
    pub async fn stream(&self, req: &Request) -> Result<Box<dyn TokenStream>, LlmError> {
        self.run(req, Capability::Stream, 0, |p| p.stream(req), |_, _| {})
            .await
    }

    pub async fn call_with_tools(
        &self,
        req: &Request,
        tools: &[ToolDef],
    ) -> Result<ToolResponse, LlmError> {
        let cap = Capability::ToolCall;
        self.run(
            req,
            cap,
            tools.len(),
            |p| p.call_with_tools(req, tools),
            |r: &ToolResponse, start| {
                let resp = Response {
                    content: r.content.clone(),
                    role: Role::Assistant,
                    usage: r.usage,
                    finish_reason: r.finish_reason.clone(),
                    thinking: r.thinking.clone(),
                };
                self.ended(cap, resp, &r.tool_calls, start)
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
            tools.len(),
            |p| p.stream_with_tools(req, tools),
            |_, _| {},
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

/// A payload as JSON. Payloads hold strings, numbers and maps with
/// string keys only, so this cannot fail.
fn to_value(p: &impl serde::Serialize) -> Value {
    serde_json::to_value(p).expect("payload serializes")
}
