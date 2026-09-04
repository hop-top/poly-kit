//! Minimal RFC 3986 URL type reproducing Go `net/url` re-serialization.
//!
//! # Why not the `url` crate
//!
//! The cache key hashes the *re-serialized* URL, so the serializer's
//! normalization behaviour is part of the cross-language wire contract
//! pinned by `contracts/httpcache-v1/keying.json`. The `url` crate
//! implements the WHATWG URL Standard, which normalizes aggressively;
//! Go's `net/url` implements RFC 3986, which does not. They disagree on
//! six of the fixture's vectors:
//!
//! | input                       | contract          | `url` crate    |
//! |-----------------------------|-------------------|----------------|
//! | `https://example.com`       | no trailing `/`   | adds `/`       |
//! | `https://example.com:443/a` | keeps `:443`      | strips it      |
//! | `http://example.com:80/a`   | keeps `:80`       | strips it      |
//! | `https://EXAMPLE.com/a`     | keeps host case   | lower-cases    |
//! | `?q=café`                   | raw, non-ASCII    | percent-encodes|
//! | `/./a/../b`                 | keeps dot segments| resolves to `/b`|
//!
//! Those are not incidental: each is an explicit `normalization` flag in
//! keying.json. A normalizing parser cannot express this contract, so the
//! parse/serialize pair is implemented here against the fixtures.
//!
//! Scope is deliberately narrow — enough to key a cache, not a
//! general-purpose URL library.

use std::fmt::Write as _;

/// Bytes emitted verbatim in a path component.
///
/// Go's `shouldEscape(c, encodePath)`: unreserved (`ALPHA / DIGIT / -._~`)
/// plus the sub-delims it permits in a path (`$&+,;=`) plus `:@/`.
/// Verified byte-exhaustively against `net/url` for all 256 values.
const PATH_KEEP: &[u8] =
    b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~$&+,;=:@/";

/// Additional bytes tolerated verbatim inside an already-parsed raw path.
///
/// Go re-emits `RawPath` untouched when it validly encodes `Path`, so a
/// source spelling containing these survives even though constructing the
/// same path from its decoded form would escape them.
const PATH_TOLERATED: &[u8] = b"!'()*[]";

/// A parsed URL, retaining enough of the source spelling to re-serialize
/// it the way Go's `URL.String()` does.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Url {
    scheme: String,
    /// `user:pw` exactly as written, without the `@`.
    userinfo: Option<String>,
    /// Host and optional `:port`, case and default port preserved.
    host: String,
    /// Decoded path bytes.
    path: Vec<u8>,
    /// Source path spelling, kept when it validly encodes `path`.
    raw_path: Option<String>,
    /// Query string, verbatim — never re-encoded.
    query: Option<String>,
    /// Fragment, verbatim.
    fragment: Option<String>,
}

/// Errors produced while parsing a URL.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum UrlError {
    /// No `:` separating a scheme, or an empty/invalid scheme.
    #[error("url: missing or invalid scheme in {0:?}")]
    MissingScheme(String),

    /// The URL has no `//` authority section.
    #[error("url: missing authority in {0:?}")]
    MissingAuthority(String),

    /// A `%` sequence was truncated or used non-hex digits.
    #[error("url: invalid percent-escape {0:?}")]
    InvalidEscape(String),

    /// A control byte appeared where the syntax forbids it.
    #[error("url: invalid control character in {0:?}")]
    InvalidControlCharacter(String),
}

