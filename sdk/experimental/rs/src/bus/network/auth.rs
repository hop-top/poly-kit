//! The auth handshake's credentials: Go's `network_auth.go`,
//! `network_auth_env.go` and `OpenAuth`.

use super::error::{AuthError, NetworkError};

/// Credentials for the handshake on network links: Go's `Authenticator`.
///
/// With one configured, a dialer sends [`token`](Authenticator::token)
/// as its first frame and waits for the ack; an acceptor reads that
/// frame and checks it with [`verify`](Authenticator::verify).
pub trait Authenticator: Send + Sync {
    /// The token to send on an outbound connect.
    fn token(&self) -> Result<String, AuthError>;
    /// Accepts or refuses an inbound token.
    fn verify(&self, token: &str) -> Result<(), AuthError>;
}

/// A shared secret: Go's `StaticTokenAuth`.
#[derive(Clone, PartialEq, Eq)]
pub struct StaticTokenAuth {
    /// The secret sent and expected. Go's `Token_`.
    pub token: String,
}

impl StaticTokenAuth {
    /// An authenticator for the shared secret `token`.
    pub fn new(token: impl Into<String>) -> Self {
        StaticTokenAuth {
            token: token.into(),
        }
    }
}

impl std::fmt::Debug for StaticTokenAuth {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("StaticTokenAuth")
            .field("token", &"<redacted>")
            .finish()
    }
}

impl Authenticator for StaticTokenAuth {
    fn token(&self) -> Result<String, AuthError> {
        Ok(self.token.clone())
    }

    /// Refuses any other token with [`NetworkError::AuthFailed`].
    fn verify(&self, token: &str) -> Result<(), AuthError> {
        if constant_time_eq(token.as_bytes(), self.token.as_bytes()) {
            Ok(())
        } else {
            Err(Box::new(NetworkError::AuthFailed))
        }
    }
}

/// Equal-length comparison without an early exit, so the time taken does
/// not reveal how much of a guess matched.
fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    a.len() == b.len() && a.iter().zip(b).fold(0u8, |acc, (x, y)| acc | (x ^ y)) == 0
}

/// For deployments without a secret that still want the handshake: Go's
/// `OpenAuth`. Sends the token `open` and accepts any non-empty token.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct OpenAuth;

impl Authenticator for OpenAuth {
    fn token(&self) -> Result<String, AuthError> {
        Ok("open".to_string())
    }

    fn verify(&self, token: &str) -> Result<(), AuthError> {
        if token.is_empty() {
            Err(Box::new(NetworkError::AuthFailed))
        } else {
            Ok(())
        }
    }
}

/// A [`StaticTokenAuth`] from the first non-empty variable among `names`,
/// checked in order: Go's `AuthFromEnv`. Pass tool-specific names first
/// and a generic fallback last (`["DPKMS_BUS_TOKEN", "BUS_TOKEN"]`).
/// `None` when every variable is unset or empty; the caller decides
/// whether that is fatal.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub fn auth_from_env<S: AsRef<str>>(names: &[S]) -> Option<StaticTokenAuth> {
    names
        .iter()
        .filter_map(|n| std::env::var(n.as_ref()).ok())
        .find(|v| !v.is_empty())
        .map(StaticTokenAuth::new)
}
