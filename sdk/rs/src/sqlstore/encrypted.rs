//! At-rest encryption wrapper around [`Store`].

use serde::de::DeserializeOwned;
use serde::Serialize;

use super::crypto::{decrypt, derive_key, encrypt};
use super::{Store, StoreError};

/// A [`Store`] with transparent at-rest value encryption.
///
/// Values are JSON-serialised, sealed with NaCl secretbox, then written to
/// the inner store as a byte array. Keys stay in plaintext — they are the
/// primary key of the `kv` table and must remain queryable — so callers
/// must not put secrets in key names.
///
/// # Example
///
/// ```
/// use hop_top_kit::sqlstore::{EncryptedStore, Options, Store};
///
/// let inner = Store::open(":memory:", Options::new()).unwrap();
/// let store = EncryptedStore::from_seed(inner, &[7u8; 32]);
///
/// store.put("token", &"s3cret").unwrap();
/// assert_eq!(store.get::<String>("token").unwrap().unwrap(), "s3cret");
/// ```
#[derive(Debug)]
pub struct EncryptedStore {
    inner: Store,
    key: [u8; 32],
}

impl EncryptedStore {
    /// Wraps `inner`, deriving the symmetric key from an Ed25519 private
    /// key `seed` via [`derive_key`].
    ///
    /// This is the counterpart of Go's
    /// `NewEncryptedStore(inner, keypair)`: Go passes the keypair and calls
    /// `PrivateKey.Seed()` internally, whereas the Rust SDK does not carry
    /// an Ed25519 keypair type, so the seed is passed directly. The derived
    /// key is identical for the same seed.
    pub fn from_seed(inner: Store, seed: &[u8; 32]) -> Self {
        Self {
            inner,
            key: derive_key(seed),
        }
    }

    /// Wraps `inner` with an already-derived 32-byte symmetric key.
    ///
    /// Use when the key comes from somewhere other than an Ed25519 seed.
    pub fn from_key(inner: Store, key: [u8; 32]) -> Self {
        Self { inner, key }
    }

    /// Serialises `value`, encrypts it, and stores it under `key`.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Json`] when serialisation fails,
    /// [`StoreError::Encrypt`] when sealing fails, and
    /// [`StoreError::Query`] when the write fails.
    pub fn put<T: Serialize + ?Sized>(&self, key: &str, value: &T) -> Result<(), StoreError> {
        let plain = serde_json::to_vec(value)?;
        let sealed = encrypt(&self.key, &plain)?;
        // Serialised as a JSON byte array so the inner store stays a plain
        // JSON kv table with no bespoke column type.
        self.inner.put(key, &sealed)
    }

    /// Reads, decrypts, and deserialises the value stored under `key`.
    ///
    /// Returns `Ok(None)` when the key is absent or has expired.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Decrypt`] when authentication fails — most
    /// commonly because the value was written under a different key.
    pub fn get<T: DeserializeOwned>(&self, key: &str) -> Result<Option<T>, StoreError> {
        let Some(sealed) = self.inner.get::<Vec<u8>>(key)? else {
            return Ok(None);
        };
        let plain = decrypt(&self.key, &sealed)?;
        Ok(Some(serde_json::from_slice(&plain)?))
    }

    /// Removes the entry stored under `key`.
    ///
    /// # Errors
    ///
    /// Returns [`StoreError::Query`] if the delete fails.
    pub fn delete(&self, key: &str) -> Result<bool, StoreError> {
        self.inner.delete(key)
    }

    /// Borrows the wrapped plaintext store.
    ///
    /// Reads through this handle return ciphertext, not values.
    pub fn inner(&self) -> &Store {
        &self.inner
    }

    /// Consumes the wrapper, returning the inner store.
    pub fn into_inner(self) -> Store {
        self.inner
    }
}
