//! The reqwest half of the policy: [`GuardedClient`], the construction
//! path every user-traffic request in this crate is issued through, and
//! the URL stripping its errors share with `api`. Gated on `api`.

use thiserror::Error;

use super::core::{HostNotAllowedError, NetPolicy, OfflineError, PolicyError, RefusalReason};

impl NetPolicy {
    /// Whether a request to `url` is permitted under this policy.
    /// Loopback destinations are always permitted; a URL with no host
    /// is refused while offline.
    ///
    /// The host is judged as Go's `URL.Host`: with `:port` when the URL
    /// names one. The url crate drops a scheme's default port, so
    /// `http://[::1]:80/` is judged as `[::1]`; both are loopback.
    pub fn allows(&self, url: &reqwest::Url) -> bool {
        self.url_denial(url).is_none()
    }

    /// Why a request to `url` is refused, if it is.
    fn url_denial(&self, url: &reqwest::Url) -> Option<RefusalReason> {
        let Some(h) = url.host_str() else {
            // No host: nothing to judge. Offline refuses it; an
            // allowlist cannot match it either.
            return (self.is_offline() || !self.allow_hosts().is_empty()).then_some(
                if self.is_offline() {
                    RefusalReason::Offline
                } else {
                    RefusalReason::HostNotAllowed
                },
            );
        };
        match url.port() {
            Some(port) => self.denial(&format!("{h}:{port}")),
            None => self.denial(h),
        }
    }
}

/// Errors surfaced by [`GuardedClient`]. Refusals are a distinct,
/// matchable variant; everything else is reqwest's own error, with its
/// URL stripped the same way as a refusal's.
#[derive(Debug, Error)]
pub enum NetError {
    /// The request was refused by the offline policy.
    #[error(transparent)]
    Offline(#[from] OfflineError),
    /// The request was refused by the host allowlist.
    #[error(transparent)]
    HostNotAllowed(#[from] HostNotAllowedError),
    /// A redirect was refused or ran past its limit. Nothing was sent
    /// to the hop's target.
    #[error(transparent)]
    Redirect(#[from] RedirectError),
    /// The request was attempted and reqwest failed it (or rejected it
    /// while building, e.g. an unsupported scheme).
    ///
    /// reqwest quotes the request URL in its Display and Debug output,
    /// so the guard replaces it before the error is returned:
    /// `reqwest::Error::url` holds scheme, host (with port) and path
    /// only. Query, fragment and userinfo are gone and cannot be
    /// recovered from the error, as they may carry credentials.
    #[error(transparent)]
    Transport(#[from] reqwest::Error),
}

impl NetError {
    /// The offline refusal, if this error is one. The analogue of
    /// `errors.Is(err, netpolicy.ErrOffline)`.
    pub fn as_offline(&self) -> Option<&OfflineError> {
        match self {
            NetError::Offline(e) => Some(e),
            _ => None,
        }
    }

    /// The allowlist refusal, if this error is one. The analogue of
    /// `errors.Is(err, netpolicy.ErrHostNotAllowed)`.
    pub fn as_host_not_allowed(&self) -> Option<&HostNotAllowedError> {
        match self {
            NetError::HostNotAllowed(e) => Some(e),
            _ => None,
        }
    }

    /// Whether this error is an offline refusal.
    pub fn is_offline(&self) -> bool {
        self.as_offline().is_some()
    }

    /// Whether this error is an allowlist refusal.
    pub fn is_host_not_allowed(&self) -> bool {
        self.as_host_not_allowed().is_some()
    }
}

impl From<PolicyError> for NetError {
    fn from(e: PolicyError) -> Self {
        match e {
            PolicyError::Offline(e) => NetError::Offline(e),
            PolicyError::HostNotAllowed(e) => NetError::HostNotAllowed(e),
        }
    }
}

/// Render `url` as scheme, host (with port) and path only: enough to say
/// where a request was going, nothing it carried. Query, fragment and
/// userinfo are dropped because they may hold credentials (an API key
/// param, basic-auth userinfo). An opaque URL (`mailto:`) keeps its
/// scheme alone. Mirrors Go's `netpolicy` refusal; also names the
/// destination of a failed request in `api`'s transport errors.
pub(crate) fn destination(url: &reqwest::Url) -> String {
    stripped(url).to_string()
}

/// [`destination`] as a URL, for handing back on a reqwest error.
fn stripped(url: &reqwest::Url) -> reqwest::Url {
    if url.cannot_be_a_base() {
        // A bare scheme always parses.
        return format!("{}:", url.scheme())
            .parse()
            .expect("scheme-only URL");
    }
    let mut d = url.clone();
    d.set_query(None);
    d.set_fragment(None);
    // Both fail only on URLs that cannot carry userinfo at all.
    let _ = d.set_username("");
    let _ = d.set_password(None);
    d
}

/// Swap the URL on a reqwest error for its [`stripped`] form, so the
/// error's Display, Debug and `url()` name where the request was going
/// and nothing it carried.
fn scrub(e: reqwest::Error) -> reqwest::Error {
    match e.url().map(stripped) {
        Some(url) => e.with_url(url),
        None => e,
    }
}

/// reqwest's own default redirect limit.
const DEFAULT_MAX_REDIRECTS: usize = 10;

/// Headers a cross-origin hop may carry. Anything else on the request
/// (`Authorization`, `Cookie`, `X-Api-Key`, or a custom header that
/// holds identity just as well) cannot be told safe from sensitive by
/// name, so a request carrying one is refused at the first hop that
/// leaves its origin, never stripped.
const CROSS_ORIGIN_HEADERS: &[&str] = &[
    "accept",
    "accept-encoding",
    "accept-language",
    "user-agent",
    "content-type",
];

/// What the client does with a 3xx response. Kit-owned: the inner
/// reqwest client always runs with its redirect handling off, because
/// reqwest's policy hook cannot see the request's headers.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Redirects {
    /// Return the 3xx response to the caller unfollowed. For callers
    /// that must never move a credential to a host the server names.
    None,
    /// Follow up to `max` hops. Every hop is judged by the policy
    /// (host allowlist, `--offline`) before it is sent, and a hop to
    /// another origin is refused when the request carries a credential
    /// header. A 301/302 on a POST and every 303 become a bodiless GET,
    /// as in reqwest.
    Follow { max: usize },
}

impl Default for Redirects {
    /// Follow up to reqwest's own limit of 10.
    fn default() -> Self {
        Redirects::Follow {
            max: DEFAULT_MAX_REDIRECTS,
        }
    }
}

/// A redirect the client refused to follow.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RedirectError {
    /// The request carries a header outside the cross-origin allowlist
    /// and the hop leaves its origin. Both ends are stripped as in a
    /// refusal.
    CrossOriginHeaders { from: String, to: String },
    /// More hops than the mode allows.
    TooMany { max: usize },
}

impl std::fmt::Display for RedirectError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            RedirectError::CrossOriginHeaders { from, to } => write!(
                f,
                "redirect from {from} to {to} refused: request carries headers that may not leave their origin"
            ),
            RedirectError::TooMany { max } => write!(f, "too many redirects (limit {max})"),
        }
    }
}

