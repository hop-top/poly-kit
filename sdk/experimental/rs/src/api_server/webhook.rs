//! The inbound webhook receiver, a port of `go/transport/webhook`
//! (feature `webhook`). Mount [`Receiver::route`] on the api-server
//! router.
//!
//! Per request, in order: a [`Handshake`] may answer (Graph's
//! validationToken echo); every [`Verifier`] must accept (a refusal is
//! 401 `webhook_unverified`, never saying why); the replay window; the
//! idempotency claim (an atomic put-if-absent with a TTL in a kv store,
//! released if the publish fails); then the payload is published and the
//! sender gets 202 when the publish finishes or the ack budget (2.5s)
//! runs out, the publish continuing detached.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use axum::body::Body;
use axum::extract::Request;
use axum::http::request::Parts;
use axum::http::{header, HeaderMap, HeaderValue, Method, StatusCode};
use axum::response::Response;
use axum::routing::{any, MethodRouter};
use base64::engine::general_purpose::STANDARD;
use base64::Engine;
use hmac::{Hmac, KeyInit, Mac};
use serde_json::{json, Map, Value};
use sha2::{Digest, Sha256, Sha512};
use subtle::ConstantTimeEq;

use super::error::{json_response, ApiError, ApiServerError, CODE_BODY_TOO_LARGE};
use crate::authn::rfc3339;
use crate::kv::TtlInserter;

/// Default body bound.
pub const DEFAULT_MAX_BODY: usize = 1 << 20;
/// Default ack budget.
pub const DEFAULT_ACK_BUDGET: Duration = Duration::from_millis(2500);
/// Default idempotency claim lifetime.
pub const DEFAULT_DEDUPE_TTL: Duration = Duration::from_secs(24 * 3600);
/// The event source of published deliveries.
pub const SOURCE: &str = "webhook.receiver";
/// A normal change notification.
pub const KIND_NOTIFICATION: &str = "notification";
/// A subscription lifecycle notice.
pub const KIND_LIFECYCLE: &str = "lifecycle";
/// Refusal code of an unverified delivery.
pub const CODE_UNVERIFIED: &str = "webhook_unverified";
/// Refusal code of a delivery outside the replay window.
pub const CODE_STALE: &str = "webhook_stale";

/// Where deliveries are published. Unlike the request-event sink, a
/// failure matters: it releases the idempotency claim and the sender
/// is told to retry.
pub trait WebhookPublisher: Send + Sync {
    /// Publishes one delivery.
    fn publish(&self, topic: &str, source: &str, payload: Value) -> Result<(), String>;
}

impl WebhookPublisher for crate::bus::SharedBus {
    fn publish(&self, topic: &str, source: &str, payload: Value) -> Result<(), String> {
        crate::bus::SharedBus::publish(self, &crate::bus::Event::new(topic, source, payload))
            .map_err(|e| e.to_string())
    }
}

/// Decides whether a delivery came from the sender.
pub type Verifier = Arc<dyn Fn(&Parts, &[u8]) -> Result<(), String> + Send + Sync>;
/// Answers a subscription-validation request, or returns `None`.
pub type Handshake = Arc<dyn Fn(&Parts, &[u8]) -> Option<Response> + Send + Sync>;
/// Reads a delivery's own timestamp.
pub type TimestampFn = Arc<dyn Fn(&Parts, &[u8]) -> Option<SystemTime> + Send + Sync>;
/// Extracts the idempotency key; empty means none.
pub type KeyFn = Arc<dyn Fn(&Parts, &[u8]) -> String + Send + Sync>;
/// Names the kind of a delivery, which picks its topic.
pub type Classifier = Arc<dyn Fn(&[u8]) -> String + Send + Sync>;
/// Called with each refusal's code and reason.
pub type OnRefused = Arc<dyn Fn(&str, &str) + Send + Sync>;
/// The clock.
pub type Clock = Arc<dyn Fn() -> SystemTime + Send + Sync>;

