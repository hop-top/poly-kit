//! The websocket hub, a port of Go's `Hub` and `WSHandler`. Feature
//! `api-server-ws`.
//!
//! Clients connect to a route carrying [`Hub::route`], receive a
//! `welcome`, `subscribe` to topic patterns and get every
//! [`Hub::publish`] on a matching topic as a `message`. Topic patterns
//! are dotted: `*` matches exactly one segment, `**` one or more and
//! ends the pattern.
//!
//! The upgrade is hyper's (never axum's `ws`, which would pull a second
//! tungstenite) and the frames tokio-tungstenite's. The route sits
//! inside the server's stack, so Host and Origin checks, rate limit and
//! authentication apply to the upgrade request: a refused request is
//! answered before any upgrade, as an ordinary HTTP response.
//!
//! Bounds: a message over [`HubOptions::max_message_size`] closes the
//! connection (1009); each connection has a send buffer of
//! [`HubOptions::send_buffer`] messages and a message for a full buffer
//! is dropped and counted ([`Hub::dropped`]), never queued without
//! bound; at most 1000 subscriptions per connection. A write that
//! stalls for [`HubOptions::write_timeout`] ends the connection.

use std::collections::{HashMap, HashSet};
use std::future::Future;
use std::net::SocketAddr;
use std::pin::Pin;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, MutexGuard, PoisonError};
use std::time::Duration;

use axum::body::Body;
use axum::extract::{ConnectInfo, Request, State};
use axum::http::{header, HeaderMap, HeaderValue, Method, StatusCode, Version};
use axum::response::Response;
use axum::routing::{any, MethodRouter};
use futures_util::{SinkExt, StreamExt};
use hyper_util::rt::TokioIo;
use serde::{Deserialize, Serialize};
use tokio::sync::{mpsc, watch};
use tokio_tungstenite::tungstenite::handshake::derive_accept_key;
use tokio_tungstenite::tungstenite::protocol::frame::coding::CloseCode;
use tokio_tungstenite::tungstenite::protocol::{CloseFrame, Role, WebSocketConfig};
use tokio_tungstenite::tungstenite::{Error as WsError, Message};
use tokio_tungstenite::WebSocketStream;

/// Go's default per-message read limit: 64 KiB.
pub const DEFAULT_MAX_MESSAGE_SIZE: usize = 65536;
/// Go's per-connection send buffer: 64 messages.
pub const DEFAULT_SEND_BUFFER: usize = 64;
/// Subscriptions one connection may hold.
pub const MAX_SUBSCRIPTIONS: usize = 1000;
/// Longest subscription pattern, in bytes. Go caps only the count.
pub const MAX_PATTERN_LEN: usize = 256;
/// Most dot-separated segments in a pattern.
pub const MAX_PATTERN_SEGMENTS: usize = 32;
/// Most `**` segments in a pattern: each one retries every suffix of
/// the topic, so the cost of a match grows with their number.
pub const MAX_PATTERN_DOUBLE_STARS: usize = 3;
/// Most bytes of subscription patterns one connection holds in all.
pub const MAX_SUBSCRIPTION_BYTES: usize = 64 * 1024;
/// How long one write may stall before the connection is dropped.
pub const DEFAULT_WRITE_TIMEOUT: Duration = Duration::from_secs(10);

/// [`Hub::publish`] after the hub closed.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HubClosed;

impl std::fmt::Display for HubClosed {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("hub closed")
    }
}

impl std::error::Error for HubClosed {}

/// Called with the client address and topic of a message dropped for a
/// client whose send buffer was full.
pub type OnDrop = Arc<dyn Fn(&str, &str) + Send + Sync>;

/// A hub's settings. The defaults are Go's.
#[derive(Clone)]
pub struct HubOptions {
    /// Read limit per message, in bytes.
    pub max_message_size: usize,
    /// Messages a connection may have waiting to be written.
    pub send_buffer: usize,
    /// Origins accepted beyond the request's own host, as host
    /// patterns (`*` and `?`): `app.example.com:8443`, `*.example.com`.
    /// A pattern with `://` is matched against `scheme://host`. Empty
    /// is same-origin only.
    pub accept_origins: Vec<String>,
    /// Close a connection that sends nothing (not even a ping) for this
    /// long. `None`, as in Go, never closes an idle connection.
    pub idle_timeout: Option<Duration>,
    /// A write stalled this long ends the connection; zero is never.
    pub write_timeout: Duration,
    /// Called for every dropped message.
    pub on_drop: Option<OnDrop>,
}

