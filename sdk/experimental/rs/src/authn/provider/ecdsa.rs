use super::SignatureVerifier;
use crate::authn::key::{Alg, VerifyKey};

pub(super) struct Ecdsa;

/// One macro arm per curve: the three crates share an API but not a
/// type. Signatures are the fixed-size `r || s` JWS form (RFC 7518
/// section 3.4), never DER.
macro_rules! curve {
    ($krate:ident, $point:expr, $msg:expr, $sig:expr) => {{
        use $krate::ecdsa::signature::Verifier;
        match (
            $krate::ecdsa::VerifyingKey::from_sec1_bytes($point),
            $krate::ecdsa::Signature::from_slice($sig),
        ) {
            (Ok(vk), Ok(sig)) => vk.verify($msg, &sig).is_ok(),
            _ => false,
        }
    }};
}

impl SignatureVerifier for Ecdsa {
    fn validate(&self, key: &VerifyKey) -> Result<(), String> {
        let ok = match key {
            VerifyKey::P256(p) => p256::ecdsa::VerifyingKey::from_sec1_bytes(p).is_ok(),
            VerifyKey::P384(p) => p384::ecdsa::VerifyingKey::from_sec1_bytes(p).is_ok(),
            VerifyKey::P521(p) => p521::ecdsa::VerifyingKey::from_sec1_bytes(p).is_ok(),
            _ => return Err("not an ECDSA key".into()),
        };
        if ok {
            Ok(())
        } else {
            Err("point is not on the curve".into())
        }
    }

    fn verify(&self, alg: Alg, key: &VerifyKey, msg: &[u8], sig: &[u8]) -> bool {
        match (alg, key) {
            (Alg::Es256, VerifyKey::P256(p)) => curve!(p256, p, msg, sig),
            (Alg::Es384, VerifyKey::P384(p)) => curve!(p384, p, msg, sig),
            (Alg::Es512, VerifyKey::P521(p)) => curve!(p521, p, msg, sig),
            _ => false,
        }
    }
}
