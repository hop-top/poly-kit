//! HTTP response cache backed by a [`crate::kv`] TTL store.
//!
//! Mirrors the Go `storage/httpcache` package. Where Go decorates an
//! `http.RoundTripper`, this port models the exchange as plain data
//! ([`Request`], [`Response`]) and takes the fetch as a closure, so it
//! pulls in no HTTP client and no async runtime. Callers wire it to
//! whatever transport they already use.
//!
//! # Wire contract
//!
//! Cache keys and the stored envelope are a cross-language contract
//! pinned by the fixtures in `contracts/httpcache-v1`. Those fixtures are
//! the record, not this implementation: `tests/httpcache_contract.rs`
//! executes the same three JSON files the Go suite does, so both
//! languages are gated by one artifact.
//!
//! Caching is conservative:
//!
//! - Only `GET` requests are cached; everything else passes through.
//! - Only 2xx responses are stored.
//! - `Cache-Control: no-store` on either side opts the exchange out.
//! - A response with `Vary` is stored per variant: the field list is
//!   recorded at an index key and the body at a key derived from the
//!   request's values for those fields. `Vary: *` is never stored.
//! - A response marked `private` is never stored (the store is shared
//!   across identities); a request carrying `Authorization` is stored
//!   only if the response says `public`.
//! - An entry past its TTL is a miss.
//!
//! Cache-layer failures never surface to the caller: a read or decode
//! failure degrades to a miss, and a write failure is swallowed.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::httpcache::{Cache, Request, Response};
//! use hop_top_kit::kv::Config;
//!
//! let dir = tempfile::tempdir().unwrap();
//! let store = Config::sqlite(dir.path().join("cache.db").to_str().unwrap())
//!     .open()
//!     .unwrap();
//! let cache = Cache::new(&store);
//!
//! let req = Request::get("https://example.com/a").unwrap();
//! let mut fetches = 0;
//! let mut fetch = |_: &Request| {
//!     fetches += 1;
//!     Ok::<_, std::convert::Infallible>(Response::new(200, "hello"))
//! };
//!
//! assert_eq!(cache.fetch(&req, &mut fetch).unwrap().body_string(), "hello");
//! assert_eq!(cache.fetch(&req, &mut fetch).unwrap().body_string(), "hello");
//! assert_eq!(fetches, 1, "second call served from cache");
//! ```

mod entry;
mod header;
pub mod url;

use std::time::Duration;

use sha2::{Digest, Sha256};

use crate::kv::{KvError, TtlStore};

pub use header::Headers;
pub use url::{Url, UrlError};

use entry::Entry;

/// Default cache-key namespace, letting several callers share one backend.
///
/// Part of the cross-language contract: every port uses this default.
pub const DEFAULT_PREFIX: &str = "httpcache:";

/// Default time a stored response is served before it is treated as a miss.
pub const DEFAULT_TTL: Duration = Duration::from_secs(24 * 60 * 60);

/// An HTTP request, reduced to what caching needs.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Request {
    /// Request method, used verbatim — never case-folded.
    pub method: String,
    /// Request URL.
    pub url: Url,
    /// Request headers.
    pub headers: Headers,
}

impl Request {
    /// Builds a request for `method` and `url`.
    ///
    /// The method is stored as given: the contract hashes it verbatim, so
    /// `get` and `GET` are deliberately different cache keys.
    ///
    /// # Errors
    ///
    /// Returns [`UrlError`] when `url` cannot be parsed.
    pub fn new(method: impl Into<String>, url: &str) -> Result<Self, UrlError> {
        Ok(Self {
            method: method.into(),
            url: Url::parse(url)?,
            headers: Headers::new(),
        })
    }

    /// Builds a `GET` request for `url`.
    ///
    /// # Errors
    ///
    /// Returns [`UrlError`] when `url` cannot be parsed.
    pub fn get(url: &str) -> Result<Self, UrlError> {
        Self::new("GET", url)
    }

    /// Adds a request header, returning `self` for chaining.
    #[must_use]
    pub fn with_header(mut self, name: &str, value: impl Into<String>) -> Self {
        self.headers.append(name, value);
        self
    }
}