impl Default for HubOptions {
    fn default() -> Self {
        Self {
            max_message_size: DEFAULT_MAX_MESSAGE_SIZE,
            send_buffer: DEFAULT_SEND_BUFFER,
            accept_origins: Vec::new(),
            idle_timeout: None,
            write_timeout: DEFAULT_WRITE_TIMEOUT,
            on_drop: None,
        }
    }
}

/// One connection's subscriptions and the bytes they hold.
#[derive(Default)]
struct Subs {
    set: HashSet<String>,
    bytes: usize,
}

impl Subs {
    /// Stores `pattern` unless a bound refuses it.
    fn add(&mut self, pattern: &str) -> bool {
        if self.set.contains(pattern) {
            return self.set.len() <= MAX_SUBSCRIPTIONS;
        }
        let doubles = pattern.split('.').filter(|s| *s == "**").count();
        if self.set.len() >= MAX_SUBSCRIPTIONS
            || pattern.len() > MAX_PATTERN_LEN
            || pattern.split('.').count() > MAX_PATTERN_SEGMENTS
            || doubles > MAX_PATTERN_DOUBLE_STARS
            || self.bytes + pattern.len() > MAX_SUBSCRIPTION_BYTES
        {
            return false;
        }
        self.bytes += pattern.len();
        self.set.insert(pattern.to_string());
        true
    }

    fn remove(&mut self, pattern: &str) {
        if self.set.remove(pattern) {
            self.bytes -= pattern.len();
        }
    }
}

struct Entry {
    addr: String,
    topics: Arc<Mutex<Subs>>,
    tx: mpsc::Sender<Arc<str>>,
}

struct Inner {
    opts: HubOptions,
    clients: Mutex<HashMap<u64, Entry>>,
    next: AtomicU64,
    dropped: AtomicU64,
    closed: AtomicBool,
    stop: watch::Sender<bool>,
}

/// Manages websocket connections and topic subscriptions. Cheap to
/// clone; clones share the hub.
#[derive(Clone)]
pub struct Hub {
    inner: Arc<Inner>,
}

impl Default for Hub {
    fn default() -> Self {
        Self::new()
    }
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(PoisonError::into_inner)
}

/// The JSON envelope of every frame.
#[derive(Serialize, Deserialize, Default)]
struct Envelope {
    #[serde(rename = "type", default)]
    kind: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    topic: String,
}

fn envelope(kind: &str, topic: &str) -> Arc<str> {
    let e = Envelope {
        kind: kind.to_string(),
        topic: topic.to_string(),
    };
    Arc::from(serde_json::to_string(&e).unwrap_or_default())
}

impl Hub {
    /// A hub with Go's defaults.
    pub fn new() -> Self {
        Self::with_options(HubOptions::default())
    }

    /// A hub with `opts`.
    pub fn with_options(opts: HubOptions) -> Self {
        let (stop, _) = watch::channel(false);
        Self {
            inner: Arc::new(Inner {
                opts,
                clients: Mutex::new(HashMap::new()),
                next: AtomicU64::new(1),
                dropped: AtomicU64::new(0),
                closed: AtomicBool::new(false),
                stop,
            }),
        }
    }

    /// Messages dropped so far for clients whose buffer was full.
    pub fn dropped(&self) -> u64 {
        self.inner.dropped.load(Ordering::Relaxed)
    }

    /// Connections currently registered.
    pub fn clients(&self) -> usize {
        lock(&self.inner.clients).len()
    }