impl Url {
    /// Parses an absolute URL.
    ///
    /// # Errors
    ///
    /// Returns [`UrlError`] when the scheme or authority is absent, a
    /// percent-escape is malformed, or a control character appears.
    pub fn parse(raw: &str) -> Result<Self, UrlError> {
        if raw.bytes().any(|b| b < 0x20 || b == 0x7f) {
            return Err(UrlError::InvalidControlCharacter(raw.to_string()));
        }

        // scheme ":" — the scheme must be ALPHA *( ALPHA / DIGIT / +-. ).
        let colon = raw
            .find(':')
            .ok_or_else(|| UrlError::MissingScheme(raw.to_string()))?;
        let scheme = &raw[..colon];
        if scheme.is_empty()
            || !scheme.starts_with(|c: char| c.is_ascii_alphabetic())
            || !scheme
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '+' | '-' | '.'))
        {
            return Err(UrlError::MissingScheme(raw.to_string()));
        }
        let rest = &raw[colon + 1..];

        let rest = rest
            .strip_prefix("//")
            .ok_or_else(|| UrlError::MissingAuthority(raw.to_string()))?;

        // Split off fragment, then query. Both are kept verbatim; the
        // fixture pins that neither is stripped nor re-encoded.
        let (rest, fragment) = match rest.split_once('#') {
            Some((head, frag)) => (head, Some(frag.to_string())),
            None => (rest, None),
        };
        let (authority_path, query) = match rest.split_once('?') {
            Some((head, q)) => (head, Some(q.to_string())),
            None => (rest, None),
        };

        // Authority ends at the first '/'; the remainder is the path.
        let (authority, raw_path) = match authority_path.find('/') {
            Some(i) => (&authority_path[..i], &authority_path[i..]),
            None => (authority_path, ""),
        };

        // Userinfo is delimited by the LAST '@', so a '@' inside a
        // password does not truncate the host.
        let (userinfo, host) = match authority.rfind('@') {
            Some(i) => (Some(authority[..i].to_string()), &authority[i + 1..]),
            None => (None, authority),
        };

        let path = percent_decode(raw_path)
            .ok_or_else(|| UrlError::InvalidEscape(raw_path.to_string()))?;

        // Retain the source spelling only when it round-trips; otherwise
        // serialization re-escapes from the decoded bytes.
        let raw_path = if raw_path.as_bytes() != path.as_slice() && valid_encoded(raw_path, &path) {
            Some(raw_path.to_string())
        } else {
            None
        };

        Ok(Self {
            scheme: scheme.to_string(),
            userinfo,
            host: host.to_string(),
            path,
            raw_path,
            query,
            fragment,
        })
    }

    /// Re-serializes the URL as Go's `URL.String()` does.
    ///
    /// Applies no normalization: host case, default ports, query order,
    /// dot segments, the empty-query marker, fragments and userinfo all
    /// survive. Only the path component is percent-encoded.
    #[must_use]
    pub fn as_string(&self) -> String {
        let mut out = String::with_capacity(self.host.len() + self.path.len() + 16);
        out.push_str(&self.scheme);
        out.push_str("://");
        if let Some(user) = &self.userinfo {
            out.push_str(user);
            out.push('@');
        }
        out.push_str(&self.host);
        out.push_str(&self.escaped_path());
        if let Some(q) = &self.query {
            out.push('?');
            out.push_str(q);
        }
        if let Some(f) = &self.fragment {
            out.push('#');
            out.push_str(f);
        }
        out
    }

    /// The path as it appears on the wire.
    ///
    /// Mirrors Go's `EscapedPath`: emit the source spelling when it is a
    /// valid encoding of the decoded path, otherwise escape the decoded
    /// bytes.
    fn escaped_path(&self) -> String {
        match &self.raw_path {
            Some(raw) => raw.clone(),
            None => escape_path(&self.path),
        }
    }
}

/// Decodes `%XX` escapes, returning `None` on a malformed sequence.
fn percent_decode(s: &str) -> Option<Vec<u8>> {
    let bytes = s.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            let hi = bytes.get(i + 1).copied().and_then(hex_value)?;
            let lo = bytes.get(i + 2).copied().and_then(hex_value)?;
            out.push(hi << 4 | lo);
            i += 3;
        } else {
            out.push(bytes[i]);
            i += 1;
        }
    }
    Some(out)
}