impl std::error::Error for RedirectError {}

fn origin(u: &reqwest::Url) -> (String, Option<String>, Option<u16>) {
    (
        u.scheme().to_string(),
        u.host_str().map(str::to_string),
        u.port_or_known_default(),
    )
}

fn is_redirect(status: reqwest::StatusCode) -> bool {
    matches!(status.as_u16(), 301 | 302 | 303 | 307 | 308)
}

/// The port's shared HTTP client: the construction path through which
/// every user-traffic reqwest request in this crate is issued (the
/// telemetry sink's own client is the deliberate exception).
///
/// It deliberately does not expose its inner `reqwest::Client`. Handing
/// one out would let a caller send a request that never reaches
/// [`GuardedClient::execute`], which is the only place the policy is
/// applied.
///
/// Errors it returns never carry the request's query, fragment or
/// userinfo: see [`NetError::Transport`]. A `reqwest::Response` it hands
/// back is reqwest's own, so its `url()` and the errors of its body
/// methods (`json`, `text`, `bytes`) still carry the full URL; callers
/// that surface those must strip it themselves.
#[derive(Debug, Clone)]
pub struct GuardedClient {
    inner: reqwest::Client,
    policy: NetPolicy,
    redirects: Redirects,
    /// Headers every request carries unless it sets its own. Held here,
    /// not on the inner client, which would re-add them on every hop.
    defaults: reqwest::header::HeaderMap,
}

impl GuardedClient {
    /// Build a client under `policy` with reqwest's defaults and
    /// [`Redirects::default`].
    pub fn new(policy: NetPolicy) -> Result<Self, reqwest::Error> {
        Self::build(
            policy,
            reqwest::Client::builder(),
            Redirects::default(),
            reqwest::header::HeaderMap::new(),
        )
    }