    /// Sends `payload` (as JSON) to every client subscribed to a
    /// pattern matching `topic`.
    ///
    /// # Errors
    ///
    /// [`HubClosed`] after [`Hub::close`]. A payload that does not
    /// serialise is sent as `null`.
    pub fn publish<T: Serialize + ?Sized>(
        &self,
        topic: &str,
        payload: &T,
    ) -> Result<(), HubClosed> {
        if self.inner.closed.load(Ordering::SeqCst) {
            return Err(HubClosed);
        }
        let raw = serde_json::to_string(payload).unwrap_or_else(|_| "null".into());
        let topic_json = serde_json::to_string(topic).unwrap_or_else(|_| "\"\"".into());
        let data: Arc<str> = Arc::from(format!(
            "{{\"type\":\"message\",\"topic\":{topic_json},\"payload\":{raw}}}"
        ));
        let clients = lock(&self.inner.clients);
        for c in clients.values() {
            let hit = lock(&c.topics).set.iter().any(|p| match_topic(p, topic));
            if hit && c.tx.try_send(data.clone()).is_err() {
                self.inner.dropped.fetch_add(1, Ordering::Relaxed);
                if let Some(f) = &self.inner.opts.on_drop {
                    f(&c.addr, topic);
                }
            }
        }
        Ok(())
    }

    /// Closes every connection (status 1001) and refuses new ones.
    /// Idempotent.
    pub fn close(&self) {
        self.inner.closed.store(true, Ordering::SeqCst);
        let _ = self.inner.stop.send(true);
        lock(&self.inner.clients).clear();
    }

    /// A route handler for the upgrade: `router.route("/ws", hub.route())`.
    pub fn route(&self) -> MethodRouter {
        any(upgrade).with_state(self.clone())
    }
}

async fn upgrade(State(hub): State<Hub>, mut req: Request) -> Response {
    if hub.inner.closed.load(Ordering::SeqCst) {
        return refuse(StatusCode::SERVICE_UNAVAILABLE, "hub closed");
    }
    let key = match check_upgrade(&req, &hub.inner.opts.accept_origins) {
        Ok(k) => k,
        Err(r) => return r,
    };
    let addr = req
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .map(|c| c.0.to_string())
        .unwrap_or_default();
    let on_upgrade = hyper::upgrade::on(&mut req);
    let h = hub.clone();
    tokio::spawn(async move {
        if let Ok(io) = on_upgrade.await {
            let cfg = WebSocketConfig::default()
                .max_message_size(Some(h.inner.opts.max_message_size))
                .max_frame_size(Some(h.inner.opts.max_message_size));
            let ws =
                WebSocketStream::from_raw_socket(TokioIo::new(io), Role::Server, Some(cfg)).await;
            run_conn(h, addr, ws).await;
        }
    });
    let mut resp = Response::new(Body::empty());
    *resp.status_mut() = StatusCode::SWITCHING_PROTOCOLS;
    let h = resp.headers_mut();
    h.insert(header::UPGRADE, HeaderValue::from_static("websocket"));
    h.insert(header::CONNECTION, HeaderValue::from_static("Upgrade"));
    if let Ok(v) = HeaderValue::from_str(&key) {
        h.insert(header::SEC_WEBSOCKET_ACCEPT, v);
    }
    resp
}

fn refuse(status: StatusCode, msg: &str) -> Response {
    let mut r = Response::new(Body::from(format!("{msg}\n")));
    *r.status_mut() = status;
    let h = r.headers_mut();
    h.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("text/plain; charset=utf-8"),
    );
    h.insert(
        header::X_CONTENT_TYPE_OPTIONS,
        HeaderValue::from_static("nosniff"),
    );
    r
}

fn has_token(h: &HeaderMap, name: header::HeaderName, token: &str) -> bool {
    h.get_all(name)
        .iter()
        .filter_map(|v| v.to_str().ok())
        .flat_map(|v| v.split(','))
        .any(|t| t.trim().eq_ignore_ascii_case(token))
}

