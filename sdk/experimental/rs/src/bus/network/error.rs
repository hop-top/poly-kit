//! [`NetworkError`]: what connect, disconnect and close return.

use std::error::Error;
use std::fmt;

use crate::bus::context::ContextError;
use crate::netpolicy::OfflineError;

/// The error an authenticator returns. Any error type works; the adapter
/// reports it unchanged.
pub type AuthError = Box<dyn Error + Send + Sync>;

/// Errors from the network adapter. Texts are Go's where Go has one.
#[derive(Debug)]
#[non_exhaustive]
pub enum NetworkError {
    /// The adapter is closed: Go's `ErrBusClosed`.
    Closed,
    /// A connect lost a race with [`disconnect`] for its address: Go's
    /// `ErrDisconnected`. No link was made.
    ///
    /// [`disconnect`]: super::NetworkAdapter::disconnect
    Disconnected,
    /// The policy refused the dial: offline, and not a loopback peer.
    /// Matches Go's `errors.Is(err, netpolicy.ErrOffline)`.
    Offline(OfflineError),
    /// The WebSocket dial failed: an unusable address, a refused or
    /// failed connection, TLS, or a rejected upgrade.
    Dial(String),
    /// The peer refused the token, or sent no usable ack: Go's
    /// `ErrAuthFailed`.
    AuthFailed,
    /// The peer sent no ack before the timeout. Go's `errAckTimeout`,
    /// which wraps `ErrAuthFailed`: [`is_auth_failed`] holds for it too.
    ///
    /// [`is_auth_failed`]: NetworkError::is_auth_failed
    AckTimeout,
    /// The authenticator failed to produce a token.
    Token(AuthError),
    /// Writing the auth frame failed.
    Write(String),
    /// The caller's context was done during the handshake.
    Context(ContextError),
}

impl NetworkError {
    /// Whether this is [`NetworkError::Closed`]: Go's
    /// `err == ErrBusClosed`.
    pub fn is_closed(&self) -> bool {
        matches!(self, NetworkError::Closed)
    }

    /// Whether authentication failed: Go's
    /// `errors.Is(err, ErrAuthFailed)`, a missing ack included.
    pub fn is_auth_failed(&self) -> bool {
        matches!(self, NetworkError::AuthFailed | NetworkError::AckTimeout)
    }

    /// The policy refusal, if the dial was refused as offline.
    pub fn as_offline(&self) -> Option<&OfflineError> {
        match self {
            NetworkError::Offline(e) => Some(e),
            _ => None,
        }
    }
}

impl fmt::Display for NetworkError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            NetworkError::Closed => f.write_str("bus: publish after close"),
            NetworkError::Disconnected => f.write_str("bus: address disconnected during connect"),
            NetworkError::Offline(e) => write!(f, "failed to WebSocket dial: {e}"),
            NetworkError::Dial(m) => write!(f, "failed to WebSocket dial: {m}"),
            NetworkError::AuthFailed => f.write_str(AUTH_FAILED),
            NetworkError::AckTimeout => write!(f, "{AUTH_FAILED}: no ack before the timeout"),
            NetworkError::Token(e) => write!(f, "{e}"),
            NetworkError::Write(m) => write!(f, "failed to write auth frame: {m}"),
            NetworkError::Context(e) => write!(f, "{e}"),
        }
    }
}

impl Error for NetworkError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            NetworkError::Offline(e) => Some(e),
            NetworkError::Token(e) => Some(e.as_ref()),
            NetworkError::Context(e) => Some(e),
            _ => None,
        }
    }
}

/// Go's `ErrAuthFailed` text.
pub(crate) const AUTH_FAILED: &str = "bus: network auth failed";
