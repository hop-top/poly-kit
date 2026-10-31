//! Idempotency keys, the HTTP form of Go's idempotency replay
//! (`go/transport/api/idempotency.go` and the bridge's ledger in
//! `cmdsurface`). Feature `api-server-idempotency`.
//!
//! A mutating request (anything but `GET`, `HEAD`, `OPTIONS`, `TRACE`)
//! carrying an `Idempotency-Key` header is answered from the ledger when
//! the same caller already ran the same request under that key: the
//! recorded response comes back marked `Idempotent-Replayed: true` and
//! nothing runs. A request the ledger has not seen runs, and a
//! successful (2xx) response is recorded for the TTL.
//!
//! - The key is scoped to the caller, as Go scopes it: a verified
//!   principal to its tenant and principal, any other caller to its
//!   client address. Store keys are hashes, so the store holds no
//!   principal or key in the clear.
//! - The request is identified by its method, path and query and body
//!   hash. A key reused for a different request is refused 422
//!   `idempotency_key_reused`, whether the first call is recorded or
//!   still running.
//! - A key whose request is still running is refused 409
//!   `idempotency_conflict`. The claim is an atomic put-if-absent in the
//!   kv store, so it holds across processes sharing the store (Go's
//!   in-flight set is per process).
//! - A response that is not 2xx, is a stream, or is larger than
//!   [`IdempotencyConfig::max_record_bytes`] is not recorded and the key
//!   is released: a retry runs again.
//!
//! Place the layer inside authentication, so the scope is the verified
//! caller; the server does.

use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use axum::body::{to_bytes, Body, HttpBody};
use axum::extract::{Request, State};
use axum::http::{header, HeaderName, HeaderValue, Method, StatusCode};
use axum::middleware::Next;
use axum::response::Response;
use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use super::ctx::{ClientIp, PeerAddr};
use super::error::{ApiError, CODE_BODY_TOO_LARGE};
use crate::authn::Claims;
use crate::kv::TtlInserter;

/// The request header carrying the key (IETF Idempotency-Key).
pub const HEADER_IDEMPOTENCY_KEY: &str = "Idempotency-Key";
/// Set to `true` on a response answered from the ledger.
pub const HEADER_IDEMPOTENT_REPLAYED: &str = "Idempotent-Replayed";
/// The 409 refusal of a key whose request is still running.
pub const CODE_IDEMPOTENCY_CONFLICT: &str = "idempotency_conflict";
/// The 422 refusal of a key reused for a different request.
pub const CODE_IDEMPOTENCY_KEY_REUSED: &str = "idempotency_key_reused";
/// The 503 refusal when the ledger store cannot be used.
pub const CODE_IDEMPOTENCY_UNAVAILABLE: &str = "idempotency_unavailable";
/// How long a recorded response replays: 24 hours, Go's default.
pub const DEFAULT_IDEMPOTENCY_TTL: Duration = Duration::from_secs(24 * 3600);
/// How long an unfinished claim holds a key if its request never
/// settles (a crashed process).
pub const DEFAULT_PENDING_TTL: Duration = Duration::from_secs(120);
/// Largest request body hashed, and response body recorded: 1 MiB.
pub const DEFAULT_MAX_RECORD_BYTES: usize = 1 << 20;

type Store = Arc<Mutex<Box<dyn TtlInserter + Send>>>;

/// The ledger's configuration.
#[derive(Clone)]
pub struct IdempotencyConfig {
    store: Store,
    /// How long a recorded response replays; zero is
    /// [`DEFAULT_IDEMPOTENCY_TTL`].
    pub ttl: Duration,
    /// How long an unfinished claim holds its key; zero is
    /// [`DEFAULT_PENDING_TTL`].
    pub pending_ttl: Duration,
    /// Largest request body the layer reads and response body it
    /// records; zero is [`DEFAULT_MAX_RECORD_BYTES`].
    pub max_record_bytes: usize,
}

impl IdempotencyConfig {
    /// A ledger over `store` with the defaults. Share one store between
    /// the processes that should answer for the same keys.
    pub fn new(store: impl TtlInserter + Send + 'static) -> Self {
        Self {
            store: Arc::new(Mutex::new(Box::new(store))),
            ttl: Duration::ZERO,
            pending_ttl: Duration::ZERO,
            max_record_bytes: 0,
        }
    }
}

#[derive(Clone)]
pub(crate) struct IdemState {
    store: Store,
    ttl: Duration,
    pending_ttl: Duration,
    max: usize,
}