/// The checks coder/websocket's `Accept` makes, in its order, then the
/// Origin check with the hub's patterns. Returns the accept key.
// The refusal is the response itself, sent once.
#[allow(clippy::result_large_err)]
fn check_upgrade(req: &Request, patterns: &[String]) -> Result<String, Response> {
    let h = req.headers();
    if req.version() < Version::HTTP_11 {
        return Err(refuse(
            StatusCode::UPGRADE_REQUIRED,
            "WebSocket protocol violation: handshake request must be at least HTTP/1.1",
        ));
    }
    let need_upgrade = |msg: String| {
        let mut r = refuse(StatusCode::UPGRADE_REQUIRED, &msg);
        r.headers_mut()
            .insert(header::CONNECTION, HeaderValue::from_static("Upgrade"));
        r.headers_mut()
            .insert(header::UPGRADE, HeaderValue::from_static("websocket"));
        r
    };
    if !has_token(h, header::CONNECTION, "upgrade") {
        return Err(need_upgrade(
            "WebSocket protocol violation: Connection header does not contain Upgrade".into(),
        ));
    }
    if !has_token(h, header::UPGRADE, "websocket") {
        return Err(need_upgrade(
            "WebSocket protocol violation: Upgrade header does not contain websocket".into(),
        ));
    }
    if req.method() != Method::GET {
        return Err(refuse(
            StatusCode::METHOD_NOT_ALLOWED,
            &format!(
                "WebSocket protocol violation: handshake request method is not GET but {:?}",
                req.method().as_str()
            ),
        ));
    }
    if h.get(header::SEC_WEBSOCKET_VERSION)
        .map(HeaderValue::as_bytes)
        != Some(b"13")
    {
        let mut r = refuse(
            StatusCode::BAD_REQUEST,
            "unsupported WebSocket protocol version (only 13 is supported)",
        );
        r.headers_mut().insert(
            header::SEC_WEBSOCKET_VERSION,
            HeaderValue::from_static("13"),
        );
        return Err(r);
    }
    let keys: Vec<_> = h.get_all(header::SEC_WEBSOCKET_KEY).iter().collect();
    let key = match keys.as_slice() {
        [] => {
            return Err(refuse(
                StatusCode::BAD_REQUEST,
                "WebSocket protocol violation: missing Sec-WebSocket-Key",
            ))
        }
        [k] => k.to_str().unwrap_or("").trim().to_string(),
        _ => {
            return Err(refuse(
                StatusCode::BAD_REQUEST,
                "WebSocket protocol violation: multiple Sec-WebSocket-Key headers",
            ))
        }
    };
    if !is_16_byte_base64(&key) {
        return Err(refuse(
            StatusCode::BAD_REQUEST,
            "WebSocket protocol violation: invalid Sec-WebSocket-Key, must be a 16 byte base64 encoded string",
        ));
    }
    authenticate_origin(h, patterns).map_err(|m| refuse(StatusCode::FORBIDDEN, &m))?;
    Ok(derive_accept_key(key.as_bytes()))
}

/// 16 bytes in standard base64 is 24 characters, the last two `=`.
fn is_16_byte_base64(s: &str) -> bool {
    let b = s.as_bytes();
    b.len() == 24
        && b[22..] == *b"=="
        && b[..22]
            .iter()
            .all(|c| c.is_ascii_alphanumeric() || *c == b'+' || *c == b'/')
}

/// coder/websocket's origin rule: no `Origin` header is fine (not a
/// browser); one naming the request's own host is fine; otherwise it
/// must match a pattern.
fn authenticate_origin(h: &HeaderMap, patterns: &[String]) -> Result<(), String> {
    let Some(origin) = h.get(header::ORIGIN).and_then(|v| v.to_str().ok()) else {
        return if h.contains_key(header::ORIGIN) {
            Err("failed to parse Origin header".into())
        } else {
            Ok(())
        };
    };
    if origin.is_empty() {
        return Ok(());
    }
    let (scheme, host) = split_origin(origin);
    let own = h
        .get(header::HOST)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    if own.eq_ignore_ascii_case(host) {
        return Ok(());
    }
    for p in patterns {
        let target = if p.contains("://") {
            format!("{scheme}://{host}")
        } else {
            host.to_string()
        };
        if glob(&p.to_lowercase(), &target.to_lowercase()) {
            return Ok(());
        }
    }
    if host.is_empty() {
        return Err(format!(
            "request Origin {origin:?} is not a valid URL with a host"
        ));
    }
    Err(format!(
        "request Origin {host:?} is not authorized for Host {own:?}"
    ))
}

