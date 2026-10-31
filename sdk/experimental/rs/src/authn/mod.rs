//! Bearer-token and API-key authentication for kit's served surfaces,
//! a port of Go's `transport/authn`. Feature `authn`; wasm-safe.
//!
//! A [`Verifier`] checks a JWT and returns the caller it names; its
//! [`Verifier::auth_func`] is an [`AuthFunc`], the seam the api-server
//! middleware (and any other transport) calls with the credentials of a
//! request. [`ApiKeys`] (feature `authn-apikeys`) is the opaque
//! alternative: kit-issued keys held hashed in a kv store.
//!
//! Every JWT is checked the same way: a signature by one of the
//! source's keys under an asymmetric algorithm (`none` and HMAC are
//! never accepted), `exp` present and not past, `nbf` and `iat` not in
//! the future, each with the [`Options::clock_skew`] tolerance, the
//! issuer and audience when [`Options`] names them, a subject, and the
//! adopter's revocation check when [`Options::check`] is set.
//!
//! The [`Claims`] an `AuthFunc` returns: the principal from `sub`, the
//! tenant from [`Options::tenant_claim`] (`tenant` by default), and the
//! scopes as [`scopes_of`] reads them: `scopes` (a list), else `scope`
//! (space-delimited, RFC 8693 and RFC 9068), else `scp`.
//!
//! RS256/384/512 and PS256/384/512 verify only with `authn-rsa`
//! (native; `ring`). Without it an RSA key is accepted into a set and
//! verifies nothing.
//!
//! No signature crate appears in this API: keys are [`VerifyKey`]
//! bytes, and the crates live in `provider/`.
//!
//! Not ported: fetching a remote JWKS or OIDC discovery document. The
//! pure core takes the document bytes ([`Verifier::from_jwks`]) or any
//! [`KeySource`] a caller backs with its own guarded fetch.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::authn::{Credentials, Jwk, Kind, Options, Verifier};
//!
//! let jwk: Jwk = serde_json::from_str(
//!     r#"{"kty":"OKP","crv":"Ed25519","kid":"k1",
//!         "x":"dgUb5FuNK0dP84B50V5dkFIPQt8Xl7FpBaxpq3bMku0"}"#,
//! )
//! .unwrap();
//! let verifier = Verifier::new(vec![jwk.to_key().unwrap()], Options::default()).unwrap();
//!
//! let auth = verifier.auth_func();
//! let refusal = auth(&Credentials { authorization: Some("Bearer garbage"), api_key: None })
//!     .unwrap_err();
//! assert_eq!(refusal.kind(), Kind::InvalidToken);
//! ```

mod bearer;
mod claims;
mod error;
mod key;
mod provider;
#[cfg(any(feature = "authn-apikeys", feature = "webhook"))]
pub(crate) mod rfc3339;
mod verifier;

#[cfg(feature = "authn-apikeys")]
mod apikey;

use std::sync::Arc;

pub use bearer::bearer_token;
pub use claims::{identity_of, scopes_of, Claims, Identity};
pub use error::{AuthnError, Kind};
pub use key::{keys_from_jwks, Alg, Jwk, Key, VerifyKey, MAX_DOCUMENT_BYTES};
pub use verifier::{
    select, CheckFn, KeySource, NowFn, Options, Token, Verifier, DEFAULT_CLOCK_SKEW,
    DEFAULT_TENANT_CLAIM,
};

#[cfg(feature = "authn-apikeys")]
pub use apikey::{ApiKey, ApiKeys, NewApiKey, API_KEY_HASH_DOMAIN, API_KEY_HEADER, API_KEY_PREFIX};

/// What a request carries that authentication reads: the raw header
/// values, so this module names no HTTP crate.
#[derive(Debug, Clone, Copy, Default)]
pub struct Credentials<'a> {
    /// The `Authorization` header value.
    pub authorization: Option<&'a str>,
    /// The `X-API-Key` header value.
    pub api_key: Option<&'a str>,
}

/// Validates a request's credentials and returns the caller on
/// success. A transport maps an error to its unauthenticated answer
/// (401 with a challenge for HTTP).
pub type AuthFunc = Arc<dyn Fn(&Credentials<'_>) -> Result<Claims, AuthnError> + Send + Sync>;
