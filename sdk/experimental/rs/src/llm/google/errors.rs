//! Error mapping for the Gemini API, as Go's `checkStatus`,
//! `mapAPIError` and `mapStreamError`: an error body maps by its own
//! `code`, a bare status by the HTTP status.

use serde_json::Value;

use crate::llm::wirehttp::cap;
use crate::llm::LlmError;

const PROVIDER: &str = "gemini";

/// Maps `{"code":..,"message":..,"status":..}`.
pub(crate) fn map_api_error(e: &Value, model: &str) -> LlmError {
    let code = e["code"].as_u64().unwrap_or(0) as u16;
    let message = cap(e["message"].as_str().unwrap_or_default());
    let status = e["status"].as_str().unwrap_or_default().to_string();
    classify(code, message, status, model)
}

fn classify(code: u16, message: String, status: String, model: &str) -> LlmError {
    match code {
        401 => LlmError::Auth {
            provider: PROVIDER.into(),
            message,
        },
        403 => LlmError::PermissionDenied {
            provider: PROVIDER.into(),
            message,
        },
        404 => LlmError::Model {
            model: model.into(),
            provider: PROVIDER.into(),
        },
        429 => LlmError::RateLimit {
            provider: PROVIDER.into(),
            retry_after: None,
        },
        c if c >= 500 => LlmError::HttpStatus { status: c, message },
        c => LlmError::Api {
            provider: PROVIDER.into(),
            status: c,
            error_type: status,
            message,
        },
    }
}

/// Maps a non-2xx response: its error body when it has one, else the
/// HTTP status.
pub(crate) fn map_status(status: u16, text: &str, model: &str) -> LlmError {
    if let Ok(v) = serde_json::from_str::<Value>(text) {
        if v["error"].is_object() {
            return map_api_error(&v["error"], model);
        }
    }
    classify(status, cap(text.trim()), String::new(), model)
}