type Store = Arc<Mutex<Box<dyn TtlInserter + Send>>>;

/// [`hmac`] configuration.
#[derive(Debug, Clone, Default)]
pub struct HmacConfig {
    /// Header carrying the signature.
    pub header: String,
    /// The shared key.
    pub secret: Vec<u8>,
    /// `sha256` (default) or `sha512`; `sha1` only with the
    /// `webhook-sha1` feature (legacy senders), refused otherwise.
    pub hash: String,
    /// Precedes the digest in the header (`sha256=`).
    pub prefix: String,
    /// `hex` (default) or `base64`.
    pub encoding: String,
    /// When set, a header of Unix seconds; the signed payload becomes
    /// `<timestamp>.<body>`.
    pub timestamp_header: String,
}

fn mac_bytes(hash: &str, secret: &[u8], msg: &[&[u8]]) -> Vec<u8> {
    macro_rules! run {
        ($d:ty) => {{
            let mut m =
                <Hmac<$d> as KeyInit>::new_from_slice(secret).expect("hmac takes any key length");
            for part in msg {
                m.update(part);
            }
            m.finalize().into_bytes().to_vec()
        }};
    }
    if hash == "sha512" {
        run!(Sha512)
    } else if hash == "sha1" {
        #[cfg(feature = "webhook-sha1")]
        {
            run!(sha1::Sha1)
        }
        #[cfg(not(feature = "webhook-sha1"))]
        {
            unreachable!("check_hmac refuses sha1 without webhook-sha1")
        }
    } else {
        run!(Sha256)
    }
}

/// The header value [`hmac`] expects, for tests and senders.
pub fn sign_hmac(cfg: &HmacConfig, timestamp: &str, body: &[u8]) -> Result<String, ApiServerError> {
    check_hmac(cfg)?;
    let dot = b".";
    let sum = if cfg.timestamp_header.is_empty() {
        mac_bytes(&cfg.hash, &cfg.secret, &[body])
    } else {
        mac_bytes(&cfg.hash, &cfg.secret, &[timestamp.as_bytes(), dot, body])
    };
    let enc = if cfg.encoding == "base64" {
        STANDARD.encode(sum)
    } else {
        sum.iter().map(|b| format!("{b:02x}")).collect()
    };
    Ok(format!("{}{enc}", cfg.prefix))
}

fn check_hmac(cfg: &HmacConfig) -> Result<(), ApiServerError> {
    let bad = |m: String| ApiServerError(format!("webhook: {m}"));
    if cfg.header.is_empty() || cfg.secret.is_empty() {
        return Err(bad("HMAC needs a header and a secret".into()));
    }
    let sha1_ok = cfg!(feature = "webhook-sha1");
    if !(matches!(cfg.hash.as_str(), "" | "sha256" | "sha512") || (sha1_ok && cfg.hash == "sha1")) {
        let want = if sha1_ok {
            "sha256, sha512 or sha1"
        } else {
            "sha256 or sha512 (sha1 needs the webhook-sha1 feature)"
        };
        return Err(bad(format!("HMAC hash {:?}: want {want}", cfg.hash)));
    }
    if !matches!(cfg.encoding.as_str(), "" | "hex" | "base64") {
        return Err(bad(format!(
            "HMAC encoding {:?}: want hex or base64",
            cfg.encoding
        )));
    }
    Ok(())
}

/// Exactly the decimal form of a non-negative i64: ASCII digits, no
/// sign, space or leading zero. Checked before the MAC.
fn plain_unix_seconds(t: &str) -> bool {
    !t.is_empty()
        && t.len() <= 19
        && t.bytes().all(|b| b.is_ascii_digit())
        && t.parse::<i64>().is_ok_and(|n| n.to_string() == t)
}

fn digest_eq(a: &[u8], b: &[u8]) -> bool {
    // Hash both so a length difference leaks nothing.
    bool::from(
        Sha256::digest(a)
            .as_slice()
            .ct_eq(Sha256::digest(b).as_slice()),
    )
}

