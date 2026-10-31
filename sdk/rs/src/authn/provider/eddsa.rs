use ed25519_dalek::{Signature, VerifyingKey};

use super::SignatureVerifier;
use crate::authn::key::{Alg, VerifyKey};

pub(super) struct EdDsa;

impl SignatureVerifier for EdDsa {
    fn validate(&self, key: &VerifyKey) -> Result<(), String> {
        let VerifyKey::Ed25519(k) = key else {
            return Err("not an Ed25519 key".into());
        };
        let vk = VerifyingKey::from_bytes(k).map_err(|_| "invalid Ed25519 public key")?;
        if vk.is_weak() {
            return Err("small-order Ed25519 public key".into());
        }
        Ok(())
    }

    fn verify(&self, alg: Alg, key: &VerifyKey, msg: &[u8], sig: &[u8]) -> bool {
        let (Alg::EdDsa, VerifyKey::Ed25519(k)) = (alg, key) else {
            return false;
        };
        let Ok(vk) = VerifyingKey::from_bytes(k) else {
            return false;
        };
        let Ok(sig) = Signature::from_slice(sig) else {
            return false;
        };
        // Strict: rejects small-order keys and non-canonical signatures.
        vk.verify_strict(msg, &sig).is_ok()
    }
}