/// An HTTP response, reduced to what caching needs.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Response {
    /// Status code.
    pub status: u16,
    /// Response headers.
    pub headers: Headers,
    /// Body bytes.
    pub body: Vec<u8>,
    /// Body length in bytes.
    ///
    /// Always recomputed from `body` when a response is reconstructed
    /// from the store; a stored `Content-Length` header never wins.
    pub content_length: u64,
}

impl Response {
    /// Builds a response with `status` and `body` and no headers.
    #[must_use]
    pub fn new(status: u16, body: impl Into<Vec<u8>>) -> Self {
        let body = body.into();
        Self {
            status,
            headers: Headers::new(),
            content_length: body.len() as u64,
            body,
        }
    }

    /// Adds a response header, returning `self` for chaining.
    #[must_use]
    pub fn with_header(mut self, name: &str, value: impl Into<String>) -> Self {
        self.headers.append(name, value);
        self
    }

    /// The body decoded as UTF-8, lossily.
    #[must_use]
    pub fn body_string(&self) -> String {
        String::from_utf8_lossy(&self.body).into_owned()
    }
}

/// Configuration for a [`Cache`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Config {
    /// How long a stored response stays fresh.
    ///
    /// A zero duration stores entries with no expiry. It must not stamp
    /// an already-past expiry, which would make every entry a
    /// perpetual miss.
    pub ttl: Duration,
    /// Cache-key namespace. Empty restores [`DEFAULT_PREFIX`].
    pub prefix: String,
}

impl Default for Config {
    fn default() -> Self {
        Self {
            ttl: DEFAULT_TTL,
            prefix: DEFAULT_PREFIX.to_string(),
        }
    }
}

impl Config {
    /// Sets the freshness window.
    #[must_use]
    pub fn with_ttl(mut self, ttl: Duration) -> Self {
        self.ttl = ttl;
        self
    }

    /// Sets the cache-key namespace. Empty restores [`DEFAULT_PREFIX`].
    #[must_use]
    pub fn with_prefix(mut self, prefix: impl Into<String>) -> Self {
        self.prefix = prefix.into();
        self
    }

    /// The namespace actually applied, substituting the default.
    #[must_use]
    pub fn effective_prefix(&self) -> &str {
        if self.prefix.is_empty() {
            DEFAULT_PREFIX
        } else {
            &self.prefix
        }
    }
}

/// A caching layer over a [`TtlStore`].
///
/// The store is borrowed, never owned: the caller keeps its lifecycle,
/// exactly as the Go port leaves the `TTLStore` to its caller.
#[derive(Debug)]
pub struct Cache<'a, S: TtlStore> {
    store: &'a S,
    config: Config,
}

impl<'a, S: TtlStore> Cache<'a, S> {
    /// Builds a cache over `store` with the default configuration.
    pub fn new(store: &'a S) -> Self {
        Self::with_config(store, Config::default())
    }

    /// Builds a cache over `store` with an explicit configuration.
    pub fn with_config(store: &'a S, config: Config) -> Self {
        Self { store, config }
    }

    /// Derives the cache key: `prefix + hex(sha256(method + " " + url))`.
    ///
    /// This is the primary key: method-and-URL only, never header-aware,
    /// and applies no normalization of its own. Responses with `Vary` use
    /// [`Cache::vary_key`] instead. Pinned by
    /// `contracts/httpcache-v1/keying.json`.
    #[must_use]
    pub fn key(&self, req: &Request) -> String {
        let mut hasher = Sha256::new();
        hasher.update(req.method.as_bytes());
        hasher.update(b" ");
        hasher.update(req.url.as_string().as_bytes());
        let digest = hasher.finalize();

        let mut key = String::with_capacity(self.config.effective_prefix().len() + 64);
        key.push_str(self.config.effective_prefix());
        for byte in digest {
            use std::fmt::Write as _;
            let _ = write!(key, "{byte:02x}");
        }
        key
    }

    /// Derives the Vary index key: `prefix + "varyidx:" + hex(sha256(method + " " + url))`.
    ///
    /// The index records the Vary field list of a URL. Pinned by the
    /// `vary` section of `contracts/httpcache-v1/keying.json`.
    #[must_use]
    pub fn vary_index_key(&self, req: &Request) -> String {
        let pre = format!("{} {}", req.method, req.url.as_string());
        format!("{}varyidx:{}", self.config.effective_prefix(), digest(&pre))
    }

