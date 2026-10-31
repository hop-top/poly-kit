//! Key derivation and NaCl secretbox, the wire format Go writes:
//! `nonce(24) || tag(16) || body`.
//!
//! [`Keypair::derive_key`] is deterministic and pinned by
//! `contracts/identity-v1`; the encrypted frames carry a random nonce, so
//! they are covered by round trips rather than fixed vectors.

use crypto_secretbox::aead::{Aead, KeyInit};
use crypto_secretbox::XSalsa20Poly1305;
use hkdf::Hkdf;
use sha2::Sha256;

use super::{IdentityError, Keypair};

/// Domain-separation string fed to HKDF as `info`. Changing it makes every
/// key derived so far unreproducible, in every language.
pub const KEY_DERIVATION_INFO: &[u8] = b"kit-identity-encryption-v1";

/// Secretbox nonce length, in bytes.
pub const NONCE_SIZE: usize = 24;

/// Poly1305 tag length, in bytes.
pub const TAG_SIZE: usize = 16;

/// HKDF-SHA256 over an Ed25519 seed (empty salt, [`KEY_DERIVATION_INFO`]),
/// expanded to 32 bytes. Takes the seed, never the 64-byte expanded key:
/// Go feeds `PrivateKey.Seed()`.
pub fn derive_key(seed: &[u8; 32]) -> [u8; 32] {
    let hk = Hkdf::<Sha256>::new(None, seed);
    let mut key = [0u8; 32];
    hk.expand(KEY_DERIVATION_INFO, &mut key)
        .expect("32 bytes is within HKDF-SHA256 output limits");
    key
}

impl Keypair {
    /// The symmetric key this keypair encrypts with; see [`derive_key`].
    pub fn derive_key(&self) -> [u8; 32] {
        derive_key(&self.seed())
    }
}

/// Seals `plaintext` under `key` with the caller's `nonce`, returning
/// `nonce || ciphertext`. A nonce must never repeat under one key: on
/// wasm32 draw it from the host CSPRNG; natively prefer [`encrypt`].
pub fn encrypt_with_nonce(key: &[u8; 32], nonce: &[u8; NONCE_SIZE], plaintext: &[u8]) -> Vec<u8> {
    let cipher = XSalsa20Poly1305::new(key.into());
    let sealed = cipher
        .encrypt(nonce.into(), plaintext)
        .expect("secretbox sealing is infallible");
    let mut out = Vec::with_capacity(NONCE_SIZE + sealed.len());
    out.extend_from_slice(nonce);
    out.extend_from_slice(&sealed);
    out
}

/// Seals `plaintext` under `key` with a fresh random nonce. Compiled off
/// wasm32-unknown-unknown; there, use [`encrypt_with_nonce`].
///
/// # Errors
///
/// [`IdentityError::Nonce`] when the OS random source fails.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub fn encrypt(key: &[u8; 32], plaintext: &[u8]) -> Result<Vec<u8>, IdentityError> {
    let mut nonce = [0u8; NONCE_SIZE];
    getrandom::fill(&mut nonce).map_err(|e| IdentityError::Nonce(e.to_string()))?;
    Ok(encrypt_with_nonce(key, &nonce, plaintext))
}

/// Opens a `nonce || ciphertext` frame.
///
/// # Errors
///
/// [`IdentityError::CiphertextTooShort`] under 40 bytes;
/// [`IdentityError::DecryptFailed`] on a wrong key or tampered data.
pub fn decrypt(key: &[u8; 32], ciphertext: &[u8]) -> Result<Vec<u8>, IdentityError> {
    if ciphertext.len() < NONCE_SIZE + TAG_SIZE {
        return Err(IdentityError::CiphertextTooShort);
    }
    let (nonce, sealed) = ciphertext.split_at(NONCE_SIZE);
    XSalsa20Poly1305::new(key.into())
        .decrypt(nonce.into(), sealed)
        .map_err(|_| IdentityError::DecryptFailed)
}
