//! The llm error taxonomy, a port of go/ai/llm/errors.
//!
//! Messages never carry a key: URLs in them are stripped or masked, and
//! [`MissingKeyError`] names variables only.

use std::fmt;
use std::time::Duration;

use crate::netpolicy::OfflineError;

/// Every error the llm core and its adapters return.
#[derive(Debug, Clone, PartialEq)]
#[non_exhaustive]
pub enum LlmError {
    /// No adapter serves the URI's scheme.
    ProviderNotFound {
        scheme: String,
    },
    /// The provider does not implement the requested call.
    CapabilityNotSupported {
        capability: String,
        provider: String,
    },
    /// 401 or 403.
    Auth {
        provider: String,
        message: String,
    },
    /// 429, or a `rate_limit_error` event. Fallbackable.
    RateLimit {
        provider: String,
        /// What the provider asked for, when it said.
        retry_after: Option<Duration>,
    },
    /// 404: the model is unknown or unavailable.
    Model {
        model: String,
        provider: String,
    },
    /// A 5xx response (529 overloaded included), or a stream error event
    /// of that class. Fallbackable when `status >= 500`.
    HttpStatus {
        status: u16,
        message: String,
    },
    /// Any other API error response. Not fallbackable.
    Api {
        provider: String,
        status: u16,
        error_type: String,
        message: String,
    },
    /// Every provider in the chain failed; one error per provider.
    FallbackExhausted(Vec<LlmError>),
    UnsupportedModality {
        modality: String,
        provider: String,
    },
    InvalidFormat {
        format: String,
        provider: String,
    },
    /// A required key resolved nowhere.
    MissingKey(MissingKeyError),
    /// A credential kit will not send to `endpoint` (masked).
    KeyEndpoint {
        scheme: String,
        endpoint: String,
        reason: KeyEndpointReason,
    },
    /// Refused by the `--offline` policy; never retried.
    Offline(OfflineError),
    /// The request could not be sent or its body not read. Fallbackable,
    /// like Go's network errors.
    Transport {
        message: String,
    },
    /// A stream ended before its final event: incomplete, never a
    /// success. Not fallbackable.
    Truncated {
        provider: String,
    },
    /// A response body, or one streamed event, exceeded its limit.
    ResponseTooLarge {
        provider: String,
        /// `body` or `event`.
        what: &'static str,
        limit: u64,
    },
    /// A retry wait was cancelled (Go's context error).
    Cancelled,
    /// The provider's response could not be understood.
    Protocol(String),
    /// Rejected before anything was sent: a bad URI, config or request.
    Invalid(String),
}

/// Why a credential was refused for an endpoint.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum KeyEndpointReason {
    /// Plain http to a host that is not loopback.
    Insecure,
    /// Userinfo, a backslash, a percent-escape, whitespace or a control
    /// character, or the endpoint does not parse.
    Malformed,
    /// Only the URI names the endpoint, and it is not trusted.
    Untrusted,
}

impl LlmError {
    /// Whether the client should try the next provider (Go
    /// `IsFallbackable`): rate limits, transport failures and 5xx.
    pub fn is_fallbackable(&self) -> bool {
        match self {
            LlmError::RateLimit { .. } | LlmError::Transport { .. } => true,
            LlmError::HttpStatus { status, .. } => *status >= 500,
            _ => false,
        }
    }

    pub(crate) fn invalid(msg: impl Into<String>) -> Self {
        LlmError::Invalid(msg.into())
    }
}

impl fmt::Display for LlmError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            LlmError::ProviderNotFound { scheme } => {
                write!(f, "provider not found: scheme {scheme:?}")
            }
            LlmError::CapabilityNotSupported {
                capability,
                provider,
            } => write!(
                f,
                "capability {capability:?} not supported by provider {provider:?}"
            ),
            LlmError::Auth { provider, message } => {
                write!(f, "auth error (provider {provider:?}): {message}")
            }
            LlmError::RateLimit {
                provider,
                retry_after: None,
            } => write!(f, "rate limited (provider {provider:?})"),
            LlmError::RateLimit {
                provider,
                retry_after: Some(d),
            } => write!(f, "rate limited (provider {provider:?}, retry after {d:?})"),
            LlmError::Model { model, provider } => {
                write!(f, "model {model:?} not available (provider {provider:?})")
            }
            LlmError::HttpStatus { status, message } => write!(f, "http {status}: {message}"),
            LlmError::Api {
                provider,
                status,
                error_type,
                message,
            } => write!(f, "{provider}: http {status}: {error_type}: {message}"),
            LlmError::FallbackExhausted(errs) => {
                write!(f, "all providers failed ({} errors)", errs.len())
            }
            LlmError::UnsupportedModality { modality, provider } => write!(
                f,
                "modality {modality:?} not supported by provider {provider:?}"
            ),
            LlmError::InvalidFormat { format, provider } => {
                write!(f, "invalid format {format:?} for provider {provider:?}")
            }
            LlmError::KeyEndpoint {
                scheme,
                endpoint,
                reason,
            } => match reason {
                KeyEndpointReason::Insecure => write!(
                    f,
                    "llm: refusing to send the {scheme} API key over plain http to {endpoint:?}; use https or a loopback host"
                ),
                KeyEndpointReason::Malformed => write!(
                    f,
                    "llm: refusing to send the {scheme} API key to {endpoint:?}: an endpoint must parse and carry no userinfo, backslash, percent-escape, whitespace or control character"
                ),
                KeyEndpointReason::Untrusted => write!(
                    f,
                    "llm: refusing to send the {scheme} API key to {endpoint:?}, which only the URI names; set it as llm.yaml base_url or LLM_BASE_URL, or pass api_key in the URI"
                ),
            },
            LlmError::MissingKey(e) => e.fmt(f),
            LlmError::Offline(e) => e.fmt(f),
            LlmError::Transport { message } => f.write_str(message),
            LlmError::Truncated { provider } => write!(
                f,
                "stream truncated (provider {provider:?}): ended before its final event"
            ),
            LlmError::ResponseTooLarge {
                provider,
                what,
                limit,
            } => write!(
                f,
                "response {what} exceeds {limit} bytes (provider {provider:?})"
            ),
            LlmError::Cancelled => f.write_str("context canceled"),
            LlmError::Protocol(m) | LlmError::Invalid(m) => f.write_str(m),
        }
    }
}

impl std::error::Error for LlmError {}

impl From<MissingKeyError> for LlmError {
    fn from(e: MissingKeyError) -> Self {
        LlmError::MissingKey(e)
    }
}

impl From<OfflineError> for LlmError {
    fn from(e: OfflineError) -> Self {
        LlmError::Offline(e)
    }
}

/// No key was found for a scheme that requires one. Names only, never a
/// value (Go `MissingKeyError`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct MissingKeyError {
    pub scheme: String,
    pub model: String,
    /// Every name consulted, highest precedence first, ending with
    /// [`FALLBACK_ENV_KEY`](super::FALLBACK_ENV_KEY).
    pub env_vars: Vec<String>,
    /// Secret-store failures met on the way; names keys, never values.
    pub store_err: Option<String>,
}

impl fmt::Display for MissingKeyError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "llm: no API key for provider {:?}", self.scheme)?;
        if !self.model.is_empty() {
            write!(f, " (model {:?})", self.model)?;
        }
        write!(f, ": set {}", self.env_vars.join(" or "))?;
        if let Some(e) = &self.store_err {
            write!(f, " ({e})")?;
        }
        Ok(())
    }
}

impl std::error::Error for MissingKeyError {}
