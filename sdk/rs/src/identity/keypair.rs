//! Ed25519 keypair: seed construction, fingerprint, PKCS#8 / SPKI PEM.

use std::fmt;

use ed25519_dalek::pkcs8::spki::der::pem::{self, LineEnding};
use ed25519_dalek::pkcs8::{DecodePrivateKey, DecodePublicKey, EncodePublicKey};
use ed25519_dalek::{Signer, SigningKey, VerifyingKey};
use sha2::{Digest, Sha256};

use super::IdentityError;

/// A 32-byte Ed25519 public key.
pub type PublicKey = [u8; 32];

/// An Ed25519 signing keypair. The private half is wiped on drop and
/// never printed by `Debug`.
#[derive(Clone)]
pub struct Keypair {
    signing: SigningKey,
}

impl fmt::Debug for Keypair {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Keypair")
            .field("public_key_id", &self.public_key_id())
            .finish_non_exhaustive()
    }
}

impl Keypair {
    /// Builds the keypair an Ed25519 seed determines (RFC 8032 §5.1.5).
    ///
    /// This is the injection point for randomness: on wasm32, draw 32
    /// bytes from the host's CSPRNG and pass them here.
    pub fn from_seed(seed: &[u8; 32]) -> Keypair {
        Keypair {
            signing: SigningKey::from_bytes(seed),
        }
    }

    /// A new random keypair from the OS random source. Compiled off
    /// wasm32-unknown-unknown; there, use [`Keypair::from_seed`].
    ///
    /// # Errors
    ///
    /// [`IdentityError::Generate`] when the OS random source fails.
    #[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
    pub fn generate() -> Result<Keypair, IdentityError> {
        let mut seed = [0u8; 32];
        getrandom::fill(&mut seed).map_err(|e| IdentityError::Generate(e.to_string()))?;
        let kp = Keypair::from_seed(&seed);
        seed.fill(0);
        Ok(kp)
    }

    /// The public key.
    pub fn public_key(&self) -> PublicKey {
        self.signing.verifying_key().to_bytes()
    }

    /// The 32-byte private seed, the input of [`Keypair::derive_key`].
    pub fn seed(&self) -> [u8; 32] {
        self.signing.to_bytes()
    }

    /// Short hex fingerprint: the first 16 hex characters (8 bytes) of
    /// SHA-256 over the public key. Used as the JWT `kid`.
    pub fn public_key_id(&self) -> String {
        let digest = Sha256::digest(self.public_key());
        let mut id = String::with_capacity(16);
        for b in &digest[..8] {
            id.push(char::from(HEX[usize::from(b >> 4)]));
            id.push(char::from(HEX[usize::from(b & 0xf)]));
        }
        id
    }

    /// Signs `msg` (Ed25519, pure).
    pub fn sign(&self, msg: &[u8]) -> [u8; 64] {
        self.signing.sign(msg).to_bytes()
    }

    /// The PEM `PUBLIC KEY` block (SubjectPublicKeyInfo).
    ///
    /// # Errors
    ///
    /// [`IdentityError::Marshal`] if encoding fails.
    pub fn marshal_public_key(&self) -> Result<String, IdentityError> {
        self.signing
            .verifying_key()
            .to_public_key_pem(LineEnding::LF)
            .map_err(|e| IdentityError::Marshal(e.to_string()))
    }

    /// The PEM `PRIVATE KEY` block (PKCS#8).
    ///
    /// # Errors
    ///
    /// [`IdentityError::Marshal`] if encoding fails.
    pub fn marshal_private_key(&self) -> Result<String, IdentityError> {
        // PKCS#8 v1 (version 0, seed only), the bytes Go's x509 writes.
        // The encoder's own `to_pkcs8_pem` emits v2 and appends the
        // public key.
        let mut der = Vec::with_capacity(PKCS8_PREFIX.len() + 32);
        der.extend_from_slice(&PKCS8_PREFIX);
        der.extend_from_slice(&self.seed());
        let pem = pem::encode_string("PRIVATE KEY", LineEnding::LF, &der)
            .map_err(|e| IdentityError::Marshal(e.to_string()));
        der.fill(0);
        pem
    }
}

/// DER of `PrivateKeyInfo { version 0, algorithm id-Ed25519, privateKey
/// OCTET STRING { OCTET STRING (32) } }` up to the seed.
const PKCS8_PREFIX: [u8; 16] = [
    0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
];

const HEX: &[u8; 16] = b"0123456789abcdef";

/// Cuts the first PEM block out of `data`, like Go's `pem.Decode`: text
/// before the `-----BEGIN` line is skipped.
fn pem_block(data: &[u8]) -> Result<&str, IdentityError> {
    let s = std::str::from_utf8(data).map_err(|_| IdentityError::NoPem)?;
    s.find("-----BEGIN ")
        .map(|i| &s[i..])
        .ok_or(IdentityError::NoPem)
}

/// Loads a public key from PEM (SubjectPublicKeyInfo, Ed25519 only).
///
/// # Errors
///
/// [`IdentityError::NoPem`] without a PEM block,
/// [`IdentityError::ParsePublicKey`] when it is not an Ed25519 key.
pub fn parse_public_key(data: &[u8]) -> Result<PublicKey, IdentityError> {
    let block = pem_block(data)?;
    VerifyingKey::from_public_key_pem(block)
        .map(|k| k.to_bytes())
        .map_err(|e| IdentityError::ParsePublicKey(e.to_string()))
}

/// Loads a keypair from PEM (PKCS#8, Ed25519 only).
///
/// # Errors
///
/// [`IdentityError::NoPem`] without a PEM block,
/// [`IdentityError::ParsePrivateKey`] when it is not an Ed25519 key.
pub fn parse_private_key(data: &[u8]) -> Result<Keypair, IdentityError> {
    let block = pem_block(data)?;
    SigningKey::from_pkcs8_pem(block)
        .map(|signing| Keypair { signing })
        .map_err(|e| IdentityError::ParsePrivateKey(e.to_string()))
}
