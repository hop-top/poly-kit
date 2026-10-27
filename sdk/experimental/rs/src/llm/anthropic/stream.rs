//! The SSE token stream and the accumulator folding its events into a
//! message, rule for rule the Go SDK's `Message.Accumulate`. Pinned by
//! `contracts/llm-v1` (`anthropic_stream`, `anthropic_accumulate`).

use std::collections::BTreeMap;

use serde_json::Value;

use crate::llm::{
    BoxFuture, LlmError, StreamOutcome, ThinkingBlock, Token, TokenStream, ToolCall, ToolCallEvent,
    ToolCallEventKind, ToolNames, Usage,
};

use super::http::map_event_error;
use super::sse::{SseDecoder, SseEvent};

#[derive(Debug, Default)]
struct Block {
    kind: String,
    text: String,
    thinking: String,
    signature: String,
    data: String,
    id: String,
    name: String,
    input: String,
}

/// A delta an event carried, for the token stream.
pub(crate) enum Delta {
    Text(String),
    Thinking(String),
}

/// Folds stream events into blocks, stop reason and usage.
#[derive(Debug, Default)]
pub(crate) struct Accumulator {
    blocks: Vec<Block>,
    stop_reason: String,
    input_tokens: u64,
    output_tokens: u64,
    cache_read_tokens: u64,
    cache_creation_tokens: u64,
}

fn protocol(msg: String) -> LlmError {
    LlmError::Protocol(msg)
}

impl Accumulator {
    /// Apply one event. Returns the text or thinking delta it carried.
    pub fn apply(&mut self, kind: &str, ev: &Value) -> Result<Option<Delta>, LlmError> {
        match kind {
            "message_start" => {
                let m = &ev["message"];
                *self = Accumulator {
                    stop_reason: m["stop_reason"].as_str().unwrap_or_default().into(),
                    input_tokens: m["usage"]["input_tokens"].as_u64().unwrap_or(0),
                    output_tokens: m["usage"]["output_tokens"].as_u64().unwrap_or(0),
                    cache_read_tokens: m["usage"]["cache_read_input_tokens"].as_u64().unwrap_or(0),
                    cache_creation_tokens: m["usage"]["cache_creation_input_tokens"]
                        .as_u64()
                        .unwrap_or(0),
                    ..Accumulator::default()
                };
            }
            "message_delta" => {
                // Cumulative totals: they overwrite, never add.
                self.stop_reason = ev["delta"]["stop_reason"]
                    .as_str()
                    .unwrap_or_default()
                    .into();
                self.output_tokens = ev["usage"]["output_tokens"].as_u64().unwrap_or(0);
                if let Some(n) = ev["usage"]["input_tokens"].as_u64() {
                    self.input_tokens = n;
                }
                if let Some(n) = ev["usage"]["cache_read_input_tokens"].as_u64() {
                    self.cache_read_tokens = n;
                }
                if let Some(n) = ev["usage"]["cache_creation_input_tokens"].as_u64() {
                    self.cache_creation_tokens = n;
                }
            }
            "content_block_start" => {
                let index = ev["index"].as_u64().unwrap_or(u64::MAX);
                if index != self.blocks.len() as u64 {
                    return Err(protocol(format!(
                        "received event of type {kind} for content block at index {index}, expected index {}",
                        self.blocks.len()
                    )));
                }
                let cb = &ev["content_block"];
                let s = |k: &str| cb[k].as_str().unwrap_or_default().to_string();
                let kind = s("type");
                let input = match cb.get("input") {
                    Some(v) => v.to_string(),
                    None if kind == "tool_use" => "{}".into(),
                    None => String::new(),
                };
                self.blocks.push(Block {
                    text: s("text"),
                    thinking: s("thinking"),
                    signature: s("signature"),
                    data: s("data"),
                    id: s("id"),
                    name: s("name"),
                    input,
                    kind,
                });
            }
            "content_block_delta" => {
                let index = ev["index"].as_u64().unwrap_or(u64::MAX);
                let len = self.blocks.len();
                let Some(b) = usize::try_from(index)
                    .ok()
                    .and_then(|i| self.blocks.get_mut(i))
                else {
                    return Err(protocol(format!(
                        "received event of type {kind} for content block at index {index}, have {len} blocks"
                    )));
                };
                let d = &ev["delta"];
                let field = |k: &str| d[k].as_str().unwrap_or_default();
                match d["type"].as_str().unwrap_or_default() {
                    "text_delta" => {
                        b.text.push_str(field("text"));
                        return Ok(Some(Delta::Text(field("text").to_string())));
                    }
                    "input_json_delta" => {
                        let part = field("partial_json");
                        if !part.is_empty() {
                            if b.input == "{}" {
                                b.input = part.to_string();
                            } else {
                                b.input.push_str(part);
                            }
                        }
                    }
                    "thinking_delta" => {
                        b.thinking.push_str(field("thinking"));
                        return Ok(Some(Delta::Thinking(field("thinking").to_string())));
                    }
                    "signature_delta" => b.signature.push_str(field("signature")),
                    _ => {} // citations_delta: not surfaced
                }
            }
            _ => {} // content_block_stop, message_stop, unknown
        }
        Ok(None)
    }

