//! The two token streams over `streamGenerateContent?alt=sse`:
//! [`PlainStream`] for a text stream (Go `streamIterator`) and
//! [`ToolStream`] for a tool-calling or event-emitting one (Go
//! `toolStream`). Pinned by `contracts/llm-v1` (`gemini_stream_events`).

use std::collections::VecDeque;

use serde_json::Value;

use crate::llm::wirehttp::sse::SseDecoder;
use crate::llm::{
    BoxFuture, LlmError, StreamOutcome, Token, TokenStream, ToolCall, ToolCallEvent,
    ToolCallEventKind, ToolNames, Usage,
};

use super::errors::map_api_error;
use super::wire::{call_of, content_text, map_finish_reason, usage_of};

type Transport = Box<dyn Fn(reqwest::Error) -> LlmError + Send>;

/// What both streams need besides the response.
pub(crate) struct StreamParts {
    pub model: String,
    pub max_event: u64,
    pub transport: Transport,
    /// Removes the key from text bound for an error.
    pub scrub: crate::llm::wirehttp::Scrub,
}

/// Pulls the next data payload: `None` once the body is over.
struct Source {
    resp: reqwest::Response,
    decoder: SseDecoder,
    transport: Transport,
    model: String,
    scrub: crate::llm::wirehttp::Scrub,
}

enum Next {
    Data(String),
    End,
    Failed(LlmError),
}

impl Source {
    fn new(resp: reqwest::Response, p: StreamParts) -> Self {
        Source {
            resp,
            decoder: SseDecoder::with_limit(p.max_event),
            transport: p.transport,
            model: p.model,
            scrub: p.scrub,
        }
    }

    async fn next(&mut self) -> Next {
        loop {
            while let Some(ev) = self.decoder.pop() {
                let data = ev.data.trim_end_matches('\n').to_string();
                if !data.is_empty() {
                    return Next::Data(data);
                }
            }
            match self.resp.chunk().await {
                Ok(Some(b)) => {
                    if let Err(limit) = self.decoder.feed(&b) {
                        return Next::Failed(LlmError::ResponseTooLarge {
                            provider: "gemini".into(),
                            what: "event",
                            limit,
                        });
                    }
                }
                Ok(None) => return Next::End,
                Err(e) => return Next::Failed((self.transport)(e)),
            }
        }
    }

    /// A chunk decoded, or why the stream failed.
    fn decode(&self, data: &str) -> Result<Value, LlmError> {
        let v: Value = serde_json::from_str(data)
            .map_err(|e| LlmError::Protocol(format!("gemini: decode stream chunk: {e}")))?;
        if v["error"].is_object() {
            // Scrub the raw payload first: truncating the message before
            // could cut a credential in two, and neither half would match.
            let scrubbed: Value = serde_json::from_str(&(self.scrub)(data)).unwrap_or(v);
            return Err(map_api_error(&scrubbed["error"], &self.model));
        }
        Ok(v)
    }
}

/// Text tokens; the stream ends on `STOP` or `MAX_TOKENS`, or when the
/// body does.
pub(crate) struct PlainStream {
    src: Source,
    done: bool,
}

impl PlainStream {
    pub(crate) fn new(resp: reqwest::Response, p: StreamParts) -> Self {
        PlainStream {
            src: Source::new(resp, p),
            done: false,
        }
    }

    async fn next_token(&mut self) -> Result<Option<Token>, LlmError> {
        loop {
            if self.done {
                return Ok(None);
            }
            let data = match self.src.next().await {
                Next::Data(d) => d,
                Next::End => {
                    self.done = true;
                    return Ok(Some(Token {
                        done: true,
                        ..Token::default()
                    }));
                }
                Next::Failed(e) => {
                    self.done = true;
                    return Err(e);
                }
            };
            let v = match self.src.decode(&data) {
                Ok(v) => v,
                Err(e) => {
                    self.done = true;
                    return Err(e);
                }
            };
            let cand = v["candidates"].as_array().and_then(|c| c.first());
            let text = cand
                .map(|c| content_text(&c["content"]))
                .unwrap_or_default();
            let finish = cand
                .and_then(|c| c["finishReason"].as_str())
                .unwrap_or_default();
            if finish == "STOP" || finish == "MAX_TOKENS" {
                self.done = true;
                return Ok(Some(Token {
                    content: text,
                    done: true,
                    ..Token::default()
                }));
            }
            if !text.is_empty() {
                return Ok(Some(Token {
                    content: text,
                    ..Token::default()
                }));
            }
        }
    }
}

impl TokenStream for PlainStream {
    fn next(&mut self) -> BoxFuture<'_, Result<Option<Token>, LlmError>> {
        Box::pin(self.next_token())
    }
}

