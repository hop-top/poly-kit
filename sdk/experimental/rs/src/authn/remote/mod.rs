//! Remote key sets: a JWKS fetched from a URL and an OpenID Connect
//! issuer's discovery, a port of the remote half of Go's
//! `transport/authn`. Feature `authn-remote`.
//!
//! The cache, refresh and discovery rules live here and do no I/O:
//! documents arrive through [`Fetch`]. The default [`Fetch`] is the
//! guarded HTTP client in `http.rs`, the only module naming an HTTP
//! crate; tests (and adopters with their own transport) pass another.
//!
//! The set is fetched on first use, again once older than
//! [`Remote::refresh`], and when a token names a `kid` the set lacks
//! (the issuer rotated), at most once per [`Remote::min_refresh`]. A
//! failed refetch keeps the last good set; a set with no signing key is
//! a failure. URLs are `https`, or `http` to a loopback host.

mod http;

use std::sync::{Arc, Mutex, MutexGuard};
use std::time::{Duration, SystemTime};

use serde::Deserialize;

use super::error::AuthnError;
use super::key::{jwks_members, signing_keys, Key, MAX_DOCUMENT_BYTES};
use super::verifier::{select, KeySource, Options, Verifier};
use crate::netpolicy::{is_loopback, NetPolicy};

pub use http::HttpFetch;

/// How long a fetched key set is used before it is fetched again.
pub const DEFAULT_REFRESH: Duration = Duration::from_secs(3600);
/// The least time between two fetches: a token naming an unknown kid,
/// or a fetch that failed, does not trigger another fetch sooner.
pub const DEFAULT_MIN_REFRESH: Duration = Duration::from_secs(60);
/// Bounds one fetch by the default HTTP fetcher.
pub const DEFAULT_FETCH_TIMEOUT: Duration = Duration::from_secs(10);

/// Fetches one JSON document. Implementations answer a non-200
/// status, a body over [`MAX_DOCUMENT_BYTES`] and transport failures
/// with an `Err` whose text names the request, as Go's does
/// (`GET <url>: status 503`).
pub trait Fetch: Send + Sync {
    /// The body of `GET url`.
    fn get(&self, url: &str) -> Result<Vec<u8>, String>;
}

/// How a remote key set is fetched and cached.
#[derive(Clone, Default)]
pub struct Remote {
    /// Fetches the documents; `None` means [`HttpFetch`] under
    /// [`Remote::net_policy`], with [`DEFAULT_FETCH_TIMEOUT`] and no
    /// redirects followed.
    pub fetch: Option<Arc<dyn Fetch>>,
    /// The network policy of the default fetcher.
    pub net_policy: NetPolicy,
    /// How long a fetched key set is used; zero means
    /// [`DEFAULT_REFRESH`].
    pub refresh: Duration,
    /// The least time between fetches; zero means
    /// [`DEFAULT_MIN_REFRESH`].
    pub min_refresh: Duration,
}

impl std::fmt::Debug for Remote {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Remote")
            .field("fetch", &self.fetch.as_ref().map(|_| ".."))
            .field("net_policy", &self.net_policy)
            .field("refresh", &self.refresh)
            .field("min_refresh", &self.min_refresh)
            .finish()
    }
}

/// A verifier for tokens signed by a key in the JSON Web Key Set at
/// `jwks_url`, fetched and cached as the module doc describes.
///
/// # Errors
///
/// [`AuthnError::Config`] when the URL is not `https` (or `http` to a
/// loopback host) or the default fetcher cannot be built.
pub fn new_jwks(jwks_url: &str, remote: Remote, opts: Options) -> Result<Verifier, AuthnError> {
    check_url(jwks_url).map_err(|e| AuthnError::Config(format!("jwks url: {e}")))?;
    let keys = RemoteKeys::new(remote, &opts, Resolve::Fixed(jwks_url.to_string()))?;
    Ok(Verifier::with_source(Arc::new(keys), opts))
}

/// A verifier for tokens an OpenID Connect provider issued: discovery
/// (`issuer` + `/.well-known/openid-configuration`) yields its
/// `jwks_uri`, handled as [`new_jwks`] handles its URL. The discovery
/// document must name `issuer` exactly (OpenID Connect Discovery
/// 4.3), and a token's `iss` must be `issuer`: `opts.issuer` is set to
/// it. Discovery runs on first use and is retried, at most once per
/// [`Remote::min_refresh`], until it succeeds.
///
/// # Errors
///
/// As [`new_jwks`], for the issuer URL.
pub fn new_oidc(issuer: &str, remote: Remote, mut opts: Options) -> Result<Verifier, AuthnError> {
    check_url(issuer).map_err(|e| AuthnError::Config(format!("oidc issuer: {e}")))?;
    opts.issuer = Some(issuer.to_string());
    let keys = RemoteKeys::new(
        remote,
        &opts,
        Resolve::Discover {
            issuer: issuer.to_string(),
            uri: Mutex::new(None),
        },
    )?;
    Ok(Verifier::with_source(Arc::new(keys), opts))
}

/// Accepts an `https` URL, or an `http` one to a loopback host
/// (127.0.0.0/8, `::1`, `localhost`): a key set fetched in plaintext
/// over the network could be replaced in transit.
///
/// # Errors
///
/// The reason, worded as Go's `CheckURL`.
pub fn check_url(raw: &str) -> Result<(), String> {
    let u = url::Url::parse(raw).map_err(|e| e.to_string())?;
    let host = u.host_str().unwrap_or("");
    if host.is_empty() {
        return Err(format!("{raw:?} is not an absolute URL"));
    }
    match u.scheme() {
        "https" => Ok(()),
        "http" => {
            if is_loopback(host) {
                Ok(())
            } else {
                Err(format!(
                    "{raw:?}: use https; plain http is accepted only to a loopback host"
                ))
            }
        }
        _ => Err(format!("{raw:?}: scheme must be https")),
    }
}

