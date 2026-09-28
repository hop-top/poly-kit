//! Who is calling, as both eras' `tools/call` gate reads it.
//!
//! The only source of identity is the mount's [`Verifier`]. An
//! `Authorization` header is presence, not verification, and a caller or
//! scopes the request claims in its body never become identity. Mirrors
//! the Go surfaces, where only an established `Meta` admits a
//! `kit/auth-required` leaf.

use std::fmt;
use std::sync::Arc;

use super::dispatch::HttpRequest;

/// The challenge a 401 carries in `WWW-Authenticate`, as Go's
/// `api.DefaultAuthChallenge`.
pub const AUTH_CHALLENGE: &str = "Bearer";

/// The caller a verifier established for one request.
///
/// `caller` is the stable principal (a user id, a service account, a
/// client id), `tenant` the account it acts in, `scopes` what it was
/// granted. All may be empty: a verifier may accept a credential that
/// names no principal.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Identity {
    /// The stable principal.
    pub caller: String,
    /// The tenant the caller acts in.
    pub tenant: String,
    /// What the caller was granted.
    pub scopes: Vec<String>,
}

type VerifyFn = dyn Fn(&HttpRequest) -> Option<Identity> + Send + Sync;

/// Establishes who is calling.
///
/// The mount calls it once per `tools/call` with the request as
/// received; it checks a credential the request presents (a bearer
/// token, a signed header) and returns the [`Identity`], or `None` to
/// refuse. Without one no call is established, so every
/// `kit/auth-required` leaf is refused with 401.
#[derive(Clone)]
pub struct Verifier(Arc<VerifyFn>);

impl Verifier {
    /// Wraps `f` as a verifier.
    pub fn new<F>(f: F) -> Self
    where
        F: Fn(&HttpRequest) -> Option<Identity> + Send + Sync + 'static,
    {
        Self(Arc::new(f))
    }

    /// Runs the verifier over `req`.
    #[must_use]
    pub fn verify(&self, req: &HttpRequest) -> Option<Identity> {
        (self.0)(req)
    }
}

impl fmt::Debug for Verifier {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("Verifier(..)")
    }
}