/// Folds chunks into tokens and the turn's outcome: text parts in
/// order, each `functionCall` whole, usage cumulative, and `done` on a
/// finish reason (or a blocked prompt). A body ending before one is
/// truncated.
pub(crate) struct ToolStream {
    src: Source,
    names: ToolNames,
    events: bool,
    /// Tokens of a chunk not yet returned.
    pending: VecDeque<Token>,
    /// No more chunks are read (finished or failed).
    done: bool,
    text: String,
    calls: Vec<ToolCall>,
    usage: Usage,
    finish: String,
    outcome: Option<StreamOutcome>,
}

impl ToolStream {
    pub(crate) fn new(
        resp: reqwest::Response,
        p: StreamParts,
        names: ToolNames,
        events: bool,
    ) -> Self {
        ToolStream {
            src: Source::new(resp, p),
            names,
            events,
            pending: VecDeque::new(),
            done: false,
            text: String::new(),
            calls: Vec::new(),
            usage: Usage::default(),
            finish: String::new(),
            outcome: None,
        }
    }

    /// Queues one chunk's tokens: its parts in order, its usage, then
    /// done when it ends the turn.
    fn fold(&mut self, v: &Value) {
        let mut finish = String::new();
        if let Some(c) = v["candidates"].as_array().and_then(|c| c.first()) {
            for p in c["content"]["parts"].as_array().into_iter().flatten() {
                if let Some(t) = p["text"].as_str().filter(|t| !t.is_empty()) {
                    self.text.push_str(t);
                    self.pending.push_back(Token {
                        content: t.to_string(),
                        ..Token::default()
                    });
                }
                if p.get("functionCall").is_some_and(|f| !f.is_null()) {
                    self.call(p);
                }
            }
            finish = c["finishReason"].as_str().unwrap_or_default().to_string();
        } else if let Some(b) = v["promptFeedback"]["blockReason"].as_str() {
            finish = b.to_string();
        }
        if v["usageMetadata"].is_object() {
            self.add_usage(usage_of(&v["usageMetadata"]));
            if self.events {
                self.pending.push_back(Token {
                    usage: Some(self.usage),
                    ..Token::default()
                });
            }
        }
        if !finish.is_empty() {
            self.finish = map_finish_reason(&finish);
            self.done = true;
            self.pending.push_back(Token {
                done: true,
                ..Token::default()
            });
        }
    }

    /// Records a functionCall part and, when asked for, queues its
    /// start, delta and end.
    fn call(&mut self, p: &Value) {
        let tc = call_of(p, &self.names);
        let i = self.calls.len();
        self.calls.push(tc.clone());
        if !self.events {
            return;
        }
        let ev = |kind, id: &str, name: &str, delta: &str, call| Token {
            tool_call: Some(ToolCallEvent {
                kind,
                index: i,
                id: id.to_string(),
                name: name.to_string(),
                arguments_delta: delta.to_string(),
                call,
            }),
            ..Token::default()
        };
        self.pending
            .push_back(ev(ToolCallEventKind::Start, &tc.id, &tc.name, "", None));
        if !tc.arguments.is_empty() {
            self.pending
                .push_back(ev(ToolCallEventKind::Delta, "", "", &tc.arguments, None));
        }
        self.pending.push_back(ev(
            ToolCallEventKind::End,
            &tc.id,
            &tc.name,
            "",
            Some(tc.clone()),
        ));
    }

    /// Gemini reports usage cumulatively; a count a chunk omits keeps its
    /// last value, so no count goes down.
    fn add_usage(&mut self, u: Usage) {
        self.usage.prompt_tokens = self.usage.prompt_tokens.max(u.prompt_tokens);
        self.usage.completion_tokens = self.usage.completion_tokens.max(u.completion_tokens);
        self.usage.total_tokens = self.usage.total_tokens.max(u.total_tokens);
    }

    async fn next_token(&mut self) -> Result<Option<Token>, LlmError> {
        loop {
            if let Some(tok) = self.pending.pop_front() {
                if tok.done {
                    self.outcome = Some(StreamOutcome {
                        content: self.text.clone(),
                        tool_calls: self.calls.clone(),
                        finish_reason: self.finish.clone(),
                        usage: self.usage,
                        ..StreamOutcome::default()
                    });
                }
                return Ok(Some(tok));
            }
            if self.done {
                return Ok(None);
            }
            let data = match self.src.next().await {
                Next::Data(d) => d,
                // No finish reason: the response is incomplete.
                Next::End => {
                    self.done = true;
                    return Err(LlmError::Truncated {
                        provider: "gemini".into(),
                    });
                }
                Next::Failed(e) => {
                    self.done = true;
                    return Err(e);
                }
            };
            match self.src.decode(&data) {
                Ok(v) => self.fold(&v),
                Err(e) => {
                    self.done = true;
                    return Err(e);
                }
            }
        }
    }
}

impl TokenStream for ToolStream {
    fn next(&mut self) -> BoxFuture<'_, Result<Option<Token>, LlmError>> {
        Box::pin(self.next_token())
    }

    fn outcome(&self) -> Option<&StreamOutcome> {
        self.outcome.as_ref()
    }
}