    /// Derives the variant key for the Vary `fields` (see [`vary_fields`]):
    /// `prefix + "vary:" + hex(sha256(preimage))`.
    ///
    /// The preimage is `method + " " + url`, then for each field in order
    /// `"\n" + field + ":" + values`, where values are the request's header
    /// values, each trimmed, joined with `", "`. A request without the field
    /// contributes an empty value, so "absent" is its own variant.
    #[must_use]
    pub fn vary_key(&self, req: &Request, fields: &[String]) -> String {
        let mut pre = format!("{} {}", req.method, req.url.as_string());
        for field in fields {
            let vals: Vec<&str> = req.headers.values(field).iter().map(|v| v.trim()).collect();
            pre.push('\n');
            pre.push_str(field);
            pre.push(':');
            pre.push_str(&vals.join(", "));
        }
        format!("{}vary:{}", self.config.effective_prefix(), digest(&pre))
    }

    /// Serves `req` from cache when possible, otherwise calls `fetch` and
    /// stores a cacheable response before returning it.
    ///
    /// Cache-layer failures are never surfaced: a store read or a decode
    /// failure degrades to a miss, and a write failure is swallowed. Only
    /// an error from `fetch` itself propagates.
    ///
    /// # Errors
    ///
    /// Returns whatever `fetch` returns when it fails.
    pub fn fetch<E, F>(&self, req: &Request, mut fetch: F) -> Result<Response, E>
    where
        F: FnMut(&Request) -> Result<Response, E>,
    {
        if !cacheable_request(req) {
            return fetch(req);
        }

        let key = self.key(req);
        if let Some(resp) = self.load(req, &key) {
            return Ok(resp);
        }

        let resp = fetch(req)?;
        if storable_for(req, &resp) {
            self.save(req, &key, &resp);
        }
        Ok(resp)
    }

    /// Reads and decodes a stored response, reporting `None` on a miss or
    /// on any malformed entry.
    ///
    /// When the URL has a recorded Vary field list the matching variant is
    /// loaded; otherwise the primary entry is, and an entry that itself
    /// names a Vary (written by a Vary-unaware writer sharing the store)
    /// is a miss.
    fn load(&self, req: &Request, key: &str) -> Option<Response> {
        if let Ok(Some(idx)) = self.store.get(self.vary_index_key(req).as_bytes()) {
            let fields: Vec<String> = serde_json::from_slice(&idx).ok()?;
            if fields.is_empty() {
                return None;
            }
            return self.load_key(&self.vary_key(req, &fields), false);
        }
        self.load_key(key, true)
    }

    fn load_key(&self, key: &str, primary: bool) -> Option<Response> {
        let raw = self.store.get(key.as_bytes()).ok().flatten()?;
        let entry: Entry = serde_json::from_slice(&raw).ok()?;
        let resp = entry.into_response();
        if primary && !vary_fields(&resp.headers).is_empty() {
            return None;
        }
        Some(resp)
    }

    /// Serializes and stores a response. Without Vary it lands on the
    /// primary key (and clears any stale Vary index for the URL); with Vary
    /// it lands on the variant key and the field list is recorded in the
    /// index. Write failures are ignored: caching is best-effort and must
    /// not fail the caller's request.
    fn save(&self, req: &Request, key: &str, resp: &Response) {
        let Ok(raw) = serde_json::to_vec(&Entry::from_response(resp)) else {
            return;
        };
        let fields = vary_fields(&resp.headers);
        if fields.is_empty() {
            let _ = self.store.delete(self.vary_index_key(req).as_bytes());
            self.put(key, &raw);
            return;
        }
        let Ok(idx) = serde_json::to_vec(&fields) else {
            return;
        };
        self.put(&self.vary_index_key(req), &idx);
        self.put(&self.vary_key(req, &fields), &raw);
    }

    fn put(&self, key: &str, raw: &[u8]) {
        let _: Result<(), KvError> = if self.config.ttl.is_zero() {
            self.store.put(key.as_bytes(), raw)
        } else {
            self.store
                .put_with_ttl(key.as_bytes(), raw, self.config.ttl)
        };
    }
}

/// Lowercase-hex sha256 of `s`.
fn digest(s: &str) -> String {
    use std::fmt::Write as _;
    let mut out = String::with_capacity(64);
    for byte in Sha256::digest(s.as_bytes()) {
        let _ = write!(out, "{byte:02x}");
    }
    out
}

