use super::SignatureVerifier;
use crate::authn::key::{Alg, VerifyKey};

pub(super) struct Rsa;

#[cfg(feature = "authn-rsa")]
mod imp {
    use ring::signature::{self, RsaParameters, UnparsedPublicKey};

    use super::{Alg, VerifyKey};

    /// `RSAPublicKey ::= SEQUENCE { modulus INTEGER, publicExponent INTEGER }`
    /// (RFC 8017 A.1.1), the form ring reads.
    pub(super) fn der(n: &[u8], e: &[u8]) -> Vec<u8> {
        fn int(v: &[u8], out: &mut Vec<u8>) {
            let pad = v.first().is_some_and(|b| b & 0x80 != 0);
            out.push(0x02);
            len(v.len() + usize::from(pad), out);
            if pad {
                out.push(0);
            }
            out.extend_from_slice(v);
        }
        fn len(n: usize, out: &mut Vec<u8>) {
            if n < 0x80 {
                out.push(n as u8);
            } else if n < 0x100 {
                out.extend_from_slice(&[0x81, n as u8]);
            } else {
                out.extend_from_slice(&[0x82, (n >> 8) as u8, n as u8]);
            }
        }
        let mut body = Vec::new();
        int(n, &mut body);
        int(e, &mut body);
        let mut out = vec![0x30];
        len(body.len(), &mut out);
        out.extend_from_slice(&body);
        out
    }

    fn params(alg: Alg) -> Option<&'static RsaParameters> {
        Some(match alg {
            Alg::Rs256 => &signature::RSA_PKCS1_2048_8192_SHA256,
            Alg::Rs384 => &signature::RSA_PKCS1_2048_8192_SHA384,
            Alg::Rs512 => &signature::RSA_PKCS1_2048_8192_SHA512,
            Alg::Ps256 => &signature::RSA_PSS_2048_8192_SHA256,
            Alg::Ps384 => &signature::RSA_PSS_2048_8192_SHA384,
            Alg::Ps512 => &signature::RSA_PSS_2048_8192_SHA512,
            _ => return None,
        })
    }

    pub(super) fn verify(alg: Alg, key: &VerifyKey, msg: &[u8], sig: &[u8]) -> bool {
        let (Some(p), VerifyKey::Rsa { n, e }) = (params(alg), key) else {
            return false;
        };
        UnparsedPublicKey::new(p, der(n, e))
            .verify(msg, sig)
            .is_ok()
    }

    /// ring accepts moduli of 2048 to 8192 bits only; a smaller key is
    /// refused up front rather than failing every token.
    pub(super) fn validate(key: &VerifyKey) -> Result<(), String> {
        let VerifyKey::Rsa { n, e } = key else {
            return Err("not an RSA key".into());
        };
        let bits = n.len() * 8 - n.first().map_or(0, |b| b.leading_zeros() as usize);
        if !(2048..=8192).contains(&bits) {
            return Err(format!("RSA modulus is {bits} bits; want 2048 to 8192"));
        }
        if e.is_empty() {
            return Err("RSA exponent is empty".into());
        }
        Ok(())
    }
}

impl SignatureVerifier for Rsa {
    #[cfg(feature = "authn-rsa")]
    fn validate(&self, key: &VerifyKey) -> Result<(), String> {
        imp::validate(key)
    }

    // Without `authn-rsa` an RSA key is kept in the set (a JWKS from
    // Google or Entra holds them) but verifies nothing.
    #[cfg(not(feature = "authn-rsa"))]
    fn validate(&self, key: &VerifyKey) -> Result<(), String> {
        match key {
            VerifyKey::Rsa { n, e } if !n.is_empty() && !e.is_empty() => Ok(()),
            _ => Err("not an RSA key".into()),
        }
    }

    #[cfg(feature = "authn-rsa")]
    fn verify(&self, alg: Alg, key: &VerifyKey, msg: &[u8], sig: &[u8]) -> bool {
        imp::verify(alg, key, msg, sig)
    }

    #[cfg(not(feature = "authn-rsa"))]
    fn verify(&self, _: Alg, _: &VerifyKey, _: &[u8], _: &[u8]) -> bool {
        false
    }
}