/// Verifies a keyed digest of the body in a header, in constant time.
pub fn hmac(cfg: HmacConfig) -> Result<Verifier, ApiServerError> {
    check_hmac(&cfg)?;
    Ok(Arc::new(move |parts, body| {
        let got =
            hdr(&parts.headers, &cfg.header).ok_or_else(|| format!("no {} header", cfg.header))?;
        let ts = if cfg.timestamp_header.is_empty() {
            String::new()
        } else {
            hdr(&parts.headers, &cfg.timestamp_header)
                .ok_or_else(|| format!("no timestamp header {}", cfg.timestamp_header))
                .and_then(|t| {
                    if plain_unix_seconds(t) {
                        Ok(t.to_string())
                    } else {
                        Err(format!(
                            "timestamp header {} is not Unix seconds",
                            cfg.timestamp_header
                        ))
                    }
                })?
        };
        let want = sign_hmac(&cfg, &ts, body).map_err(|e| e.0)?;
        if digest_eq(got.as_bytes(), want.as_bytes()) {
            Ok(())
        } else {
            Err("signature mismatch".into())
        }
    }))
}

/// Verifies the shared secret Graph echoes in every notification: each
/// `value[].clientState` equals `expected`, and there is at least one.
pub fn client_state(expected: &str) -> Verifier {
    let want = expected.to_string();
    Arc::new(move |_, body| {
        let doc: Value =
            serde_json::from_slice(body).map_err(|_| "no notifications".to_string())?;
        let items = doc["value"]
            .as_array()
            .filter(|a| !a.is_empty())
            .ok_or("no notifications")?;
        let mut ok = true;
        for n in items {
            let got = n["clientState"].as_str().unwrap_or("");
            ok &= digest_eq(got.as_bytes(), want.as_bytes());
        }
        if ok {
            Ok(())
        } else {
            Err("clientState mismatch".into())
        }
    })
}

/// Verifies HTTP basic credentials, in constant time.
pub fn basic_auth(user: &str, password: &str) -> Verifier {
    let (wu, wp) = (user.to_string(), password.to_string());
    Arc::new(move |parts, _| {
        let h = hdr(&parts.headers, "authorization").ok_or("no basic credentials")?;
        let (scheme, b64) = h.split_once(' ').ok_or("no basic credentials")?;
        if !scheme.eq_ignore_ascii_case("basic") {
            return Err("no basic credentials".into());
        }
        let raw = STANDARD
            .decode(b64.trim())
            .map_err(|_| "no basic credentials".to_string())?;
        let raw = String::from_utf8(raw).map_err(|_| "no basic credentials".to_string())?;
        let (u, p) = raw.split_once(':').ok_or("no basic credentials")?;
        let ok = digest_eq(u.as_bytes(), wu.as_bytes()) & digest_eq(p.as_bytes(), wp.as_bytes());
        if ok {
            Ok(())
        } else {
            Err("credentials mismatch".into())
        }
    })
}

/// Answers Graph's subscription validation: a request with a
/// `validationToken` query parameter is answered 200 `text/plain` with
/// the URL-decoded token; an overlong one is 400.
pub fn graph_validation_token() -> Handshake {
    Arc::new(|parts, _| {
        let token = query_param(parts.uri.query().unwrap_or(""), "validationToken")?;
        if token.is_empty() {
            return None;
        }
        let mut resp = Response::new(Body::empty());
        let h = resp.headers_mut();
        h.insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("text/plain; charset=utf-8"),
        );
        h.insert(
            "x-content-type-options",
            HeaderValue::from_static("nosniff"),
        );
        if token.len() > 2048 {
            *resp.status_mut() = StatusCode::BAD_REQUEST;
        } else {
            *resp.body_mut() = Body::from(token);
        }
        Some(resp)
    })
}