/// Reports whether `req` is eligible for caching.
///
/// Only `GET` qualifies, and an explicit `no-store` opts it out. Mirrored
/// by `contracts/httpcache-v1/cacheability.json`.
#[must_use]
pub fn cacheable_request(req: &Request) -> bool {
    req.method == "GET" && !has_no_store(&req.headers)
}

/// Reports whether `resp` may be stored: 2xx only, minus `no-store`.
#[must_use]
pub fn cacheable_response(resp: &Response) -> bool {
    (200..300).contains(&resp.status) && !has_no_store(&resp.headers)
}

/// Reports whether `resp`, fetched for `req`, may be written to the store.
///
/// Layers the request-dependent rules of RFC 9111 on top of
/// [`cacheable_response`]:
///
/// - §4.1: `Vary: *` can never be matched, so it is not stored.
/// - §5.2.2.7: a `private` response is never stored; the store is shared
///   across identities. This wins over the §3.5 exemptions.
/// - §3.5: a request carrying `Authorization` is stored only when the
///   response says `public`.
///
/// Mirrored by the `vary_*`, `authorization_*` and `private_*` sections of
/// `contracts/httpcache-v1/cacheability.json`.
#[must_use]
pub fn storable_for(req: &Request, resp: &Response) -> bool {
    if !cacheable_response(resp) {
        return false;
    }
    if vary_fields(&resp.headers) == ["*"] {
        return false;
    }
    if has_directive(&resp.headers, "private") {
        return false;
    }
    if req
        .headers
        .get("Authorization")
        .is_some_and(|v| !v.is_empty())
    {
        // Only an explicit `public` opts an authenticated response into this
        // shared store: it neither revalidates (`must-revalidate`) nor tracks
        // `s-maxage` freshness, so those RFC 9111 §3.5 exemptions do not apply.
        return has_directive(&resp.headers, "public");
    }
    true
}

/// Returns the lowercased, sorted, de-duplicated field names of every
/// `Vary` header line. `*` collapses the result to `["*"]`; no `Vary`
/// yields an empty list.
#[must_use]
pub fn vary_fields(headers: &Headers) -> Vec<String> {
    let mut seen = std::collections::BTreeSet::new();
    for line in headers.values("Vary") {
        for field in line.split(',') {
            let field = field.trim().to_ascii_lowercase();
            if field.is_empty() {
                continue;
            }
            if field == "*" {
                return vec!["*".to_string()];
            }
            seen.insert(field);
        }
    }
    seen.into_iter().collect()
}

/// Reports whether any `Cache-Control` header carries the named directive
/// (case-insensitive, token-bounded, with or without `=value`).
fn has_directive(headers: &Headers, name: &str) -> bool {
    headers.values("Cache-Control").iter().any(|value| {
        value.split(',').any(|token| {
            let token = token.trim();
            let token = token.split_once('=').map_or(token, |(n, _)| n.trim());
            token.eq_ignore_ascii_case(name)
        })
    })
}

/// Reports whether any `Cache-Control` header carries `no-store`.
///
/// Matching is case-insensitive and token-bounded, so `no-store-extension`
/// does not match.
fn has_no_store(headers: &Headers) -> bool {
    headers.values("Cache-Control").iter().any(|value| {
        value
            .split(',')
            .any(|token| token.trim().eq_ignore_ascii_case("no-store"))
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn no_store_is_token_bounded() {
        let bounded = |value: &str| {
            let mut h = Headers::new();
            h.set("Cache-Control", value);
            has_no_store(&h)
        };
        assert!(bounded("no-store"));
        assert!(bounded("NO-STORE"));
        assert!(bounded("no-cache, no-store"));
        assert!(bounded("max-age=60, no-store"));
        assert!(!bounded("no-store-extension"));
        assert!(!bounded("max-age=60"));
    }

    #[test]
    fn effective_prefix_substitutes_default() {
        assert_eq!(Config::default().effective_prefix(), DEFAULT_PREFIX);
        assert_eq!(
            Config::default().with_prefix("").effective_prefix(),
            DEFAULT_PREFIX
        );
        assert_eq!(
            Config::default().with_prefix("app:v2:").effective_prefix(),
            "app:v2:"
        );
    }
}
