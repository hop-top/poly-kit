//! Error mapping for the Chat Completions API, as Go's `mapError`:
//! 401 auth, 403 permission denied, 404 model, 429 rate limit, 5xx an
//! HTTP status error, 3xx a refused redirect, anything else the API's
//! own error. Pinned by `contracts/llm-v1` (`openai_error`).

use std::time::Duration;

use serde_json::Value;

use crate::llm::wirehttp::{cap, redirect};
use crate::llm::LlmError;

/// `{"error":{"message":..,"type":..,"code":..}}`, or the raw text.
pub(crate) fn error_body(text: &str) -> (String, String) {
    match serde_json::from_str::<Value>(text) {
        Ok(v) if v["error"].is_object() => (
            v["error"]["type"].as_str().unwrap_or_default().to_string(),
            cap(v["error"]["message"].as_str().unwrap_or_default()),
        ),
        _ => (String::new(), cap(text.trim())),
    }
}

/// Map an error response to the llm taxonomy.
pub(crate) fn map_status(
    scheme: &str,
    status: u16,
    retry_after: Option<Duration>,
    text: &str,
    model: &str,
) -> LlmError {
    let (error_type, message) = error_body(text);
    classify(scheme, status, retry_after, error_type, message, model)
}

fn classify(
    scheme: &str,
    status: u16,
    retry_after: Option<Duration>,
    error_type: String,
    message: String,
    model: &str,
) -> LlmError {
    let detail = if error_type.is_empty() {
        message.clone()
    } else {
        format!("{error_type}: {message}")
    };
    match status {
        401 => LlmError::Auth {
            provider: scheme.into(),
            message: detail,
        },
        403 => LlmError::PermissionDenied {
            provider: scheme.into(),
            message: detail,
        },
        300..=399 => redirect("openai", status),
        429 => LlmError::RateLimit {
            provider: scheme.into(),
            retry_after,
        },
        404 => LlmError::Model {
            model: model.into(),
            provider: scheme.into(),
        },
        s if s >= 500 => LlmError::HttpStatus {
            status: s,
            message: if message.is_empty() { detail } else { message },
        },
        s => LlmError::Api {
            provider: scheme.into(),
            status: s,
            error_type,
            message,
        },
    }
}

/// An error chunk (`{"error":{..}}`) that arrived on a 200 response:
/// the HTTP status said nothing about it, so it is status 0, classified
/// like any other.
pub(crate) fn chunk_error(scheme: &str, data: &str, model: &str) -> LlmError {
    let (error_type, message) = error_body(data);
    classify(scheme, 0, None, error_type, message, model)
}