impl IdemState {
    pub(crate) fn new(c: &IdempotencyConfig) -> Self {
        let or = |d: Duration, def: Duration| if d.is_zero() { def } else { d };
        Self {
            store: c.store.clone(),
            ttl: or(c.ttl, DEFAULT_IDEMPOTENCY_TTL),
            pending_ttl: or(c.pending_ttl, DEFAULT_PENDING_TTL),
            max: if c.max_record_bytes == 0 {
                DEFAULT_MAX_RECORD_BYTES
            } else {
                c.max_record_bytes
            },
        }
    }

    fn with<T>(
        &self,
        f: impl FnOnce(&dyn TtlInserter) -> Result<T, crate::kv::KvError>,
    ) -> Result<T, ()> {
        let g = self.store.lock().unwrap_or_else(PoisonError::into_inner);
        f(g.as_ref()).map_err(|_| ())
    }
}

#[derive(Serialize, Deserialize)]
struct Record {
    /// Fingerprint of the request the key was used for.
    fp: String,
    /// False while the request runs.
    done: bool,
    #[serde(default)]
    status: u16,
    #[serde(default)]
    headers: Vec<(String, String)>,
    #[serde(default)]
    body: String,
}

fn hex(b: &[u8]) -> String {
    b.iter().map(|x| format!("{x:02x}")).collect()
}

fn sha(parts: &[&[u8]]) -> Vec<u8> {
    let mut h = Sha256::new();
    for (i, p) in parts.iter().enumerate() {
        if i > 0 {
            h.update([0]);
        }
        h.update(p);
    }
    h.finalize().to_vec()
}

/// The caller's scope: a verified principal to tenant and principal,
/// anyone else to the client address.
fn scope(req: &axum::http::request::Parts) -> String {
    let ext = &req.extensions;
    if let Some(c) = ext.get::<Claims>() {
        return hex(&sha(&[
            b"verified",
            c.tenant.as_bytes(),
            c.subject.as_bytes(),
        ]));
    }
    let host = ext
        .get::<ClientIp>()
        .map(|c| c.0.to_string())
        .or_else(|| ext.get::<PeerAddr>().map(|p| p.0.ip().to_string()))
        .or_else(|| {
            ext.get::<axum::extract::ConnectInfo<std::net::SocketAddr>>()
                .map(|c| c.0.ip().to_string())
        })
        .unwrap_or_default();
    hex(&sha(&[b"claimed", b"http", host.as_bytes()]))
}

fn refuse(status: u16, code: &str, msg: &str) -> Response {
    ApiError::new(status, code, msg).into_response()
}

/// Headers a replay must not carry: connection-level, or set per
/// response by outer layers.
fn recordable(name: &HeaderName) -> bool {
    !matches!(
        name.as_str(),
        "set-cookie"
            | "date"
            | "x-request-id"
            | "content-length"
            | "transfer-encoding"
            | "connection"
            | "keep-alive"
            | "upgrade"
            | "idempotent-replayed"
            | "www-authenticate"
    )
}

fn replay(rec: Record) -> Response {
    let body = B64.decode(rec.body).unwrap_or_default();
    let mut resp = Response::new(Body::from(body));
    *resp.status_mut() = StatusCode::from_u16(rec.status).unwrap_or(StatusCode::OK);
    for (k, v) in rec.headers {
        if let (Ok(k), Some(v)) = (
            HeaderName::try_from(k),
            B64.decode(v)
                .ok()
                .and_then(|b| HeaderValue::from_bytes(&b).ok()),
        ) {
            resp.headers_mut().append(k, v);
        }
    }
    resp.headers_mut().insert(
        HeaderName::from_static("idempotent-replayed"),
        HeaderValue::from_static("true"),
    );
    resp
}

/// Frees a claim unless it was settled, including when the request is
/// cancelled mid-flight.
struct Claim {
    st: IdemState,
    key: String,
    live: bool,
}

impl Drop for Claim {
    fn drop(&mut self) {
        if self.live {
            let _ = self.st.with(|s| s.delete(self.key.as_bytes()));
        }
    }
}