/// Unix seconds in a header.
pub fn header_unix_timestamp(name: &str) -> TimestampFn {
    let name = name.to_string();
    Arc::new(move |parts, _| {
        let n: i64 = hdr(&parts.headers, &name)?.trim().parse().ok()?;
        Some(from_secs(n))
    })
}

/// RFC 3339 text or Unix seconds at a JSON pointer.
pub fn json_timestamp(pointer: &str) -> TimestampFn {
    let pointer = pointer.to_string();
    Arc::new(move |_, body| {
        let doc: Value = serde_json::from_slice(body).ok()?;
        match doc.pointer(&pointer)? {
            Value::String(s) => rfc3339::parse(s).flatten(),
            Value::Number(n) => Some(from_secs(n.as_f64()? as i64)),
            _ => None,
        }
    })
}

/// Keys on a header.
pub fn header_key(name: &str) -> KeyFn {
    let name = name.to_string();
    Arc::new(move |parts, _| hdr(&parts.headers, &name).unwrap_or("").trim().to_string())
}

/// Keys on the string at a JSON pointer.
pub fn json_key(pointer: &str) -> KeyFn {
    let pointer = pointer.to_string();
    Arc::new(move |_, body| {
        serde_json::from_slice::<Value>(body)
            .ok()
            .and_then(|d| {
                d.pointer(&pointer)
                    .and_then(Value::as_str)
                    .map(str::to_string)
            })
            .unwrap_or_default()
    })
}

/// Keys on the SHA-256 of the body: an exact resend is a repeat.
pub fn body_hash_key() -> KeyFn {
    Arc::new(|_, body| {
        Sha256::digest(body)
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect()
    })
}

fn from_secs(n: i64) -> SystemTime {
    if n >= 0 {
        UNIX_EPOCH + Duration::from_secs(n as u64)
    } else {
        UNIX_EPOCH - Duration::from_secs(n.unsigned_abs())
    }
}

fn hdr<'a>(h: &'a HeaderMap, name: &str) -> Option<&'a str> {
    h.get(name)
        .and_then(|v| v.to_str().ok())
        .filter(|s| !s.is_empty())
}

/// Percent-decoded value of query parameter `name`.
fn query_param(query: &str, name: &str) -> Option<String> {
    query.split('&').find_map(|kv| {
        let (k, v) = kv.split_once('=').unwrap_or((kv, ""));
        (pct_decode(k) == name).then(|| pct_decode(v))
    })
}

fn pct_decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'+' => out.push(b' '),
            b'%' if i + 2 < b.len()
                && b.get(i + 1..i + 3)
                    .is_some_and(|h| h.iter().all(u8::is_ascii_hexdigit)) =>
            {
                out.push(u8::from_str_radix(&s[i + 1..i + 3], 16).unwrap_or(b'%'));
                i += 2;
            }
            c => out.push(c),
        }
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

/// A receiver under construction.
pub struct ReceiverBuilder {
    name: String,
    verifiers: Vec<Verifier>,
    handshakes: Vec<Handshake>,
    replay: Option<(Duration, TimestampFn)>,
    key: KeyFn,
    key_in_body: bool,
    sig_ts: Option<TimestampFn>,
    dedupe: Option<(Store, Duration)>,
    publisher: Option<Arc<dyn WebhookPublisher>>,
    topics: BTreeMap<String, String>,
    classify: Classifier,
    max_body: usize,
    ack_budget: Duration,
    now: Clock,
    on_refused: Option<OnRefused>,
}

/// A webhook endpoint.
pub struct Receiver {
    b: ReceiverBuilder,
}

