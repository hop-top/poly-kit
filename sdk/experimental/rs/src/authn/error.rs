use std::fmt;

/// What kind of refusal an [`AuthnError`] is, for callers that branch
/// on class rather than text (the Go port's `errors.Is` sentinels).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    /// The request carried no usable bearer token.
    NoToken,
    /// The credential failed verification.
    InvalidToken,
    /// The key set or key store could not be read; the credential was
    /// not judged and a later attempt may succeed.
    KeySetUnavailable,
    /// An API key id the store does not hold.
    ApiKeyNotFound,
    /// A verifier or key could not be constructed.
    Config,
}

/// A refusal from verification, key handling or the API-key store.
///
/// Every credential failure is [`Kind::InvalidToken`] except a missing
/// credential ([`Kind::NoToken`]) and a key set or store that could not
/// be read ([`Kind::KeySetUnavailable`]). The `Display` text matches
/// the Go port's error strings.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum AuthnError {
    /// No bearer token; the detail, when present, says why the header
    /// was not one.
    NoToken(Option<String>),
    /// The credential failed verification.
    InvalidToken(String),
    /// The key set or store could not be read.
    KeySetUnavailable(String),
    /// A revoke or lookup of an API key id the store lacks.
    ApiKeyNotFound(String),
    /// A verifier, key or issue request that cannot be built.
    Config(String),
}

impl AuthnError {
    /// The class of this refusal.
    pub fn kind(&self) -> Kind {
        match self {
            Self::NoToken(_) => Kind::NoToken,
            Self::InvalidToken(_) => Kind::InvalidToken,
            Self::KeySetUnavailable(_) => Kind::KeySetUnavailable,
            Self::ApiKeyNotFound(_) => Kind::ApiKeyNotFound,
            Self::Config(_) => Kind::Config,
        }
    }

    pub(crate) fn invalid(msg: impl Into<String>) -> Self {
        Self::InvalidToken(msg.into())
    }
}

impl fmt::Display for AuthnError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::NoToken(None) => f.write_str("authn: no bearer token"),
            Self::NoToken(Some(d)) => write!(f, "authn: no bearer token: {d}"),
            Self::InvalidToken(m) => write!(f, "authn: invalid token: {m}"),
            Self::KeySetUnavailable(m) => write!(f, "authn: key set unavailable: {m}"),
            Self::ApiKeyNotFound(id) => write!(f, "authn: no such api key: {id}"),
            Self::Config(m) => write!(f, "authn: {m}"),
        }
    }
}

impl std::error::Error for AuthnError {}