    /// The usage folded so far.
    pub fn usage(&self) -> Usage {
        Usage::with_cache(
            self.input_tokens,
            self.output_tokens,
            self.cache_read_tokens,
            self.cache_creation_tokens,
        )
    }

    /// The block at `index` when it is a tool call, as folded so far.
    fn tool_call(&self, index: u64) -> Option<ToolCall> {
        let b = self.blocks.get(usize::try_from(index).ok()?)?;
        (b.kind == "tool_use").then(|| call_of(b))
    }

    pub fn outcome(&self) -> StreamOutcome {
        let mut out = StreamOutcome {
            finish_reason: self.stop_reason.clone(),
            usage: self.usage(),
            ..StreamOutcome::default()
        };
        for b in &self.blocks {
            match b.kind.as_str() {
                "text" => out.content.push_str(&b.text),
                "thinking" => out.thinking.push(ThinkingBlock {
                    thinking: b.thinking.clone(),
                    signature: b.signature.clone(),
                    redacted: String::new(),
                }),
                "redacted_thinking" => out.thinking.push(ThinkingBlock {
                    redacted: b.data.clone(),
                    ..ThinkingBlock::default()
                }),
                "tool_use" => out.tool_calls.push(call_of(b)),
                _ => {}
            }
        }
        out
    }
}

fn call_of(b: &Block) -> ToolCall {
    ToolCall {
        id: b.id.clone(),
        name: b.name.clone(),
        arguments: b.input.clone(),
        ..ToolCall::default()
    }
}

/// Event names the stream decodes; others (`ping` included) are skipped.
const EVENTS: &[&str] = &[
    "message_start",
    "message_delta",
    "message_stop",
    "content_block_start",
    "content_block_delta",
    "content_block_stop",
];

pub(crate) struct AnthropicStream {
    resp: reqwest::Response,
    decoder: SseDecoder,
    acc: Accumulator,
    model: String,
    /// Set once `done` or an error was returned: `next` is `None` after.
    finished: bool,
    outcome: Option<StreamOutcome>,
    transport: Box<dyn Fn(reqwest::Error) -> LlmError + Send>,
    scrub: Scrub,
    /// Thinking deltas stream as tokens.
    thinking: bool,
    /// Maps the outcome's tool names back to the caller's.
    names: ToolNames,
    /// Tool-call events and usage snapshots stream as tokens
    /// ([`crate::llm::Request::stream_events`]).
    events: bool,
    /// A tool_use block's content index to its call index.
    calls: BTreeMap<u64, usize>,
}

/// Removes credential values from text bound for an error.
pub(crate) type Scrub = std::sync::Arc<dyn Fn(&str) -> String + Send + Sync>;

impl AnthropicStream {
    pub fn new(
        resp: reqwest::Response,
        model: String,
        transport: Box<dyn Fn(reqwest::Error) -> LlmError + Send>,
        max_event: u64,
        scrub: Scrub,
        thinking: bool,
        names: ToolNames,
        events: bool,
    ) -> Self {
        AnthropicStream {
            resp,
            decoder: SseDecoder::with_limit(max_event),
            acc: Accumulator::default(),
            model,
            finished: false,
            outcome: None,
            transport,
            scrub,
            thinking,
            names,
            events,
            calls: BTreeMap::new(),
        }
    }

    fn done(&mut self) -> Result<Option<Token>, LlmError> {
        self.finished = true;
        let mut out = self.acc.outcome();
        self.names.restore_calls(&mut out.tool_calls);
        self.outcome = Some(out);
        Ok(Some(Token {
            done: true,
            ..Token::default()
        }))
    }

