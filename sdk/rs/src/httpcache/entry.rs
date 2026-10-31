//! The on-store JSON envelope for a cached response.
//!
//! Pinned by `contracts/httpcache-v1/entry.json`. The envelope is
//! language-neutral, not a Rust-specific dump: every port must read and
//! write byte-identical bytes so they can share one kv backend.

use std::collections::BTreeMap;

use base64::engine::general_purpose::STANDARD;
use base64::Engine as _;
use serde::{Deserialize, Deserializer, Serialize, Serializer};

use super::header::{canonical_key, is_framing_header, Headers};
use super::Response;

/// A cached response as persisted.
///
/// Field order is load-bearing: the contract requires `status`, `headers`,
/// `body` in that order so envelopes are byte-comparable across ports.
/// `serde`'s derived `Serialize` emits declaration order, so the
/// declaration below *is* the wire order — do not reorder these fields.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub(super) struct Entry {
    /// HTTP status code.
    #[serde(default)]
    pub status: u16,

    /// Header map, canonical-cased keys to ordered value lists.
    ///
    /// `BTreeMap` gives deterministic key order, which byte-exactness
    /// needs; Go's `encoding/json` sorts map keys for the same reason.
    /// `null` decodes to an empty map rather than erroring.
    #[serde(default, deserialize_with = "deserialize_headers")]
    pub headers: BTreeMap<String, Vec<String>>,

    /// Response body.
    ///
    /// serde's default for `Vec<u8>` is an array of numbers
    /// (`[104,105]`), which no other port can read. These overrides emit
    /// and accept a standard-alphabet, padded base64 *string* instead,
    /// matching Go `encoding/json`'s `[]byte` behaviour.
    #[serde(
        default,
        serialize_with = "serialize_body",
        deserialize_with = "deserialize_body"
    )]
    pub body: Vec<u8>,
}

/// Serializes bytes as standard base64 with padding.
fn serialize_body<S: Serializer>(body: &[u8], ser: S) -> Result<S::Ok, S::Error> {
    ser.serialize_str(&STANDARD.encode(body))
}

/// Decodes a standard-alphabet, padded base64 string.
///
/// `null` yields an empty body. The URL-safe alphabet and unpadded input
/// are rejected, as the contract requires: `STANDARD` enforces both.
fn deserialize_body<'de, D: Deserializer<'de>>(de: D) -> Result<Vec<u8>, D::Error> {
    let encoded = Option::<String>::deserialize(de)?;
    match encoded {
        None => Ok(Vec::new()),
        Some(s) => STANDARD.decode(s).map_err(serde::de::Error::custom),
    }
}

/// Decodes the header map, treating `null` as an empty set.
fn deserialize_headers<'de, D: Deserializer<'de>>(
    de: D,
) -> Result<BTreeMap<String, Vec<String>>, D::Error> {
    Ok(Option::deserialize(de)?.unwrap_or_default())
}

impl Entry {
    /// Builds an envelope from a response, dropping framing headers.
    ///
    /// The strip is case-insensitive and does not mutate `resp`: framing
    /// headers describe the original transfer's wire framing, and the
    /// stored bytes are authoritative for length.
    pub(super) fn from_response(resp: &Response) -> Self {
        let mut headers = BTreeMap::new();
        for (name, values) in resp.headers.iter() {
            if is_framing_header(name) {
                continue;
            }
            headers
                .entry(canonical_key(name))
                .or_insert_with(Vec::new)
                .extend(values.iter().cloned());
        }
        Self {
            status: resp.status,
            headers,
            body: resp.body.clone(),
        }
    }

    /// Reconstructs a response, recomputing content length from the body.
    pub(super) fn into_response(self) -> Response {
        let mut headers = Headers::new();
        for (name, values) in self.headers {
            for value in values {
                headers.append(&name, value);
            }
        }
        Response {
            status: self.status,
            content_length: self.body.len() as u64,
            headers,
            body: self.body,
        }
    }
}
