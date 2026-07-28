//! Symmetric key derivation and at-rest encryption primitives.
//!
//! Mirrors the Go `core/identity` encryption surface, which is the only
//! part of that package [`super::EncryptedStore`] depends on:
//!
//! - [`derive_key`] — HKDF-SHA256 over an Ed25519 private key seed.
//! - [`encrypt`] / [`decrypt`] — NaCl secretbox with a random 24-byte nonce.
//!
//! # Cross-language contract
//!
//! [`derive_key`] is deterministic, so it is a wire contract in its own
//! right: a store encrypted by the Go implementation must be readable here
//! and vice versa. The shared vectors live in
//! `contracts/identity-v1/derive-key.json` and are asserted by
//! `tests/sqlstore.rs`.
//!
//! Only the seed feeds the KDF, never the full 64-byte Ed25519 expanded
//! private key — the Go side calls `PrivateKey.Seed()`, which is the first
//! 32 bytes. Passing the whole 64-byte key would silently derive a
//! different key, so [`derive_key`] takes exactly `[u8; 32]`.

use crypto_secretbox::aead::{Aead, KeyInit, OsRng};
use crypto_secretbox::{AeadCore, XSalsa20Poly1305};
use hkdf::Hkdf;
use sha2::Sha256;

use super::StoreError;

/// Domain-separation string fed to HKDF as `info`.
///
/// Changing this value invalidates every key derived so far, in every
/// language. It is versioned for exactly that reason.
pub const KEY_DERIVATION_INFO: &[u8] = b"kit-identity-encryption-v1";

/// Length of the NaCl secretbox nonce, in bytes.
pub const NONCE_SIZE: usize = 24;

/// Length of the Poly1305 authentication tag prepended to the ciphertext.
pub const TAG_SIZE: usize = 16;

/// Derives a 32-byte symmetric key from an Ed25519 private key seed.
///
/// HKDF-SHA256 with an empty salt and [`KEY_DERIVATION_INFO`] as `info`.
/// Deterministic — the same seed always yields the same key, across
/// languages.
pub fn derive_key(seed: &[u8; 32]) -> [u8; 32] {
    let hk = Hkdf::<Sha256>::new(None, seed);
    let mut key = [0u8; 32];
    // Only errors when the requested length exceeds 255 * HashLen;
    // 32 bytes is always valid for SHA-256.
    hk.expand(KEY_DERIVATION_INFO, &mut key)
        .expect("32 bytes is within HKDF-SHA256 output limits");
    key
}

/// Encrypts `plaintext` under `key` using NaCl secretbox and a fresh
/// random nonce.
///
/// Returns `nonce || ciphertext`, matching the Go wire layout.
///
/// # Errors
///
/// Returns [`StoreError::Encrypt`] if the underlying AEAD fails.
pub fn encrypt(key: &[u8; 32], plaintext: &[u8]) -> Result<Vec<u8>, StoreError> {
    let cipher = XSalsa20Poly1305::new(key.into());
    let nonce = XSalsa20Poly1305::generate_nonce(&mut OsRng);
    let sealed = cipher
        .encrypt(&nonce, plaintext)
        .map_err(|_| StoreError::Encrypt)?;

    let mut out = Vec::with_capacity(NONCE_SIZE + sealed.len());
    out.extend_from_slice(nonce.as_slice());
    out.extend_from_slice(&sealed);
    Ok(out)
}

/// Decrypts a `nonce || ciphertext` buffer produced by [`encrypt`].
///
/// # Errors
///
/// Returns [`StoreError::CiphertextTooShort`] when the buffer cannot hold
/// a nonce plus an authentication tag, and [`StoreError::Decrypt`] when
/// authentication fails — including the case of a wrong key.
pub fn decrypt(key: &[u8; 32], ciphertext: &[u8]) -> Result<Vec<u8>, StoreError> {
    if ciphertext.len() < NONCE_SIZE + TAG_SIZE {
        return Err(StoreError::CiphertextTooShort);
    }
    let (nonce, sealed) = ciphertext.split_at(NONCE_SIZE);
    let cipher = XSalsa20Poly1305::new(key.into());
    cipher
        .decrypt(nonce.into(), sealed)
        .map_err(|_| StoreError::Decrypt)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn derive_key_is_deterministic() {
        let seed = [7u8; 32];
        assert_eq!(derive_key(&seed), derive_key(&seed));
    }

    #[test]
    fn derive_key_separates_seeds() {
        assert_ne!(derive_key(&[0u8; 32]), derive_key(&[1u8; 32]));
    }

    #[test]
    fn encrypt_decrypt_roundtrip() {
        let key = derive_key(&[3u8; 32]);
        let sealed = encrypt(&key, b"hello").unwrap();
        assert_eq!(decrypt(&key, &sealed).unwrap(), b"hello");
    }

    #[test]
    fn encrypt_uses_fresh_nonce() {
        let key = derive_key(&[3u8; 32]);
        let a = encrypt(&key, b"same").unwrap();
        let b = encrypt(&key, b"same").unwrap();
        assert_ne!(
            a, b,
            "identical plaintexts must not produce identical bytes"
        );
    }

    #[test]
    fn decrypt_rejects_wrong_key() {
        let sealed = encrypt(&derive_key(&[3u8; 32]), b"hello").unwrap();
        assert!(matches!(
            decrypt(&derive_key(&[4u8; 32]), &sealed),
            Err(StoreError::Decrypt)
        ));
    }

    #[test]
    fn decrypt_rejects_short_buffer() {
        let key = derive_key(&[3u8; 32]);
        assert!(matches!(
            decrypt(&key, &[0u8; NONCE_SIZE]),
            Err(StoreError::CiphertextTooShort)
        ));
    }

    #[test]
    fn decrypt_rejects_tampered_ciphertext() {
        let key = derive_key(&[3u8; 32]);
        let mut sealed = encrypt(&key, b"hello").unwrap();
        let last = sealed.len() - 1;
        sealed[last] ^= 0xff;
        assert!(matches!(decrypt(&key, &sealed), Err(StoreError::Decrypt)));
    }
}