    fn fail(&mut self, e: LlmError) -> Result<Option<Token>, LlmError> {
        self.finished = true;
        Err(e)
    }

    /// Handle one event: `Some` when it ends the call or yields a token.
    fn event(&mut self, ev: SseEvent) -> Option<Result<Option<Token>, LlmError>> {
        if ev.event == "error" {
            let e = map_event_error(&(self.scrub)(&ev.data), &self.model);
            return Some(self.fail(e));
        }
        if !EVENTS.contains(&ev.event.as_str()) {
            return None;
        }
        let v: Value = match serde_json::from_str(&ev.data) {
            Ok(v) => v,
            Err(e) => {
                let e = protocol(format!("anthropic: decode {} event: {e}", ev.event));
                return Some(self.fail(e));
            }
        };
        match self.acc.apply(&ev.event, &v) {
            Err(e) => Some(self.fail(e)),
            Ok(Some(Delta::Text(text))) => Some(Ok(Some(Token {
                content: text,
                ..Token::default()
            }))),
            Ok(Some(Delta::Thinking(t))) if self.thinking => Some(Ok(Some(Token {
                thinking: t,
                ..Token::default()
            }))),
            Ok(Some(Delta::Thinking(_))) => None,
            Ok(None) if ev.event == "message_stop" => Some(self.done()),
            Ok(None) => self.stream_event(&ev.event, &v).map(|t| Ok(Some(t))),
        }
    }

    /// The tool-call or usage token an applied event makes, when the
    /// request asked for them (Go `streamIter.event`).
    fn stream_event(&mut self, kind: &str, ev: &Value) -> Option<Token> {
        if !self.events {
            return None;
        }
        let index = ev["index"].as_u64().unwrap_or(u64::MAX);
        let tool_call = match kind {
            "message_start" | "message_delta" => {
                return Some(Token {
                    usage: Some(self.acc.usage()),
                    ..Token::default()
                });
            }
            "content_block_start" => {
                let call = self.acc.tool_call(index)?;
                let i = self.calls.len();
                self.calls.insert(index, i);
                ToolCallEvent {
                    kind: ToolCallEventKind::Start,
                    index: i,
                    id: call.id,
                    name: self.names.original(&call.name).to_string(),
                    arguments_delta: String::new(),
                    call: None,
                }
            }
            "content_block_delta" => {
                let i = *self.calls.get(&index)?;
                let d = &ev["delta"];
                let part = d["partial_json"].as_str().unwrap_or_default();
                if d["type"] != "input_json_delta" || part.is_empty() {
                    return None;
                }
                ToolCallEvent {
                    kind: ToolCallEventKind::Delta,
                    index: i,
                    id: String::new(),
                    name: String::new(),
                    arguments_delta: part.to_string(),
                    call: None,
                }
            }
            "content_block_stop" => {
                let i = *self.calls.get(&index)?;
                let mut call = self.acc.tool_call(index)?;
                call.name = self.names.original(&call.name).to_string();
                ToolCallEvent {
                    kind: ToolCallEventKind::End,
                    index: i,
                    id: call.id.clone(),
                    name: call.name.clone(),
                    arguments_delta: String::new(),
                    call: Some(call),
                }
            }
            _ => return None,
        };
        Some(Token {
            tool_call: Some(tool_call),
            ..Token::default()
        })
    }

    async fn next_token(&mut self) -> Result<Option<Token>, LlmError> {
        loop {
            if self.finished {
                return Ok(None);
            }
            while let Some(ev) = self.decoder.pop() {
                if let Some(r) = self.event(ev) {
                    return r;
                }
            }
            match self.resp.chunk().await {
                Ok(Some(bytes)) => {
                    if let Err(limit) = self.decoder.feed(&bytes) {
                        return self.fail(LlmError::ResponseTooLarge {
                            provider: super::SCHEME.into(),
                            what: "event",
                            limit,
                        });
                    }
                }
                // No message_stop: the response is incomplete.
                Ok(None) => {
                    return self.fail(LlmError::Truncated {
                        provider: super::SCHEME.into(),
                    })
                }
                Err(e) => {
                    let e = (self.transport)(e);
                    return self.fail(e);
                }
            }
        }
    }
}

impl TokenStream for AnthropicStream {
    fn next(&mut self) -> BoxFuture<'_, Result<Option<Token>, LlmError>> {
        Box::pin(self.next_token())
    }

    fn outcome(&self) -> Option<&StreamOutcome> {
        self.outcome.as_ref()
    }
}