pub(crate) async fn idempotency(State(st): State<IdemState>, req: Request, next: Next) -> Response {
    if matches!(
        *req.method(),
        Method::GET | Method::HEAD | Method::OPTIONS | Method::TRACE
    ) {
        return next.run(req).await;
    }
    let Some(key) = req
        .headers()
        .get(HEADER_IDEMPOTENCY_KEY)
        .and_then(|v| v.to_str().ok())
        .filter(|k| !k.is_empty())
        .map(str::to_string)
    else {
        return next.run(req).await;
    };

    let (parts, body) = req.into_parts();
    let declared = parts
        .headers
        .get(header::CONTENT_LENGTH)
        .and_then(|v| v.to_str().ok())
        .and_then(|s| s.parse::<usize>().ok());
    let too_large = || {
        refuse(
            413,
            CODE_BODY_TOO_LARGE,
            &format!("request body exceeds {} bytes", st.max),
        )
    };
    if declared.is_some_and(|n| n > st.max) {
        return too_large();
    }
    let bytes = match to_bytes(body, st.max).await {
        Ok(b) => b,
        Err(_) => return too_large(),
    };

    let store_key = hex(&sha(&[b"served", scope(&parts).as_bytes(), key.as_bytes()]));
    let pq = parts
        .uri
        .path_and_query()
        .map_or(parts.uri.path(), |p| p.as_str());
    let fp = hex(&sha(&[
        parts.method.as_str().as_bytes(),
        pq.as_bytes(),
        &sha(&[&bytes]),
    ]));

    let pending = serde_json::to_vec(&Record {
        fp: fp.clone(),
        done: false,
        status: 0,
        headers: Vec::new(),
        body: String::new(),
    })
    .unwrap_or_default();
    let unavailable = || {
        refuse(
            503,
            CODE_IDEMPOTENCY_UNAVAILABLE,
            "the idempotency store is unavailable",
        )
    };

    // Claim the key, or learn what holds it. A record that expires
    // between the two is claimed again once.
    let mut claimed = false;
    for _ in 0..2 {
        match st.with(|s| s.put_if_absent_with_ttl(store_key.as_bytes(), &pending, st.pending_ttl))
        {
            Err(()) => return unavailable(),
            Ok(true) => {
                claimed = true;
                break;
            }
            Ok(false) => {}
        }
        let held = match st.with(|s| s.get(store_key.as_bytes())) {
            Err(()) => return unavailable(),
            Ok(h) => h,
        };
        let Some(raw) = held else { continue };
        let Ok(rec) = serde_json::from_slice::<Record>(&raw) else {
            // A record this version cannot read is no record: replace it.
            let _ = st.with(|s| s.delete(store_key.as_bytes()));
            continue;
        };
        if rec.fp != fp {
            return refuse(
                422,
                CODE_IDEMPOTENCY_KEY_REUSED,
                "api: idempotency key reused with a different call",
            );
        }
        if !rec.done {
            return refuse(
                409,
                CODE_IDEMPOTENCY_CONFLICT,
                "api: idempotency key in use by a call still running",
            );
        }
        return replay(rec);
    }
    if !claimed {
        return refuse(
            409,
            CODE_IDEMPOTENCY_CONFLICT,
            "api: idempotency key in use by a call still running",
        );
    }

    let mut claim = Claim {
        st: st.clone(),
        key: store_key.clone(),
        live: true,
    };
    let resp = next
        .run(Request::from_parts(parts, Body::from(bytes)))
        .await;

    // Only a bounded, complete success is recorded.
    let streaming = resp
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .is_some_and(|v| v.starts_with("text/event-stream"));
    let bounded = resp
        .body()
        .size_hint()
        .upper()
        .is_some_and(|n| n as usize <= st.max);
    if !resp.status().is_success() || streaming || !bounded {
        return resp; // Claim drops: the key is released.
    }
    let (rparts, rbody) = resp.into_parts();
    let Ok(rbytes) = to_bytes(rbody, st.max).await else {
        return refuse(500, "internal", "response body could not be read");
    };
    let rec = Record {
        fp,
        done: true,
        status: rparts.status.as_u16(),
        headers: rparts
            .headers
            .iter()
            .filter(|(k, _)| recordable(k))
            .map(|(k, v)| (k.as_str().to_string(), B64.encode(v.as_bytes())))
            .collect(),
        body: B64.encode(&rbytes),
    };
    // A record that cannot be written is dropped: the work happened and
    // its answer still reaches the caller; the claim is released and a
    // later call with the key runs again.
    if let Ok(raw) = serde_json::to_vec(&rec) {
        if st
            .with(|s| s.put_with_ttl(store_key.as_bytes(), &raw, st.ttl))
            .is_ok()
        {
            claim.live = false;
        }
    }
    Response::from_parts(rparts, Body::from(rbytes))
}