impl Receiver {
    /// A receiver named `name` (namespaces idempotency keys; published
    /// as `receiver`).
    pub fn builder(name: impl Into<String>) -> ReceiverBuilder {
        let mut topics = BTreeMap::new();
        topics.insert(
            KIND_NOTIFICATION.to_string(),
            "kit.webhook.notification.received".to_string(),
        );
        topics.insert(
            KIND_LIFECYCLE.to_string(),
            "kit.webhook.lifecycle.received".to_string(),
        );
        ReceiverBuilder {
            name: name.into(),
            verifiers: Vec::new(),
            handshakes: Vec::new(),
            replay: None,
            key: body_hash_key(),
            key_in_body: false,
            sig_ts: None,
            dedupe: None,
            publisher: None,
            topics,
            classify: Arc::new(|_| KIND_NOTIFICATION.to_string()),
            max_body: DEFAULT_MAX_BODY,
            ack_budget: DEFAULT_ACK_BUDGET,
            now: Arc::new(SystemTime::now),
            on_refused: None,
        }
    }
}

impl ReceiverBuilder {
    /// Adds a verifier; every one must accept.
    pub fn verifier(mut self, v: Verifier) -> Self {
        self.verifiers.push(v);
        self
    }
    /// Adds a handshake, tried in order before verification.
    pub fn handshake(mut self, h: Handshake) -> Self {
        self.handshakes.push(h);
        self
    }
    /// Refuses a delivery whose timestamp is further than `window` from now.
    pub fn replay_window(mut self, window: Duration, ts: TimestampFn) -> Self {
        self.replay = Some((window, ts));
        self
    }
    /// Claims each delivery's key in `store` for `ttl` (zero is the
    /// default, 24h). `key` of `None` keeps the body hash.
    pub fn idempotency(
        mut self,
        store: impl TtlInserter + Send + 'static,
        ttl: Duration,
        key: Option<KeyFn>,
    ) -> Self {
        self.dedupe = Some((
            Arc::new(Mutex::new(Box::new(store))),
            if ttl.is_zero() {
                DEFAULT_DEDUPE_TTL
            } else {
                ttl
            },
        ));
        if let Some(k) = key {
            self.key = k;
        }
        self
    }
    /// Where deliveries go.
    pub fn publisher(mut self, p: Arc<dyn WebhookPublisher>) -> Self {
        self.publisher = Some(p);
        self
    }
    /// The topic of a kind; must pass the bus topic grammar.
    pub fn topic(mut self, kind: &str, topic: &str) -> Result<Self, ApiServerError> {
        crate::bus::validate_topic(&crate::bus::Topic::from(topic))
            .map_err(|e| ApiServerError(format!("webhook: topic for {kind:?}: {e}")))?;
        self.topics.insert(kind.to_string(), topic.to_string());
        Ok(self)
    }
    /// Names the kind of a delivery.
    pub fn classifier(mut self, c: Classifier) -> Self {
        self.classify = c;
        self
    }
    /// Bounds the body (default 1 MiB).
    pub fn max_body(mut self, n: usize) -> Self {
        if n > 0 {
            self.max_body = n;
        }
        self
    }
    /// How long a sender waits at most (default 2.5s).
    pub fn ack_budget(mut self, d: Duration) -> Self {
        if !d.is_zero() {
            self.ack_budget = d;
        }
        self
    }
    /// The clock; for tests.
    pub fn clock(mut self, c: Clock) -> Self {
        self.now = c;
        self
    }
    /// Called with every refusal.
    pub fn on_refused(mut self, f: OnRefused) -> Self {
        self.on_refused = Some(f);
        self
    }

    /// Microsoft Graph: validationToken handshake, clientState, and
    /// lifecycle notices on their own topic.
    pub fn graph(self, client_state_secret: &str) -> Self {
        self.handshake(graph_validation_token())
            .verifier(client_state(client_state_secret))
            .classifier(Arc::new(|body| {
                let lifecycle = serde_json::from_slice::<Value>(body)
                    .ok()
                    .and_then(|d| {
                        d["value"].as_array().map(|a| {
                            a.iter().any(|n| {
                                n["lifecycleEvent"].as_str().is_some_and(|s| !s.is_empty())
                            })
                        })
                    })
                    .unwrap_or(false);
                if lifecycle {
                    KIND_LIFECYCLE
                } else {
                    KIND_NOTIFICATION
                }
                .to_string()
            }))
    }

