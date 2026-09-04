//! Multi-value HTTP header map with canonical-cased storage.
//!
//! The envelope contract stores keys in HTTP canonical form and preserves
//! value order within a key, while lookup stays case-insensitive. A flat
//! string map would lose duplicate `Set-Cookie` / `Vary` values, so values
//! are always lists.

use std::collections::BTreeMap;

/// Headers whose values describe the original transfer's wire framing.
///
/// Stripped at encode time: the transport has already dechunked the body,
/// so the stored bytes are authoritative for length. Carrying these into a
/// reconstructed response produces states HTTP forbids, such as a chunked
/// `Transfer-Encoding` beside an explicit content length.
pub(super) const FRAMING_HEADERS: [&str; 3] = ["Content-Length", "Transfer-Encoding", "Connection"];

/// Reports whether `name` is a framing header, ignoring case.
pub(super) fn is_framing_header(name: &str) -> bool {
    FRAMING_HEADERS.iter().any(|h| h.eq_ignore_ascii_case(name))
}

/// Converts a header name to HTTP canonical casing.
///
/// Each dash-separated token gets an upper-case first byte and lower-case
/// remainder: `content-type` becomes `Content-Type`, `ETAG` becomes
/// `Etag`.
pub(super) fn canonical_key(name: &str) -> String {
    let mut out = String::with_capacity(name.len());
    let mut at_start = true;
    for c in name.chars() {
        if at_start {
            out.extend(c.to_uppercase());
        } else {
            out.extend(c.to_lowercase());
        }
        at_start = c == '-';
    }
    out
}

/// An ordered, multi-value header map.
///
/// Keys are stored canonical-cased and iterate in sorted order, which is
/// what byte-exact envelope comparison requires.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Headers {
    inner: BTreeMap<String, Vec<String>>,
}

impl Headers {
    /// Returns an empty header map.
    #[must_use]
    pub fn new() -> Self {
        Self::default()
    }

    /// Appends `value` under `name`, keeping any existing values.
    pub fn append(&mut self, name: &str, value: impl Into<String>) {
        self.inner
            .entry(canonical_key(name))
            .or_default()
            .push(value.into());
    }

    /// Replaces every value under `name` with `value`.
    pub fn set(&mut self, name: &str, value: impl Into<String>) {
        self.inner.insert(canonical_key(name), vec![value.into()]);
    }

    /// Returns the first value for `name`, matched case-insensitively.
    #[must_use]
    pub fn get(&self, name: &str) -> Option<&str> {
        self.values(name).first().map(String::as_str)
    }

    /// Returns every value for `name`, matched case-insensitively.
    #[must_use]
    pub fn values(&self, name: &str) -> &[String] {
        self.inner
            .get(&canonical_key(name))
            .map_or(&[], Vec::as_slice)
    }

    /// Iterates over `(name, values)` pairs in sorted key order.
    pub fn iter(&self) -> impl Iterator<Item = (&str, &Vec<String>)> {
        self.inner.iter().map(|(k, v)| (k.as_str(), v))
    }

    /// Reports whether the map holds no headers.
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.inner.is_empty()
    }
}

impl<K: AsRef<str>, V: Into<String>> FromIterator<(K, V)> for Headers {
    fn from_iter<I: IntoIterator<Item = (K, V)>>(iter: I) -> Self {
        let mut headers = Self::new();
        for (name, value) in iter {
            headers.append(name.as_ref(), value);
        }
        headers
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn canonicalizes_keys() {
        assert_eq!(canonical_key("content-type"), "Content-Type");
        assert_eq!(canonical_key("ETAG"), "Etag");
        assert_eq!(canonical_key("x-multi-part-name"), "X-Multi-Part-Name");
        assert_eq!(canonical_key(""), "");
    }

    #[test]
    fn lookup_is_case_insensitive() {
        let mut h = Headers::new();
        h.set("Content-Type", "text/plain");
        assert_eq!(h.get("content-type"), Some("text/plain"));
        assert_eq!(h.get("CONTENT-TYPE"), Some("text/plain"));
        assert_eq!(h.get("missing"), None);
    }

    #[test]
    fn append_preserves_order() {
        let mut h = Headers::new();
        h.append("X-Multi", "a");
        h.append("x-multi", "b");
        assert_eq!(h.values("X-Multi"), ["a", "b"]);
    }

    #[test]
    fn set_replaces_all_values() {
        let mut h = Headers::new();
        h.append("X", "a");
        h.append("X", "b");
        h.set("X", "c");
        assert_eq!(h.values("X"), ["c"]);
    }

    #[test]
    fn framing_headers_match_ignoring_case() {
        assert!(is_framing_header("content-length"));
        assert!(is_framing_header("TRANSFER-ENCODING"));
        assert!(is_framing_header("Connection"));
        assert!(!is_framing_header("X-Keep"));
    }
}
