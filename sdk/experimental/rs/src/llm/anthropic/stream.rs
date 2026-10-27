//! The SSE token stream and the accumulator folding its events into a
//! message, rule for rule the Go SDK's `Message.Accumulate`. Pinned by
//! `contracts/llm-v1` (`anthropic_stream`, `anthropic_accumulate`).

use serde_json::Value;

use crate::llm::{
    BoxFuture, LlmError, StreamOutcome, ThinkingBlock, Token, TokenStream, ToolCall, Usage,
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

    pub fn outcome(&self) -> StreamOutcome {
        let mut out = StreamOutcome {
            finish_reason: self.stop_reason.clone(),
            usage: Usage::with_cache(
                self.input_tokens,
                self.output_tokens,
                self.cache_read_tokens,
                self.cache_creation_tokens,
            ),
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
                "tool_use" => out.tool_calls.push(ToolCall {
                    id: b.id.clone(),
                    name: b.name.clone(),
                    arguments: b.input.clone(),
                    ..ToolCall::default()
                }),
                _ => {}
            }
        }
        out
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
        }
    }

    fn done(&mut self) -> Result<Option<Token>, LlmError> {
        self.finished = true;
        self.outcome = Some(self.acc.outcome());
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
            Ok(None) => None,
        }
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
