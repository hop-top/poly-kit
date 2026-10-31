//! The NDJSON token stream of `/api/chat`, as Go's `streamIterator`:
//! one JSON object per line, the last with `done` set. A body that ends
//! without `done` ends the stream normally, as in Go.

use serde_json::Value;

use crate::llm::{BoxFuture, LlmError, StreamOutcome, Token, TokenStream, Usage};

use super::wire::{outcome, parse_chat};

pub(crate) struct ChatStream {
    resp: reqwest::Response,
    buf: Vec<u8>,
    done: bool,
    content: String,
    usage: Usage,
    outcome: Option<StreamOutcome>,
    transport: Box<dyn Fn(reqwest::Error) -> LlmError + Send>,
    max_line: u64,
}

impl ChatStream {
    pub(crate) fn new(
        resp: reqwest::Response,
        max_line: u64,
        transport: Box<dyn Fn(reqwest::Error) -> LlmError + Send>,
    ) -> Self {
        ChatStream {
            resp,
            buf: Vec::new(),
            done: false,
            content: String::new(),
            usage: Usage::default(),
            outcome: None,
            transport,
            max_line,
        }
    }

    fn finish(&mut self, content: String) -> Result<Option<Token>, LlmError> {
        self.done = true;
        self.outcome = Some(outcome(std::mem::take(&mut self.content), self.usage));
        Ok(Some(Token {
            content,
            done: true,
            ..Token::default()
        }))
    }

    /// Handles one line; `Some` yields a token or ends the stream.
    fn line(&mut self, raw: &[u8]) -> Option<Result<Option<Token>, LlmError>> {
        let line = String::from_utf8_lossy(raw);
        let line = line.trim();
        if line.is_empty() {
            return None;
        }
        let v: Value = match serde_json::from_str(line) {
            Ok(v) => v,
            Err(e) => {
                self.done = true;
                return Some(Err(LlmError::Protocol(format!(
                    "ollama: decode stream chunk: {e}"
                ))));
            }
        };
        let chat = parse_chat(&v);
        self.content.push_str(&chat.content);
        if chat.done {
            self.usage = chat.usage;
            return Some(self.finish(chat.content));
        }
        Some(Ok(Some(Token {
            content: chat.content,
            ..Token::default()
        })))
    }

    async fn next_token(&mut self) -> Result<Option<Token>, LlmError> {
        loop {
            if self.done {
                return Ok(None);
            }
            if let Some(i) = self.buf.iter().position(|&b| b == b'\n') {
                let raw: Vec<u8> = self.buf.drain(..=i).collect();
                if let Some(r) = self.line(&raw) {
                    return r;
                }
                continue;
            }
            match self.resp.chunk().await {
                Ok(Some(bytes)) => {
                    self.buf.extend_from_slice(&bytes);
                    if self.buf.len() as u64 > self.max_line && !self.buf.contains(&b'\n') {
                        self.done = true;
                        return Err(LlmError::ResponseTooLarge {
                            provider: "ollama".into(),
                            what: "event",
                            limit: self.max_line,
                        });
                    }
                }
                Ok(None) => {
                    // The body ended: a last line without its newline
                    // still counts; then the stream is done.
                    let rest = std::mem::take(&mut self.buf);
                    if let Some(r) = self.line(&rest) {
                        return r;
                    }
                    return self.finish(String::new());
                }
                Err(e) => {
                    self.done = true;
                    return Err((self.transport)(e));
                }
            }
        }
    }
}

impl TokenStream for ChatStream {
    fn next(&mut self) -> BoxFuture<'_, Result<Option<Token>, LlmError>> {
        Box::pin(self.next_token())
    }

    fn outcome(&self) -> Option<&StreamOutcome> {
        self.outcome.as_ref()
    }
}