    /// Build a client under `policy` from a configured
    /// `reqwest::ClientBuilder`, for callers that need timeouts or other
    /// transport settings. The policy is applied regardless of how the
    /// builder was configured. Redirects are the client's own business,
    /// named by `redirects`; any `redirect` policy on `builder` is
    /// replaced, so a caller cannot opt out of the per-hop check by
    /// accident, nor into following by omission.
    ///
    /// Default headers go in `default_headers`, not on `builder`: the
    /// client merges them into each request itself, so the redirect
    /// logic sees exactly what is sent. (A `default_headers` set on
    /// `builder` is replaced.)
    pub fn build(
        policy: NetPolicy,
        builder: reqwest::ClientBuilder,
        redirects: Redirects,
        default_headers: reqwest::header::HeaderMap,
    ) -> Result<Self, reqwest::Error> {
        Ok(Self {
            inner: builder
                .redirect(reqwest::redirect::Policy::none())
                .default_headers(reqwest::header::HeaderMap::new())
                .build()?,
            policy,
            redirects,
            defaults: default_headers,
        })
    }

    /// The policy this client enforces.
    pub fn policy(&self) -> NetPolicy {
        self.policy.clone()
    }

    /// What this client does with a 3xx response.
    pub fn redirects(&self) -> Redirects {
        self.redirects
    }

    /// Start a request. The returned builder is reqwest's own, so the
    /// full builder surface stays available; it must be handed back to
    /// [`RequestBuilder::send`] (or [`GuardedClient::execute`]) to be
    /// issued.
    pub fn request(&self, method: reqwest::Method, url: impl reqwest::IntoUrl) -> RequestBuilder {
        RequestBuilder {
            client: self.clone(),
            inner: self.inner.request(method, url),
        }
    }

    /// Start a GET request.
    pub fn get(&self, url: impl reqwest::IntoUrl) -> RequestBuilder {
        self.request(reqwest::Method::GET, url)
    }

    /// Start a POST request.
    pub fn post(&self, url: impl reqwest::IntoUrl) -> RequestBuilder {
        self.request(reqwest::Method::POST, url)
    }

    /// Issue `req`, refusing it when the policy forbids the
    /// destination. This is the chokepoint: no request leaves this
    /// crate without passing through here. A failure's URL is stripped
    /// to scheme, host and path (see [`NetError::Transport`]).
    pub async fn execute(&self, mut req: reqwest::Request) -> Result<reqwest::Response, NetError> {
        for (k, v) in &self.defaults {
            if !req.headers().contains_key(k) {
                req.headers_mut().append(k.clone(), v.clone());
            }
        }
        self.judge(req.method().as_str(), req.url())?;
        let Redirects::Follow { max } = self.redirects else {
            return self.send(req).await;
        };
        let unsafe_headers = req
            .headers()
            .keys()
            .any(|k| !CROSS_ORIGIN_HEADERS.contains(&k.as_str()));
        let first = req.url().clone();
        let mut next = req.try_clone();
        let mut resp = self.send(req).await?;
        let mut hops = 0;
        while is_redirect(resp.status()) {
            // A body that cannot be replayed (a stream) ends the chain
            // at the 3xx, which the caller sees as a response.
            let Some(cur) = next.take() else { break };
            let Some(to) = resp
                .headers()
                .get(reqwest::header::LOCATION)
                .and_then(|v| v.to_str().ok())
                .and_then(|loc| cur.url().join(loc).ok())
            else {
                break;
            };
            if hops >= max {
                return Err(RedirectError::TooMany { max }.into());
            }
            hops += 1;
            // The same decision as the first URL, per hop: an allowed
            // host cannot bounce the request to a refused one.
            self.judge("redirect", &to)?;
            if unsafe_headers && origin(&to) != origin(&first) {
                let (from, dest) = (destination(&first), destination(&to));
                self.policy
                    .note(RefusalReason::CrossOriginHeaders, "redirect", &dest);
                return Err(RedirectError::CrossOriginHeaders { from, to: dest }.into());
            }
            let cross_origin = origin(&to) != origin(&first);
            let req = redirected(&cur, resp.status(), to, cross_origin);
            next = req.try_clone();
            resp = self.send(req).await?;
        }
        Ok(resp)
    }

    fn judge(&self, op: &str, url: &reqwest::Url) -> Result<(), NetError> {
        match self.policy.url_denial(url) {
            Some(reason) => Err(self
                .policy
                .refuse(reason, op.to_string(), destination(url))
                .into()),
            None => Ok(()),
        }
    }

    async fn send(&self, req: reqwest::Request) -> Result<reqwest::Response, NetError> {
        self.inner
            .execute(req)
            .await
            .map_err(|e| NetError::Transport(scrub(e)))
    }
}

