//! The error taxonomy. Messages carry Go's `identity: ` prefix and wording
//! so logs read the same across ports.

use thiserror::Error;

/// Everything this module can refuse or fail at.
#[derive(Debug, Error)]
pub enum IdentityError {
    /// The OS random source failed.
    #[error("identity: generate keypair: {0}")]
    Generate(String),
    /// Encoding a key to PEM failed.
    #[error("identity: marshal key: {0}")]
    Marshal(String),
    /// No `-----BEGIN` block in the input.
    #[error("identity: no PEM block found")]
    NoPem,
    /// The PEM block is not a valid Ed25519 public key.
    #[error("identity: parse public key: {0}")]
    ParsePublicKey(String),
    /// The PEM block is not a valid Ed25519 PKCS#8 private key.
    #[error("identity: parse private key: {0}")]
    ParsePrivateKey(String),
    /// The token does not have three dot-separated parts.
    #[error("identity: malformed JWT: expected 3 parts")]
    MalformedJwt,
    /// The signature segment is not base64url.
    #[error("identity: decode signature: {0}")]
    DecodeSignature(String),
    /// The signature does not verify under the key.
    #[error("identity: invalid JWT signature")]
    InvalidSignature,
    /// The payload segment is not base64url.
    #[error("identity: decode payload: {0}")]
    DecodePayload(String),
    /// The payload is not a claims object.
    #[error("identity: unmarshal claims: {0}")]
    UnmarshalClaims(String),
    /// `aud` is neither a string nor an array of strings.
    #[error("identity: aud must be string or []string: {0}")]
    AudienceFormat(String),
    /// `exp` is set and in the past.
    #[error("identity: token expired")]
    Expired,
    /// The `iss` claim differs from the expected issuer.
    #[error("identity: issuer mismatch: got {got:?}, want {want:?}")]
    IssuerMismatch {
        /// The token's issuer.
        got: String,
        /// The issuer the caller required.
        want: String,
    },
    /// The expected audience is not among the token's `aud`.
    #[error("identity: audience mismatch: got {got}, want {want:?}")]
    AudienceMismatch {
        /// The token's audience, formatted `[a b]`.
        got: String,
        /// The audience the caller required.
        want: String,
    },
    /// The nonce could not be generated.
    #[error("identity: generate nonce: {0}")]
    Nonce(String),
    /// Shorter than a nonce plus the Poly1305 tag.
    #[error("identity: ciphertext too short")]
    CiphertextTooShort,
    /// Authentication failed: wrong key or tampered data.
    #[error("identity: decryption failed")]
    DecryptFailed,
    /// No keypair at the expected path.
    #[error("identity: keypair not found")]
    NotFound,
    /// A filesystem operation of the store failed.
    #[cfg(not(target_arch = "wasm32"))]
    #[error("identity: {context}: {source}")]
    Io {
        /// What was being done.
        context: &'static str,
        /// The underlying error.
        #[source]
        source: std::io::Error,
    },
}