/// The scheme and host (with port) of an origin URL; the host is empty
/// when it is not an absolute URL.
fn split_origin(origin: &str) -> (&str, &str) {
    let Some((scheme, rest)) = origin.split_once("://") else {
        return ("", "");
    };
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    let authority = &rest[..end];
    let host = authority.rsplit_once('@').map_or(authority, |(_, h)| h);
    (scheme, host)
}

/// `*` (any run without `/`) and `?` (one character), as `path.Match`.
fn glob(pattern: &str, s: &str) -> bool {
    fn go(p: &[u8], s: &[u8]) -> bool {
        match p.split_first() {
            None => s.is_empty(),
            Some((b'*', rest)) => {
                let mut i = 0;
                loop {
                    if go(rest, &s[i..]) {
                        return true;
                    }
                    if i >= s.len() || s[i] == b'/' {
                        return false;
                    }
                    i += 1;
                }
            }
            Some((b'?', rest)) => !s.is_empty() && s[0] != b'/' && go(rest, &s[1..]),
            Some((c, rest)) => s.first() == Some(c) && go(rest, &s[1..]),
        }
    }
    go(pattern.as_bytes(), s.as_bytes())
}

/// Whether `topic` matches `pattern`: `*` is exactly one segment; `**`
/// is one or more, and when more pattern follows it that is tried
/// against every suffix of the topic.
pub(crate) fn match_topic(pattern: &str, topic: &str) -> bool {
    if pattern == topic {
        return true;
    }
    let p: Vec<&str> = pattern.split('.').collect();
    let t: Vec<&str> = topic.split('.').collect();
    match_parts(&p, &t)
}

fn match_parts(pattern: &[&str], topic: &[&str]) -> bool {
    let (mut pi, mut ti) = (0, 0);
    while pi < pattern.len() && ti < topic.len() {
        if pattern[pi] == "**" {
            if pi == pattern.len() - 1 {
                return true;
            }
            return (ti..topic.len()).any(|k| match_parts(&pattern[pi + 1..], &topic[k..]));
        }
        if pattern[pi] != "*" && pattern[pi] != topic[ti] {
            return false;
        }
        pi += 1;
        ti += 1;
    }
    pi == pattern.len() && ti == topic.len()
}

type Ws = WebSocketStream<TokioIo<hyper::upgrade::Upgraded>>;

fn close_msg(code: u16, reason: &str) -> Message {
    Message::Close(Some(CloseFrame {
        code: CloseCode::from(code),
        reason: reason.to_string().into(),
    }))
}

async fn send(ws: &mut Ws, m: Message, timeout: Duration) -> bool {
    let fut = ws.send(m);
    if timeout.is_zero() {
        return fut.await.is_ok();
    }
    matches!(tokio::time::timeout(timeout, fut).await, Ok(Ok(())))
}

struct Registered {
    hub: Hub,
    id: u64,
}

impl Drop for Registered {
    fn drop(&mut self) {
        lock(&self.hub.inner.clients).remove(&self.id);
    }
}

