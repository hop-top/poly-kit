//! Local-first cryptographic identity, a port of Go `go/core/identity`.
//! Gated behind the `identity` feature.
//!
//! - [`Keypair`]: Ed25519, built from a seed or the OS RNG; PKCS#8 and
//!   SPKI PEM both ways ([`parse_private_key`], [`parse_public_key`]).
//! - JWT: [`Keypair::sign_jwt`] and [`verify_jwt`] /
//!   [`verify_jwt_with_options`] (EdDSA, `kid` = [`Keypair::public_key_id`]).
//! - Encryption: [`Keypair::derive_key`] (HKDF-SHA256 over the seed) feeds
//!   [`encrypt`] / [`decrypt`] (NaCl secretbox, `nonce || ciphertext`).
//! - [`Store`] / [`default_store`]: the keypair on disk, `0600`/`0644`
//!   in a `0700` directory (native only).
//!
//! The key-derivation bytes are the contract `contracts/identity-v1`;
//! `tests/identity_conformance.rs` asserts them.
//!
//! # wasm32
//!
//! The core is wasm-safe. wasm32-unknown-unknown has no OS random source
//! and no clock, so the forms that need them are compiled off it, and the
//! caller injects instead: [`Keypair::from_seed`], [`encrypt_with_nonce`],
//! [`verify_jwt_at`] and [`verify_jwt_with_options_at`].
//!
//! # Example
//!
//! ```
//! use hop_top_kit::identity::{decrypt, encrypt_with_nonce, verify_jwt_at, Claims, Keypair};
//!
//! let kp = Keypair::from_seed(&[7u8; 32]);
//! let token = kp.sign_jwt(Claims { subject: kp.public_key_id(), issued_at: 100, ..Claims::default() });
//! let claims = verify_jwt_at(&token, &kp.public_key(), 100).unwrap();
//! assert_eq!(claims.subject, kp.public_key_id());
//!
//! let key = kp.derive_key();
//! let sealed = encrypt_with_nonce(&key, &[1u8; 24], b"at rest");
//! assert_eq!(decrypt(&key, &sealed).unwrap(), b"at rest");
//! ```

mod encrypt;
mod error;
mod jwt;
mod keypair;
#[cfg(not(target_arch = "wasm32"))]
mod store;

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub use encrypt::encrypt;
pub use encrypt::{
    decrypt, derive_key, encrypt_with_nonce, KEY_DERIVATION_INFO, NONCE_SIZE, TAG_SIZE,
};
pub use error::IdentityError;
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub use jwt::{verify_jwt, verify_jwt_with_options};
pub use jwt::{verify_jwt_at, verify_jwt_with_options_at, Audience, Claims, VerifyOptions};
pub use keypair::{parse_private_key, parse_public_key, Keypair, PublicKey};
#[cfg(not(target_arch = "wasm32"))]
pub use store::{default_store, Store};
