//! The adapters: the only modules that name a signature crate.
//!
//! Everything above this seam speaks kit-owned types ([`VerifyKey`],
//! [`Alg`]); an adapter turns the key bytes into its crate's key type
//! and answers one question, whether a signature verifies. Swapping a
//! crate (or dropping `ring` for a fixed `rsa`) is a change here and
//! nowhere else.

use super::key::{Alg, VerifyKey};

mod ecdsa;
mod eddsa;
mod rsa;

/// What a signature adapter answers.
pub(crate) trait SignatureVerifier {
    /// Whether `key` can be built into the adapter's key type.
    fn validate(&self, key: &VerifyKey) -> Result<(), String>;
    /// Whether `sig` is a valid signature over `msg` by `key` under
    /// `alg`. `false` for any malformed input.
    fn verify(&self, alg: Alg, key: &VerifyKey, msg: &[u8], sig: &[u8]) -> bool;
}

fn adapter(key: &VerifyKey) -> &'static dyn SignatureVerifier {
    match key {
        VerifyKey::Ed25519(_) => &eddsa::EdDsa,
        VerifyKey::P256(_) | VerifyKey::P384(_) | VerifyKey::P521(_) => &ecdsa::Ecdsa,
        VerifyKey::Rsa { .. } => &rsa::Rsa,
    }
}

pub(crate) fn validate(key: &VerifyKey) -> Result<(), String> {
    adapter(key).validate(key)
}

pub(crate) fn verify(alg: Alg, key: &VerifyKey, msg: &[u8], sig: &[u8]) -> bool {
    key.supports(alg) && adapter(key).verify(alg, key, msg, sig)
}