async fn run_conn(hub: Hub, addr: String, mut ws: Ws) {
    let opts = &hub.inner.opts;
    let (tx, mut rx) = mpsc::channel::<Arc<str>>(opts.send_buffer.max(1));
    let topics = Arc::new(Mutex::new(Subs::default()));
    let id = hub.inner.next.fetch_add(1, Ordering::Relaxed);
    let mut stop = hub.inner.stop.subscribe();
    {
        let mut clients = lock(&hub.inner.clients);
        if hub.inner.closed.load(Ordering::SeqCst) {
            drop(clients);
            let _ = send(&mut ws, close_msg(1001, "hub shutdown"), opts.write_timeout).await;
            return;
        }
        clients.insert(
            id,
            Entry {
                addr,
                topics: topics.clone(),
                tx: tx.clone(),
            },
        );
    }
    let _registered = Registered {
        hub: hub.clone(),
        id,
    };
    let _ = tx.try_send(envelope("welcome", ""));

    let idle = |d: Option<Duration>| -> Pin<Box<dyn Future<Output = ()> + Send>> {
        match d {
            Some(d) => Box::pin(tokio::time::sleep(d)),
            None => Box::pin(std::future::pending()),
        }
    };
    let mut idle_timer = idle(opts.idle_timeout);
    // The code of the close frame to send, if any.
    let mut closing: Option<(u16, &str)> = Some((1000, ""));
    loop {
        tokio::select! {
            _ = stop.changed() => {
                closing = Some((1001, "hub shutdown"));
                break;
            }
            () = &mut idle_timer => {
                closing = Some((1001, "idle"));
                break;
            }
            out = rx.recv() => {
                let Some(text) = out else { break };
                if !send(&mut ws, Message::text(text.to_string()), opts.write_timeout).await {
                    closing = None;
                    break;
                }
            }
            inc = ws.next() => {
                idle_timer = idle(opts.idle_timeout);
                match inc {
                    Some(Ok(Message::Text(t))) => on_message(t.as_bytes(), &topics, &tx),
                    Some(Ok(Message::Binary(b))) => on_message(&b, &topics, &tx),
                    Some(Ok(Message::Close(_))) | None => {
                        closing = None;
                        break;
                    }
                    Some(Ok(_)) => {}
                    Some(Err(WsError::Capacity(_))) => {
                        closing = Some((1009, "message too big"));
                        break;
                    }
                    Some(Err(_)) => {
                        closing = None;
                        break;
                    }
                }
            }
        }
    }
    drop(_registered);
    if let Some((code, reason)) = closing {
        let _ = send(&mut ws, close_msg(code, reason), opts.write_timeout).await;
    }
    // Let the peer answer the close, bounded; polling is what completes it.
    let _ = tokio::time::timeout(Duration::from_secs(2), async {
        while let Some(Ok(_)) = ws.next().await {}
    })
    .await;
}

fn on_message(data: &[u8], topics: &Arc<Mutex<Subs>>, tx: &mpsc::Sender<Arc<str>>) {
    let Ok(msg) = serde_json::from_slice::<Envelope>(data) else {
        return;
    };
    match msg.kind.as_str() {
        "subscribe" => {
            let mut t = lock(topics);
            if !t.add(&msg.topic) {
                let _ = tx.try_send(envelope("error", &msg.topic));
                return;
            }
            let _ = tx.try_send(envelope("ack", &msg.topic));
        }
        "unsubscribe" => {
            lock(topics).remove(&msg.topic);
            let _ = tx.try_send(envelope("ack", &msg.topic));
        }
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn topics() {
        assert!(match_topic("a.b", "a.b"));
        assert!(match_topic("a.*", "a.b"));
        assert!(!match_topic("a.*", "a.b.c"));
        assert!(match_topic("a.**", "a.b.c"));
        assert!(match_topic("a.**", "a.b"));
        assert!(!match_topic("a.**", "a"));
        assert!(match_topic("a.**.z", "a.b.c.z"));
        assert!(!match_topic("a.**.z", "a.b.c"));
        assert!(!match_topic("*", "a.b"));
    }

    #[test]
    fn origins() {
        let mut h = HeaderMap::new();
        h.insert(header::HOST, HeaderValue::from_static("api.test:8080"));
        assert!(authenticate_origin(&h, &[]).is_ok());
        h.insert(
            header::ORIGIN,
            HeaderValue::from_static("http://API.test:8080"),
        );
        assert!(authenticate_origin(&h, &[]).is_ok());
        h.insert(
            header::ORIGIN,
            HeaderValue::from_static("https://evil.test"),
        );
        assert!(authenticate_origin(&h, &[]).is_err());
        assert!(authenticate_origin(&h, &["*.test".into()]).is_ok());
        assert!(authenticate_origin(&h, &["https://evil.test".into()]).is_ok());
        assert!(authenticate_origin(&h, &["http://evil.test".into()]).is_err());
        h.insert(header::ORIGIN, HeaderValue::from_static("evil.test"));
        // Not a URL with a host: refused, unless a pattern matches the empty
        // host, which, as in coder/websocket, `*` does.
        assert!(authenticate_origin(&h, &["a.test".into()]).is_err());
    }

    #[test]
    fn keys() {
        assert!(is_16_byte_base64("dGhlIHNhbXBsZSBub25jZQ=="));
        assert!(!is_16_byte_base64("dGhlIHNhbXBsZSBub25jZQ"));
        assert!(!is_16_byte_base64("short=="));
    }
}
