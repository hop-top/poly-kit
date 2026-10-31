//! Idempotency keys on the api-server. The corpus table in
//! `contracts/parity/api.json` (`idempotency`) is run through the
//! assembled router; Go runs the same table through the bridge's ledger
//! (`TestIdempotencyContract`) and is the authority.
#![cfg(feature = "api-server-idempotency")]

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;

use axum::body::Body;
use axum::extract::State;
use axum::http::{header, HeaderMap, Method, Request, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::Router;
use hop_top_kit::api_server::idempotency::{
    IdempotencyConfig, CODE_IDEMPOTENCY_CONFLICT, CODE_IDEMPOTENCY_KEY_REUSED,
    DEFAULT_IDEMPOTENCY_TTL, HEADER_IDEMPOTENCY_KEY, HEADER_IDEMPOTENT_REPLAYED,
};
use hop_top_kit::api_server::{ApiServerConfig, ApiService};
use hop_top_kit::authn::{AuthFunc, AuthnError, Claims};
use hop_top_kit::kv::SqliteStore;
use serde_json::{json, Value};
use tokio::sync::Notify;
use tower::ServiceExt;

fn corpus() -> Value {
    let path = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../contracts/parity/api.json");
    serde_json::from_slice(&std::fs::read(path).expect("read api.json")).expect("JSON")
}

#[derive(Clone)]
struct Handler {
    runs: Arc<AtomicUsize>,
    began: Arc<Notify>,
    gate: Arc<Notify>,
}

async fn widgets(State(h): State<Handler>, headers: HeaderMap, body: String) -> Response {
    let n = h.runs.fetch_add(1, Ordering::SeqCst) + 1;
    if headers.contains_key("x-hold") {
        h.began.notify_one();
        h.gate.notified().await;
    }
    if headers.contains_key("x-fail") {
        return (StatusCode::INTERNAL_SERVER_ERROR, "failed").into_response();
    }
    (
        StatusCode::CREATED,
        [("x-widget", "w1"), ("set-cookie", "a=b")],
        format!("run {n}: {body}"),
    )
        .into_response()
}

struct Harness {
    app: Router,
    h: Handler,
    store_path: tempfile::TempDir,
}

fn auth() -> AuthFunc {
    Arc::new(|c| match c.authorization {
        Some(t) if t.starts_with("Bearer ") => Ok(Claims {
            subject: t[7..].to_string(),
            ..Default::default()
        }),
        _ => Err(AuthnError::NoToken(None)),
    })
}

fn harness_with(f: impl FnOnce(&mut IdempotencyConfig)) -> Harness {
    let h = Handler {
        runs: Arc::new(AtomicUsize::new(0)),
        began: Arc::new(Notify::new()),
        gate: Arc::new(Notify::new()),
    };
    let routes = Router::new()
        .route("/widgets", post(widgets).get(|| async { "list" }))
        .route(
            "/stream",
            post(|| async { ([(header::CONTENT_TYPE, "text/event-stream")], "data: x\n\n") }),
        )
        .route("/plain", get(|| async { "x" }))
        .with_state(h.clone());
    let dir = tempfile::tempdir().unwrap();
    let store = SqliteStore::new(dir.path().join("idem.db").to_str().unwrap()).unwrap();
    let mut idem = IdempotencyConfig::new(store);
    f(&mut idem);
    let mut cfg = ApiServerConfig::new("127.0.0.1:8080", routes);
    cfg.log = Some(Arc::new(|_| {}));
    cfg.auth = Some(auth());
    cfg.idempotency = Some(idem);
    Harness {
        app: ApiService::build(cfg).unwrap().router(),
        h,
        store_path: dir,
    }
}

fn harness() -> Harness {
    harness_with(|_| {})
}

fn post_req(principal: &str, key: &str, body: &str) -> Request<Body> {
    let mut r = Request::builder()
        .method(Method::POST)
        .uri("/widgets")
        .header("host", "127.0.0.1:8080")
        .header("authorization", format!("Bearer {principal}"));
    if !key.is_empty() {
        r = r.header(HEADER_IDEMPOTENCY_KEY, key);
    }
    r.body(Body::from(body.to_string())).unwrap()
}

async fn body_of(r: Response) -> String {
    String::from_utf8(
        axum::body::to_bytes(r.into_body(), usize::MAX)
            .await
            .unwrap()
            .to_vec(),
    )
    .unwrap()
}

#[test]
fn constants_match_the_corpus() {
    let k = &corpus()["constants"]["idempotency"];
    assert_eq!(k["key_header"], HEADER_IDEMPOTENCY_KEY);
    assert_eq!(k["replayed_header"], HEADER_IDEMPOTENT_REPLAYED);
    assert_eq!(k["codes"][CODE_IDEMPOTENCY_CONFLICT], 409);
    assert_eq!(k["codes"][CODE_IDEMPOTENCY_KEY_REUSED], 422);
    assert_eq!(k["default_ttl_seconds"], DEFAULT_IDEMPOTENCY_TTL.as_secs());
}

#[tokio::test]
async fn the_corpus_table() {
    for case in corpus()["idempotency"].as_array().unwrap() {
        let name = case["name"].as_str().unwrap();
        let hz = harness();
        let mut held: Option<tokio::task::JoinHandle<Response>> = None;
        for s in case["steps"].as_array().unwrap() {
            let call = |c: &str| {
                if c == "A" {
                    r#"{"name":"a"}"#
                } else {
                    r#"{"name":"b"}"#
                }
            };
            match s["op"].as_str().unwrap() {
                "hold" => {
                    let mut req = post_req(
                        s["principal"].as_str().unwrap(),
                        s["key"].as_str().unwrap(),
                        call(s["call"].as_str().unwrap()),
                    );
                    req.headers_mut().insert("x-hold", "1".parse().unwrap());
                    let app = hz.app.clone();
                    held = Some(tokio::spawn(async move { app.oneshot(req).await.unwrap() }));
                    hz.h.began.notified().await;
                }
                "release" => {
                    hz.h.gate.notify_one();
                    let r = held.take().unwrap().await.unwrap();
                    assert_eq!(r.status(), 201, "{name}: the held call finished");
                }
                "call" => {
                    let mut req = post_req(
                        s["principal"].as_str().unwrap(),
                        s["key"].as_str().unwrap(),
                        call(s["call"].as_str().unwrap()),
                    );
                    if s["fail"].as_bool().unwrap_or(false) {
                        req.headers_mut().insert("x-fail", "1".parse().unwrap());
                    }
                    let before = hz.h.runs.load(Ordering::SeqCst);
                    let resp = hz.app.clone().oneshot(req).await.unwrap();
                    let replayed = resp.headers().get(HEADER_IDEMPOTENT_REPLAYED).is_some();
                    let status = resp.status().as_u16();
                    let v: Option<Value> = serde_json::from_str(&body_of(resp).await).ok();
                    let got = match (status, v.as_ref().and_then(|v| v["code"].as_str())) {
                        (409, Some(CODE_IDEMPOTENCY_CONFLICT)) => "conflict",
                        (422, Some(CODE_IDEMPOTENCY_KEY_REUSED)) => "key_reused",
                        _ if replayed => "replay",
                        _ => "run",
                    };
                    assert_eq!(got, s["want"], "{name}: {s}");
                    let ran = hz.h.runs.load(Ordering::SeqCst) != before;
                    assert_eq!(ran, got == "run", "{name}: the handler runs only on a run");
                }
                op => panic!("unknown op {op}"),
            }
        }
    }
}

#[tokio::test]
async fn a_replay_is_the_recorded_response() {
    let hz = harness();
    let first = hz
        .app
        .clone()
        .oneshot(post_req("alice", "k1", "{}"))
        .await
        .unwrap();
    assert_eq!(first.status(), 201);
    assert!(first.headers().get(HEADER_IDEMPOTENT_REPLAYED).is_none());
    let first_body = body_of(first).await;
    assert_eq!(first_body, "run 1: {}");

    let again = hz
        .app
        .clone()
        .oneshot(post_req("alice", "k1", "{}"))
        .await
        .unwrap();
    assert_eq!(again.status(), 201);
    assert_eq!(again.headers()[HEADER_IDEMPOTENT_REPLAYED], "true");
    assert_eq!(again.headers()["x-widget"], "w1", "headers replay");
    assert!(
        again.headers().get(header::SET_COOKIE).is_none(),
        "a cookie is never replayed to the second caller"
    );
    assert_eq!(
        body_of(again).await,
        first_body,
        "the first run's body, byte for byte"
    );
    assert_eq!(hz.h.runs.load(Ordering::SeqCst), 1);
}

#[tokio::test]
async fn the_body_is_part_of_the_call() {
    let hz = harness();
    hz.app
        .clone()
        .oneshot(post_req("alice", "k", r#"{"a":1}"#))
        .await
        .unwrap();
    let r = hz
        .app
        .clone()
        .oneshot(post_req("alice", "k", r#"{"a":2}"#))
        .await
        .unwrap();
    assert_eq!(r.status(), 422);
    assert_eq!(hz.h.runs.load(Ordering::SeqCst), 1);
}

#[tokio::test]
async fn the_path_is_part_of_the_call() {
    let hz = harness();
    hz.app
        .clone()
        .oneshot(post_req("alice", "k", "{}"))
        .await
        .unwrap();
    let mut other = post_req("alice", "k", "{}");
    *other.uri_mut() = "/widgets?x=1".parse().unwrap();
    let r = hz.app.clone().oneshot(other).await.unwrap();
    assert_eq!(r.status(), 422);
}

#[tokio::test]
async fn safe_methods_and_keyless_requests_are_never_touched() {
    let hz = harness();
    for _ in 0..2 {
        let r = hz
            .app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/plain")
                    .header("host", "127.0.0.1:8080")
                    .header("authorization", "Bearer alice")
                    .header(HEADER_IDEMPOTENCY_KEY, "k")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(r.status(), 200);
        assert!(r.headers().get(HEADER_IDEMPOTENT_REPLAYED).is_none());
    }
    for _ in 0..2 {
        hz.app
            .clone()
            .oneshot(post_req("alice", "", "{}"))
            .await
            .unwrap();
    }
    assert_eq!(hz.h.runs.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn the_key_is_scoped_after_authentication() {
    // An unauthenticated request is refused before the ledger sees it.
    let hz = harness();
    let r = hz
        .app
        .clone()
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/widgets")
                .header("host", "127.0.0.1:8080")
                .header(HEADER_IDEMPOTENCY_KEY, "k")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(r.status(), 401);
    assert_eq!(hz.h.runs.load(Ordering::SeqCst), 0);
}

#[tokio::test]
async fn a_stream_is_not_recorded_and_frees_its_key() {
    let hz = harness();
    let stream = || {
        Request::builder()
            .method(Method::POST)
            .uri("/stream")
            .header("host", "127.0.0.1:8080")
            .header("authorization", "Bearer alice")
            .header(HEADER_IDEMPOTENCY_KEY, "s")
            .body(Body::empty())
            .unwrap()
    };
    for _ in 0..2 {
        let r = hz.app.clone().oneshot(stream()).await.unwrap();
        assert_eq!(r.status(), 200);
        assert!(r.headers().get(HEADER_IDEMPOTENT_REPLAYED).is_none());
    }
}

#[tokio::test]
async fn an_oversized_response_is_not_recorded() {
    let hz = harness_with(|c| c.max_record_bytes = 8);
    for _ in 0..2 {
        let r = hz
            .app
            .clone()
            .oneshot(post_req("alice", "k", "{}"))
            .await
            .unwrap();
        assert_eq!(r.status(), 201);
        assert!(r.headers().get(HEADER_IDEMPOTENT_REPLAYED).is_none());
    }
    assert_eq!(hz.h.runs.load(Ordering::SeqCst), 2, "ran both times");
}

#[tokio::test]
async fn an_oversized_request_is_refused_413() {
    let hz = harness_with(|c| c.max_record_bytes = 4);
    let r = hz
        .app
        .clone()
        .oneshot(post_req("alice", "k", "0123456789"))
        .await
        .unwrap();
    assert_eq!(r.status(), 413);
    assert_eq!(hz.h.runs.load(Ordering::SeqCst), 0);
}

#[tokio::test]
async fn a_cancelled_request_frees_its_key() {
    let hz = harness();
    let mut req = post_req("alice", "k", "{}");
    req.headers_mut().insert("x-hold", "1".parse().unwrap());
    let app = hz.app.clone();
    let t = tokio::spawn(async move { app.oneshot(req).await });
    hz.h.began.notified().await;
    t.abort(); // the client went away mid-flight
    let _ = t.await;
    tokio::time::sleep(Duration::from_millis(50)).await;
    let r = hz
        .app
        .clone()
        .oneshot(post_req("alice", "k", "{}"))
        .await
        .unwrap();
    assert_eq!(r.status(), 201, "the key is free, not held as running");
    assert_eq!(
        r.headers().get(HEADER_IDEMPOTENT_REPLAYED),
        None,
        "and nothing was recorded"
    );
}

#[tokio::test]
async fn a_recorded_response_expires_with_its_ttl() {
    let hz = harness_with(|c| c.ttl = Duration::from_millis(150));
    hz.app
        .clone()
        .oneshot(post_req("alice", "k", "{}"))
        .await
        .unwrap();
    tokio::time::sleep(Duration::from_millis(300)).await;
    let r = hz
        .app
        .clone()
        .oneshot(post_req("alice", "k", "{}"))
        .await
        .unwrap();
    assert!(r.headers().get(HEADER_IDEMPOTENT_REPLAYED).is_none());
    assert_eq!(hz.h.runs.load(Ordering::SeqCst), 2);
    let _ = (&hz.store_path, json!(null));
}

#[tokio::test]
async fn two_servers_on_one_store_share_the_claim() {
    // The claim is an atomic put-if-absent in the store: a key running
    // on one server conflicts on another that shares it.
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("shared.db");
    let mk = |gate: Arc<Notify>, began: Arc<Notify>| {
        let h = Handler {
            runs: Arc::new(AtomicUsize::new(0)),
            began,
            gate,
        };
        let routes = Router::new()
            .route("/widgets", post(widgets))
            .with_state(h.clone());
        let mut cfg = ApiServerConfig::new("127.0.0.1:8080", routes);
        cfg.log = Some(Arc::new(|_| {}));
        cfg.auth = Some(auth());
        cfg.idempotency = Some(IdempotencyConfig::new(
            SqliteStore::new(path.to_str().unwrap()).unwrap(),
        ));
        (ApiService::build(cfg).unwrap().router(), h)
    };
    let (a, ha) = mk(Arc::new(Notify::new()), Arc::new(Notify::new()));
    let (b, hb) = mk(Arc::new(Notify::new()), Arc::new(Notify::new()));
    let mut req = post_req("alice", "k", "{}");
    req.headers_mut().insert("x-hold", "1".parse().unwrap());
    let t = tokio::spawn(async move { a.oneshot(req).await.unwrap() });
    ha.began.notified().await;
    let r = b
        .clone()
        .oneshot(post_req("alice", "k", "{}"))
        .await
        .unwrap();
    assert_eq!(r.status(), 409);
    assert_eq!(hb.runs.load(Ordering::SeqCst), 0);
    ha.gate.notify_one();
    t.await.unwrap();
    let r = b.oneshot(post_req("alice", "k", "{}")).await.unwrap();
    assert_eq!(
        r.headers()[HEADER_IDEMPOTENT_REPLAYED],
        "true",
        "replays across servers"
    );
}