/// The request for the next hop. 303, and 301/302 on a POST, become a
/// bodiless GET; 307/308 replay method and body.
fn redirected(
    prev: &reqwest::Request,
    status: reqwest::StatusCode,
    to: reqwest::Url,
    cross_origin: bool,
) -> reqwest::Request {
    let to_get = status.as_u16() == 303
        || (matches!(status.as_u16(), 301 | 302) && prev.method() == reqwest::Method::POST);
    let method = if to_get {
        reqwest::Method::GET
    } else {
        prev.method().clone()
    };
    let mut req = reqwest::Request::new(method, to);
    *req.headers_mut() = prev.headers().clone();
    if cross_origin {
        let drop: Vec<_> = req
            .headers()
            .keys()
            .filter(|k| !CROSS_ORIGIN_HEADERS.contains(&k.as_str()))
            .cloned()
            .collect();
        for k in drop {
            req.headers_mut().remove(k);
        }
    }
    if to_get {
        for h in [
            reqwest::header::CONTENT_TYPE,
            reqwest::header::CONTENT_LENGTH,
            reqwest::header::CONTENT_ENCODING,
            reqwest::header::TRANSFER_ENCODING,
        ] {
            req.headers_mut().remove(h);
        }
    } else if let Some(body) = prev.body().and_then(|b| b.as_bytes()) {
        *req.body_mut() = Some(body.to_vec().into());
    }
    *req.timeout_mut() = prev.timeout().copied();
    req
}

/// A request under construction against a [`GuardedClient`].
///
/// Wraps reqwest's builder so the only way to issue the request is
/// [`RequestBuilder::send`], which routes through the guard.
#[derive(Debug)]
pub struct RequestBuilder {
    client: GuardedClient,
    inner: reqwest::RequestBuilder,
}

impl RequestBuilder {
    /// Set a header. Takes the string forms rather than reqwest's full
    /// generic bound, which would require a direct `http` dependency
    /// this crate does not otherwise carry.
    pub fn header(self, key: &str, value: &str) -> Self {
        Self {
            inner: self.inner.header(key, value),
            ..self
        }
    }

    /// Set a bearer authorization header.
    pub fn bearer_auth<T: std::fmt::Display>(self, token: T) -> Self {
        Self {
            inner: self.inner.bearer_auth(token),
            ..self
        }
    }

    /// Set the request body.
    pub fn body<T: Into<reqwest::Body>>(self, body: T) -> Self {
        Self {
            inner: self.inner.body(body),
            ..self
        }
    }

    /// Serialize `json` as the JSON request body.
    pub fn json<T: serde::Serialize + ?Sized>(self, json: &T) -> Self {
        Self {
            inner: self.inner.json(json),
            ..self
        }
    }

    /// Append serialized `query` to the URL's query string.
    pub fn query<T: serde::Serialize + ?Sized>(self, query: &T) -> Self {
        Self {
            inner: self.inner.query(query),
            ..self
        }
    }

    /// Issue the request through the guard.
    pub async fn send(self) -> Result<reqwest::Response, NetError> {
        let req = self.inner.build().map_err(scrub)?;
        self.client.execute(req).await
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn policy_allows_loopback_while_offline() {
        let p = NetPolicy::offline();
        assert!(p.allows(&"http://127.0.0.1:8080/x".parse().unwrap()));
        assert!(p.allows(&"http://localhost/x".parse().unwrap()));
        assert!(p.allows(&"http://[::1]:9000/x".parse().unwrap()));
        assert!(!p.allows(&"https://example.com/x".parse().unwrap()));
    }

    #[test]
    fn online_policy_allows_everything() {
        let p = NetPolicy::online();
        assert!(p.allows(&"https://example.com/x".parse().unwrap()));
        assert!(p.allows(&"http://127.0.0.1/x".parse().unwrap()));
    }

    #[test]
    fn destination_keeps_scheme_host_port_path_only() {
        let cases = [
            (
                "https://alice:pw-secret@example.invalid:8443/v1/m:generate?key=q-secret#frag",
                "https://example.invalid:8443/v1/m:generate",
            ),
            ("https://example.invalid", "https://example.invalid/"),
            (
                "http://[2001:db8::1]:9000/x?y",
                "http://[2001:db8::1]:9000/x",
            ),
            ("mailto:alice@example.invalid?subject=q-secret", "mailto:"),
        ];
        for (raw, want) in cases {
            let url: reqwest::Url = raw.parse().unwrap();
            assert_eq!(destination(&url), want, "{raw}");
        }
    }

    #[test]
    fn net_error_discriminates() {
        let e: NetError = OfflineError {
            method: "GET".into(),
            url: "https://example.com/x".into(),
        }
        .into();
        assert!(e.is_offline());
        assert_eq!(e.as_offline().unwrap().method, "GET");
    }
}