    /// Pipedrive v2: basic auth, `meta.id` as the key, `meta.timestamp`
    /// for the replay window (when `window` is non-zero).
    pub fn pipedrive(mut self, user: &str, password: &str, window: Duration) -> Self {
        self = self.verifier(basic_auth(user, password));
        self.key = json_key("/meta/id");
        self.key_in_body = true;
        if !window.is_zero() {
            self = self.replay_window(window, json_timestamp("/meta/timestamp"));
        }
        self
    }

    /// A plain HMAC-signed sender.
    pub fn generic_hmac(
        mut self,
        cfg: HmacConfig,
        window: Duration,
        key_header: &str,
    ) -> Result<Self, ApiServerError> {
        let ts_header = cfg.timestamp_header.clone();
        self = self.verifier(hmac(cfg)?);
        if !key_header.is_empty() {
            self.key = header_key(key_header);
        }
        if !ts_header.is_empty() {
            self.sig_ts = Some(header_unix_timestamp(&ts_header));
        }
        if !window.is_zero() && !ts_header.is_empty() {
            self = self.replay_window(window, header_unix_timestamp(&ts_header));
        }
        Ok(self)
    }

    /// Builds the receiver. It needs a name, a publisher and a verifier:
    /// an open endpoint is a mistake, not a default.
    pub fn build(self) -> Result<Receiver, ApiServerError> {
        let bad = |m: &str| Err(ApiServerError(format!("webhook: {m}")));
        if self.name.is_empty()
            || !self
                .name
                .bytes()
                .all(|b| matches!(b, b'a'..=b'z' | b'0'..=b'9' | b'_' | b'-'))
        {
            return Err(ApiServerError(format!(
                "webhook: receiver name {:?}: want lowercase letters, digits, '-' and '_'",
                self.name
            )));
        }
        if self.publisher.is_none() {
            return bad("a receiver needs a publisher");
        }
        if self.verifiers.is_empty() {
            return bad("a receiver needs a verifier");
        }
        Ok(Receiver { b: self })
    }
}

impl Receiver {
    /// The route to mount (`router.route("/hooks/x", receiver.route())`).
    pub fn route(self) -> MethodRouter {
        let rc = Arc::new(self);
        any(move |req: Request| {
            let rc = rc.clone();
            async move { rc.handle(req).await }
        })
    }

    fn refuse(&self, status: u16, code: &str, msg: &str, why: &str) -> Response {
        if let Some(f) = &self.b.on_refused {
            f(code, why);
        }
        ApiError::new(status, code, msg).into_response()
    }