enum Resolve {
    Fixed(String),
    Discover {
        issuer: String,
        uri: Mutex<Option<String>>,
    },
}

#[derive(Default)]
struct State {
    keys: Option<Vec<Key>>,
    fetched: Option<SystemTime>,
    last_attempt: Option<SystemTime>,
    last_err: String,
}

/// A fetched, cached key set behind the [`KeySource`] seam.
struct RemoteKeys {
    fetch: Arc<dyn Fetch>,
    refresh: Duration,
    min_refresh: Duration,
    opts_now: Options,
    resolve: Resolve,
    state: Mutex<State>,
}

impl RemoteKeys {
    fn new(remote: Remote, opts: &Options, resolve: Resolve) -> Result<Self, AuthnError> {
        let fetch = match remote.fetch {
            Some(f) => f,
            None => Arc::new(HttpFetch::new(remote.net_policy, DEFAULT_FETCH_TIMEOUT)?),
        };
        let or = |d: Duration, default: Duration| if d.is_zero() { default } else { d };
        Ok(Self {
            fetch,
            refresh: or(remote.refresh, DEFAULT_REFRESH),
            min_refresh: or(remote.min_refresh, DEFAULT_MIN_REFRESH),
            opts_now: Options {
                now: opts.now.clone(),
                ..Options::default()
            },
            resolve,
            state: Mutex::new(State::default()),
        })
    }

    fn lock(&self) -> MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|p| p.into_inner())
    }

    fn may_fetch(&self, st: &State, now: SystemTime) -> bool {
        st.last_attempt
            .is_none_or(|t| since(now, t) >= self.min_refresh)
    }

    /// Replaces the key set; on failure keeps the last good one and
    /// records the error.
    fn refetch(&self, st: &mut State, now: SystemTime) {
        st.last_attempt = Some(now);
        let url = match self.resolve_url() {
            Ok(u) => u,
            Err(e) => {
                st.last_err = e;
                return;
            }
        };
        let members = match self
            .get_json(&url)
            .and_then(|b| jwks_members(&b).map_err(|e| format!("GET {url}: {e}")))
        {
            Ok(m) => m,
            Err(e) => {
                st.last_err = e;
                return;
            }
        };
        let keys = signing_keys(members);
        if keys.is_empty() {
            st.last_err = format!("{url} holds no signing key");
            return;
        }
        st.keys = Some(keys);
        st.fetched = Some(now);
        st.last_err.clear();
    }

    /// The body of `GET url`, with the document size cap enforced
    /// whatever the fetcher did.
    fn get_json(&self, url: &str) -> Result<Vec<u8>, String> {
        let body = self.fetch.get(url)?;
        if body.len() > MAX_DOCUMENT_BYTES {
            return Err(format!(
                "GET {url}: document larger than {MAX_DOCUMENT_BYTES} bytes"
            ));
        }
        Ok(body)
    }

    fn resolve_url(&self) -> Result<String, String> {
        match &self.resolve {
            Resolve::Fixed(u) => Ok(u.clone()),
            Resolve::Discover { issuer, uri } => {
                let mut cached = uri.lock().unwrap_or_else(|p| p.into_inner());
                if let Some(u) = cached.as_ref() {
                    return Ok(u.clone());
                }
                let well_known = format!(
                    "{}/.well-known/openid-configuration",
                    issuer.strip_suffix('/').unwrap_or(issuer)
                );
                #[derive(Deserialize)]
                struct Doc {
                    issuer: Option<String>,
                    jwks_uri: Option<String>,
                }
                let body = self
                    .get_json(&well_known)
                    .map_err(|e| format!("discovery: {e}"))?;
                let doc: Doc = serde_json::from_slice(&body)
                    .map_err(|e| format!("discovery: GET {well_known}: {e}"))?;
                let named = doc.issuer.unwrap_or_default();
                if named != *issuer {
                    return Err(format!(
                        "discovery: {well_known} names issuer {named:?}, not {issuer:?}"
                    ));
                }
                let jwks = doc.jwks_uri.unwrap_or_default();
                if jwks.is_empty() {
                    return Err(format!("discovery: {well_known} names no jwks_uri"));
                }
                check_url(&jwks).map_err(|e| format!("discovery: jwks_uri: {e}"))?;
                *cached = Some(jwks.clone());
                Ok(jwks)
            }
        }
    }
}

/// `now - earlier`, zero when the clock stepped back.
fn since(now: SystemTime, earlier: SystemTime) -> Duration {
    now.duration_since(earlier).unwrap_or_default()
}

impl KeySource for RemoteKeys {
    fn lookup(&self, kid: &str) -> Result<Vec<Key>, AuthnError> {
        let now = self
            .opts_now
            .now()
            .map_err(|e| AuthnError::KeySetUnavailable(e.to_string()))?;
        let mut st = self.lock();
        let stale = st
            .fetched
            .is_none_or(|t| st.keys.is_none() || since(now, t) >= self.refresh);
        if stale && self.may_fetch(&st, now) {
            self.refetch(&mut st, now);
        }
        let Some(keys) = &st.keys else {
            return Err(AuthnError::KeySetUnavailable(st.last_err.clone()));
        };
        let found = select(keys, kid);
        if found.is_empty() && !kid.is_empty() && self.may_fetch(&st, now) {
            // An unknown kid: the issuer may have rotated since the
            // set was fetched.
            self.refetch(&mut st, now);
            if let Some(keys) = &st.keys {
                return Ok(select(keys, kid));
            }
        }
        Ok(found)
    }
}