/// Value of a single ASCII hex digit.
fn hex_value(c: u8) -> Option<u8> {
    match c {
        b'0'..=b'9' => Some(c - b'0'),
        b'a'..=b'f' => Some(c - b'a' + 10),
        b'A'..=b'F' => Some(c - b'A' + 10),
        _ => None,
    }
}

/// Reports whether `raw` is a well-formed path encoding of `path`.
///
/// Requires that every literal byte is one the path grammar admits, so a
/// raw spelling containing an unescaped space or control byte is rejected
/// and the decoded form is re-escaped instead.
fn valid_encoded(raw: &str, path: &[u8]) -> bool {
    if percent_decode(raw).as_deref() != Some(path) {
        return false;
    }
    let bytes = raw.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        let c = bytes[i];
        if c == b'%' {
            // Validated by the decode above.
            i += 3;
            continue;
        }
        if !PATH_KEEP.contains(&c) && !PATH_TOLERATED.contains(&c) {
            return false;
        }
        i += 1;
    }
    true
}

/// Percent-encodes path bytes, keeping the set Go leaves unescaped.
fn escape_path(path: &[u8]) -> String {
    let mut out = String::with_capacity(path.len());
    for &b in path {
        if PATH_KEEP.contains(&b) {
            out.push(b as char);
        } else {
            let _ = write!(out, "%{b:02X}");
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Round-trips that the keying fixture does not itself cover, kept
    /// here so the parser is exercised beyond the contract vectors.
    #[test]
    fn reserializes_without_normalizing() {
        for (input, want) in [
            ("https://example.com/a", "https://example.com/a"),
            ("https://example.com", "https://example.com"),
            ("https://example.com/", "https://example.com/"),
            ("https://EXAMPLE.com:443/A", "https://EXAMPLE.com:443/A"),
            (
                "https://example.com/./a/../b",
                "https://example.com/./a/../b",
            ),
            ("https://example.com/a?", "https://example.com/a?"),
            ("https://example.com/a#f", "https://example.com/a#f"),
        ] {
            let got = Url::parse(input).expect("parse").as_string();
            assert_eq!(got, want, "input {input:?}");
        }
    }

    #[test]
    fn percent_encodes_path_but_not_query() {
        let u = Url::parse("https://example.com/café?q=café").expect("parse");
        assert_eq!(u.as_string(), "https://example.com/caf%C3%A9?q=café");
    }

    #[test]
    fn preserves_existing_path_escapes() {
        for input in [
            "https://example.com/a%2Fb",
            "https://example.com/a%2fb",
            "https://example.com/a%41b",
            "https://example.com/%7Ea",
            "https://example.com/%2521",
        ] {
            assert_eq!(Url::parse(input).expect("parse").as_string(), input);
        }
    }

    #[test]
    fn escapes_literal_space_in_path() {
        let u = Url::parse("https://example.com/a b").expect("parse");
        assert_eq!(u.as_string(), "https://example.com/a%20b");
    }

    #[test]
    fn userinfo_splits_on_last_at() {
        let u = Url::parse("https://user:p@w@example.com/a").expect("parse");
        assert_eq!(u.as_string(), "https://user:p@w@example.com/a");
    }

    #[test]
    fn rejects_malformed_input() {
        assert!(matches!(
            Url::parse("example.com/a"),
            Err(UrlError::MissingScheme(_))
        ));
        assert!(matches!(
            Url::parse("https:example.com"),
            Err(UrlError::MissingAuthority(_))
        ));
        assert!(matches!(
            Url::parse("https://example.com/a%zz"),
            Err(UrlError::InvalidEscape(_))
        ));
        assert!(matches!(
            Url::parse("https://example.com/a%2"),
            Err(UrlError::InvalidEscape(_))
        ));
        assert!(matches!(
            Url::parse("https://example.com/a\u{7f}"),
            Err(UrlError::InvalidControlCharacter(_))
        ));
    }
}