    async fn handle(&self, req: Request) -> Response {
        let b = &self.b;
        let (parts, body) = req.into_parts();
        let has_token = query_param(parts.uri.query().unwrap_or(""), "validationToken")
            .is_some_and(|t| !t.is_empty());
        if parts.method != Method::POST && !has_token {
            let mut r =
                ApiError::new(405, "method_not_allowed", "webhooks are POSTed").into_response();
            r.headers_mut()
                .insert(header::ALLOW, HeaderValue::from_static("POST"));
            return r;
        }
        let body = match axum::body::to_bytes(body, b.max_body).await {
            Ok(x) => x,
            Err(_) => {
                return ApiError::new(
                    413,
                    CODE_BODY_TOO_LARGE,
                    format!("request body exceeds {} bytes", b.max_body),
                )
                .into_response()
            }
        };
        for h in &b.handshakes {
            if let Some(resp) = h(&parts, &body) {
                return resp;
            }
        }
        for v in &b.verifiers {
            if let Err(why) = v(&parts, &body) {
                return self.refuse(401, CODE_UNVERIFIED, "delivery not verified", &why);
            }
        }
        if let Some((window, ts)) = &b.replay {
            let now = (b.now)();
            let stale = match ts(&parts, &body) {
                None => true,
                Some(t) => {
                    let d = now.duration_since(t).map_err(|e| e.duration());
                    match d {
                        Ok(d) | Err(d) => d > *window,
                    }
                }
            };
            if stale {
                return self.refuse(
                    401,
                    CODE_STALE,
                    "delivery outside the replay window",
                    "timestamp missing or outside the window",
                );
            }
        }

        let mut key = (b.key)(&parts, &body);
        if key.is_empty() {
            key = body_hash_key()(&parts, &body);
        }
        let claimed = b
            .dedupe
            .as_ref()
            .map(|_| self.claim_key(&parts, &key, &body));
        if let (Some((store, ttl)), Some(claim)) = (&b.dedupe, &claimed) {
            let (store, ttl, claim) = (store.clone(), *ttl, claim.clone());
            let res = tokio::task::spawn_blocking(move || {
                let s = store.lock().unwrap_or_else(|p| p.into_inner());
                s.put_if_absent_with_ttl(claim.as_bytes(), b"1", ttl)
            })
            .await;
            match res {
                Ok(Ok(true)) => {}
                Ok(Ok(false)) => return json_response(200, &json!({"status": "duplicate"})),
                _ => return ApiError::new(503, "unavailable", "try again").into_response(),
            }
        }

        let kind = (b.classify)(&body);
        let topic = b
            .topics
            .get(&kind)
            .or_else(|| b.topics.get(KIND_NOTIFICATION))
            .cloned()
            .unwrap_or_default();
        let payload = self.payload(&parts, &kind, &key, &body);
        let (publisher, store) = (b.publisher.clone().expect("checked"), b.dedupe.clone());
        let release = claimed.clone();
        let task = tokio::task::spawn_blocking(move || {
            let r = publisher.publish(&topic, SOURCE, payload);
            if r.is_err() {
                if let (Some((store, _)), Some(claim)) = (store, release) {
                    let s = store.lock().unwrap_or_else(|p| p.into_inner());
                    let _ = s.delete(claim.as_bytes());
                }
            }
            r
        });
        match tokio::time::timeout(b.ack_budget, task).await {
            Ok(Ok(Err(_))) | Ok(Err(_)) => {
                ApiError::new(503, "unavailable", "try again").into_response()
            }
            _ => json_response(202, &json!({"status": "accepted"})),
        }
    }

    /// The kv key a delivery is claimed under: a hash, so no name and
    /// key join to the same string. A key from a header is not trusted:
    /// the claim is the body's hash and the signed timestamp.
    fn claim_key(&self, parts: &Parts, key: &str, body: &[u8]) -> String {
        let material = if self.b.key_in_body {
            key.to_string()
        } else {
            let mut m = body_hash_key()(parts, body);
            if let Some(t) = self.b.sig_ts.as_ref().and_then(|f| f(parts, body)) {
                let secs = t.duration_since(UNIX_EPOCH).map_or(0, |d| d.as_secs());
                m.push_str(&format!("\0{secs}"));
            }
            m
        };
        let hex: String = Sha256::digest(material.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        format!("webhook/{}/{hex}", self.b.name)
    }

    fn payload(&self, parts: &Parts, kind: &str, key: &str, body: &[u8]) -> Value {
        let mut p = Map::new();
        p.insert("receiver".into(), json!(self.b.name));
        p.insert("key".into(), json!(key));
        p.insert("kind".into(), json!(kind));
        p.insert("received_at".into(), json!(rfc3339::format((self.b.now)())));
        p.insert(
            "content_type".into(),
            json!(parts
                .headers
                .get(header::CONTENT_TYPE)
                .and_then(|v| v.to_str().ok())
                .unwrap_or("")),
        );
        match std::str::from_utf8(body)
            .ok()
            .and_then(|s| serde_json::from_str::<Value>(s).ok())
        {
            Some(v) => {
                p.insert("encoding".into(), json!("json"));
                p.insert("body".into(), v);
            }
            None => {
                p.insert("encoding".into(), json!("base64"));
                p.insert("body".into(), json!(STANDARD.encode(body)));
            }
        }
        Value::Object(p)
    }
}
