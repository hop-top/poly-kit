//! The REST server's middleware stack and lifecycle, through the
//! assembled router and over a real listener.
#![cfg(feature = "api-server")]

use std::net::SocketAddr;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime};

use axum::body::Body;
use axum::extract::{ConnectInfo, Extension};
use axum::http::{header, Method, Request, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{any, get, post};
use axum::Router;
use hop_top_kit::api_server::{
    json_response, sse, ApiServerConfig, ApiService, Authenticated, CorsConfig, EventPublisher,
    OpenApi, Operation, RateLimit, ReadinessCheck, Topics,
};
use hop_top_kit::authn::{AuthFunc, AuthnError, Claims};
use hop_top_kit::breaker::Clock;
use hop_top_kit::serve::{
    CancelToken, Service, ServiceConfig, ServiceConfigs, ServiceRegistry, Supervisor,
    SupervisorOptions,
};
use serde_json::{json, Value};
use tower::ServiceExt;

/// A config for tests: quiet log, loopback, defaults otherwise.
fn cfg(routes: Router) -> ApiServerConfig {
    let mut c = ApiServerConfig::new("127.0.0.1:0", routes);
    c.log = Some(Arc::new(|_| {}));
    c
}

fn app(c: ApiServerConfig) -> Router {
    ApiService::build(c).expect("build").router()
}

fn get_req(path: &str) -> Request<Body> {
    Request::builder()
        .uri(path)
        .header("host", "127.0.0.1:8080")
        .body(Body::empty())
        .unwrap()
}

async fn text(resp: Response) -> String {
    String::from_utf8(
        axum::body::to_bytes(resp.into_body(), usize::MAX)
            .await
            .unwrap()
            .to_vec(),
    )
    .unwrap()
}

async fn json_of(resp: Response) -> Value {
    serde_json::from_str(&text(resp).await).unwrap()
}

fn hello() -> Router {
    Router::new()
        .route("/hello", get(|| async { "hi" }))
        .route("/echo", post(|body: String| async move { body }))
}

// --- request id ----------------------------------------------------------

#[tokio::test]
async fn request_id_is_kept_or_minted() {
    let a = app(cfg(hello()));
    let kept = a
        .clone()
        .oneshot(
            Request::builder()
                .uri("/hello")
                .header("host", "127.0.0.1")
                .header("x-request-id", "abc-123")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(kept.headers()["x-request-id"], "abc-123");

    let minted = a.oneshot(get_req("/hello")).await.unwrap();
    let id = minted.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_string();
    assert_eq!(id.len(), 32, "16 random bytes, hex");
    assert!(id.bytes().all(|b| b.is_ascii_hexdigit()));
}

// --- security headers ----------------------------------------------------

#[tokio::test]
async fn security_headers_default_on_and_hsts_follows_trusted_proto() {
    let mut c = cfg(hello());
    c.trusted_proxies = vec!["10.0.0.0/8".into()];
    let a = app(c);

    let plain = a.clone().oneshot(get_req("/hello")).await.unwrap();
    let h = plain.headers();
    assert_eq!(h["x-content-type-options"], "nosniff");
    assert_eq!(h["referrer-policy"], "no-referrer");
    assert!(h["content-security-policy"]
        .to_str()
        .unwrap()
        .starts_with("default-src 'none'"));
    assert!(h.get("strict-transport-security").is_none());

    let hsts = |peer: &str, proto: Option<&str>| {
        let a = a.clone();
        let peer: SocketAddr = peer.parse().unwrap();
        let proto = proto.map(String::from);
        async move {
            let mut r = Request::builder().uri("/hello").header("host", "127.0.0.1");
            if let Some(p) = proto {
                r = r.header("x-forwarded-proto", p);
            }
            let mut r = r.body(Body::empty()).unwrap();
            r.extensions_mut().insert(ConnectInfo(peer));
            a.oneshot(r)
                .await
                .unwrap()
                .headers()
                .get("strict-transport-security")
                .cloned()
        }
    };
    assert_eq!(
        hsts("10.0.0.2:1", Some("https")).await.unwrap(),
        "max-age=31536000"
    );
    assert!(hsts("203.0.113.9:1", Some("https")).await.is_none());
    assert!(hsts("10.0.0.2:1", Some("http")).await.is_none());
    assert!(hsts("10.0.0.2:1", None).await.is_none());

    let mut off = cfg(hello());
    off.security_headers = false;
    let resp = app(off).oneshot(get_req("/hello")).await.unwrap();
    assert!(resp.headers().get("x-content-type-options").is_none());
}

#[tokio::test]
async fn a_handler_set_header_wins() {
    let routes = Router::new().route(
        "/x",
        get(|| async { ([("content-security-policy", "default-src 'self'")], "x") }),
    );
    let resp = app(cfg(routes)).oneshot(get_req("/x")).await.unwrap();
    assert_eq!(
        resp.headers()["content-security-policy"],
        "default-src 'self'"
    );
}

// --- health ---------------------------------------------------------------

#[tokio::test]
async fn health_routes_answer_before_the_guards() {
    let ready = Arc::new(AtomicBool::new(true));
    let r = ready.clone();
    let mut c = cfg(hello());
    c.auth = Some(Arc::new(|_| Err(AuthnError::NoToken(None))));
    c.readiness = vec![ReadinessCheck::new("db", move || r.load(Ordering::SeqCst))];
    let a = app(c);

    // No credentials, and a Host no check would allow: still answered.
    let probe = |path: &'static str, method: Method| {
        let a = a.clone();
        async move {
            a.oneshot(
                Request::builder()
                    .method(method)
                    .uri(path)
                    .header("host", "10.9.9.9")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap()
        }
    };
    let live = probe("/healthz", Method::GET).await;
    assert_eq!(live.status(), 200);
    assert_eq!(live.headers()["cache-control"], "no-store");
    assert_eq!(json_of(live).await, json!({"status": "ok"}));

    // Not up: the listener's own readiness is false before start.
    let not_up = probe("/readyz", Method::GET).await;
    assert_eq!(not_up.status(), 503);
    assert_eq!(
        json_of(not_up).await,
        json!({"status": "unavailable", "failing": ["api"]}),
        "loopback bind names the failing checks"
    );

    let head = probe("/healthz", Method::HEAD).await;
    assert_eq!(head.status(), 200);
    assert_eq!(head.headers()["content-type"], "application/json");
    assert!(text(head).await.is_empty());

    let post = probe("/healthz", Method::POST).await;
    assert_eq!(post.status(), 405);
    assert_eq!(post.headers()["allow"], "GET, HEAD");
    assert_eq!(json_of(post).await["code"], "method_not_allowed");

    // Everything else is still guarded: by the Host check here, and by
    // authentication for a Host this listener serves.
    assert_eq!(probe("/hello", Method::GET).await.status(), 403);
    assert_eq!(a.oneshot(get_req("/hello")).await.unwrap().status(), 401);
}

// --- host and origin ------------------------------------------------------

#[tokio::test]
async fn host_and_origin_checks_are_on_by_default() {
    let a = app(cfg(hello()));
    let wrong_host = a
        .clone()
        .oneshot(
            Request::builder()
                .uri("/hello")
                .header("host", "evil.test")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(wrong_host.status(), 403);
    let body = json_of(wrong_host).await;
    assert_eq!(body["code"], "host_rejected");
    assert_eq!(body["message"], "host \"evil.test\" is not served here");
    assert_eq!(body["status"], 403);

    let cross = a
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/echo")
                .header("host", "localhost:8080")
                .header("origin", "https://evil.test")
                .body(Body::from("x"))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(cross.status(), 403);
    assert_eq!(json_of(cross).await["code"], "origin_rejected");

    let mut off = cfg(hello());
    off.host_check = false;
    off.origin_check = false;
    let resp = app(off)
        .oneshot(
            Request::builder()
                .uri("/hello")
                .header("host", "evil.test")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), 200);
}

#[tokio::test]
async fn a_wildcard_bind_without_allowed_hosts_checks_no_host() {
    let mut c = cfg(hello());
    c.addr = "0.0.0.0:0".into();
    let ok = app(c)
        .oneshot(
            Request::builder()
                .uri("/hello")
                .header("host", "anything.test")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(ok.status(), 200);

    let mut c = cfg(hello());
    c.addr = "0.0.0.0:0".into();
    c.allow_hosts = vec!["api.test".into()];
    let a = app(c);
    let bad = a
        .clone()
        .oneshot(
            Request::builder()
                .uri("/hello")
                .header("host", "anything.test")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(bad.status(), 403);
    let good = a
        .oneshot(
            Request::builder()
                .uri("/hello")
                .header("host", "api.test:8080")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(good.status(), 200);
}

// --- body limit -----------------------------------------------------------

#[tokio::test]
async fn body_over_the_limit_is_refused_in_json() {
    let mut c = cfg(hello());
    c.max_body_bytes = 8;
    let a = app(c);
    let send = |body: &'static str| {
        let a = a.clone();
        async move {
            a.oneshot(
                Request::builder()
                    .method(Method::POST)
                    .uri("/echo")
                    .header("host", "127.0.0.1")
                    .header(header::CONTENT_LENGTH, body.len())
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap()
        }
    };
    assert_eq!(send("12345678").await.status(), 200);
    let big = send("123456789").await;
    assert_eq!(big.status(), 413);
    let body = json_of(big).await;
    assert_eq!(body["code"], "body_too_large");
    assert_eq!(body["message"], "request body exceeds 8 bytes");

    // Zero is the default (1 MiB); negative is no limit.
    let mut dflt = cfg(hello());
    dflt.max_body_bytes = 0;
    let resp = app(dflt)
        .oneshot(
            Request::builder()
                .method(Method::POST)
                .uri("/echo")
                .header("host", "127.0.0.1")
                .header(header::CONTENT_LENGTH, (1 << 20) + 1)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), 413);
}

// --- auth -----------------------------------------------------------------

fn bearer_auth() -> AuthFunc {
    Arc::new(|c| match c.authorization {
        None => Err(AuthnError::NoToken(None)),
        Some("Bearer good") => Ok(Claims {
            subject: "alice".into(),
            tenant: "acme".into(),
            scopes: vec!["s".into()],
        }),
        Some(_) => Err(AuthnError::InvalidToken("nope".into())),
    })
}

#[tokio::test]
async fn auth_refuses_with_a_challenge_and_passes_claims() {
    let routes = Router::new().route(
        "/me",
        get(
            |claims: Extension<Claims>, _v: Extension<Authenticated>| async move {
                json_response(200, &*claims)
            },
        ),
    );
    let refused = Arc::new(Mutex::new(Vec::<String>::new()));
    let r = refused.clone();
    let mut c = cfg(routes);
    c.auth = Some(bearer_auth());
    c.on_auth_refused = Some(Arc::new(move |e| r.lock().unwrap().push(e.to_string())));
    let a = app(c);

    let none = a.clone().oneshot(get_req("/me")).await.unwrap();
    assert_eq!(none.status(), 401);
    assert_eq!(none.headers()["www-authenticate"], "Bearer");
    let body = json_of(none).await;
    assert_eq!(body["code"], "unauthenticated");
    assert_eq!(body["message"], "authn: no bearer token");

    let bad = a
        .clone()
        .oneshot(
            Request::builder()
                .uri("/me")
                .header("host", "127.0.0.1")
                .header("authorization", "Bearer bad")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(bad.status(), 401);

    let ok = a
        .oneshot(
            Request::builder()
                .uri("/me")
                .header("host", "127.0.0.1")
                .header("authorization", "Bearer good")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(ok.status(), 200);
    assert_eq!(
        json_of(ok).await,
        json!({"sub": "alice", "tenant": "acme", "scopes": ["s"]})
    );
    assert_eq!(
        refused.lock().unwrap().len(),
        2,
        "every refusal is reported"
    );
}

#[tokio::test]
async fn a_custom_challenge_is_used() {
    let mut c = cfg(hello());
    c.auth = Some(bearer_auth());
    c.auth_challenge = Some("Bearer realm=\"kit\"".into());
    let resp = app(c).oneshot(get_req("/hello")).await.unwrap();
    assert_eq!(resp.headers()["www-authenticate"], "Bearer realm=\"kit\"");
}

#[test]
fn non_loopback_without_auth_is_refused_at_validate() {
    let mut c = cfg(hello());
    c.addr = "0.0.0.0:8080".into();
    let svc = ApiService::build(c).unwrap();
    let why = svc.validate().expect("refused");
    assert!(why.contains("not a loopback address"), "{why}");

    for ok in [
        {
            let mut c = cfg(hello());
            c.addr = "0.0.0.0:8080".into();
            c.insecure_remote = true;
            c
        },
        {
            let mut c = cfg(hello());
            c.addr = "0.0.0.0:8080".into();
            c.auth = Some(bearer_auth());
            c
        },
        {
            let mut c = cfg(hello());
            c.addr = "[::1]:8080".into();
            c
        },
    ] {
        assert!(ApiService::build(ok).unwrap().validate().is_none());
    }
}

// --- rate limit -----------------------------------------------------------

struct Fake(Mutex<SystemTime>);

impl Clock for Fake {
    fn now(&self) -> SystemTime {
        *self.0.lock().unwrap()
    }
}

#[tokio::test]
async fn rate_limit_is_per_client() {
    let clock = Arc::new(Fake(Mutex::new(
        SystemTime::UNIX_EPOCH + Duration::from_secs(1000),
    )));
    let mut c = cfg(hello());
    let mut rl = RateLimit::new(2, Duration::from_secs(30));
    rl.clock = Some(clock.clone());
    c.rate_limit = Some(rl);
    let a = app(c);
    let from = |ip: &str| {
        let a = a.clone();
        let sa: SocketAddr = format!("{ip}:1000").parse().unwrap();
        async move {
            let mut r = get_req("/hello");
            r.extensions_mut().insert(ConnectInfo(sa));
            a.oneshot(r).await.unwrap()
        }
    };
    assert_eq!(from("192.0.2.1").await.status(), 200);
    assert_eq!(from("192.0.2.1").await.status(), 200);
    let limited = from("192.0.2.1").await;
    assert_eq!(limited.status(), 429);
    assert_eq!(limited.headers()["retry-after"], "30");
    assert_eq!(json_of(limited).await["code"], "rate_limited");
    assert_eq!(
        from("192.0.2.2").await.status(),
        200,
        "another client has its own bucket"
    );

    *clock.0.lock().unwrap() += Duration::from_secs(31);
    assert_eq!(from("192.0.2.1").await.status(), 200, "the window passed");
}

// --- recovery, access log, redaction --------------------------------------

#[tokio::test]
async fn a_panic_is_a_500_not_a_dropped_connection() {
    let lines = Arc::new(Mutex::new(Vec::<String>::new()));
    let l = lines.clone();
    let routes = Router::new().route(
        "/boom",
        get(|| async {
            if true {
                panic!("kaboom")
            };
            "unreachable"
        }),
    );
    let mut c = cfg(routes);
    c.log = Some(Arc::new(move |line| {
        l.lock().unwrap().push(line.to_string())
    }));
    let resp = app(c).oneshot(get_req("/boom")).await.unwrap();
    assert_eq!(resp.status(), 500);
    assert_eq!(resp.headers()["content-type"], "text/plain; charset=utf-8");
    assert_eq!(text(resp).await, "Internal Server Error\n");
    let lines = lines.lock().unwrap();
    assert!(lines
        .iter()
        .any(|l| l.contains("panic recovered") && l.contains("kaboom")));
    assert!(lines.iter().any(|l| l.contains("status=500")), "{lines:?}");
}

#[tokio::test]
async fn the_access_log_runs_through_redact() {
    let lines = Arc::new(Mutex::new(Vec::<String>::new()));
    let l = lines.clone();
    let mut c = cfg(Router::new().route("/{*rest}", any(|| async { "ok" })));
    c.log = Some(Arc::new(move |line| {
        l.lock().unwrap().push(line.to_string())
    }));
    let a = app(c);

    // A GitHub-token-shaped secret in the path, built at runtime.
    let secret = format!("{}{}", "ghp_", "a1B2c3D4e5F6g7H8i9J0k1L2m3N4o5P6q7R8");
    let resp = a.oneshot(get_req(&format!("/x/{secret}"))).await.unwrap();
    assert_eq!(resp.status(), 200);
    let lines = lines.lock().unwrap();
    assert_eq!(lines.len(), 1);
    let line = &lines[0];
    assert!(line.contains("msg=\"http request\""), "{line}");
    assert!(line.contains("method=GET"), "{line}");
    assert!(line.contains("status=200"), "{line}");
    assert!(line.contains("request_id="), "{line}");
    assert!(
        !line.contains(&secret),
        "the secret reached the log: {line}"
    );
}

// --- events ---------------------------------------------------------------

#[derive(Default)]
struct Rec(Mutex<Vec<(String, String, Value)>>);

impl EventPublisher for Rec {
    fn publish(&self, topic: &str, source: &str, payload: Value) {
        self.0
            .lock()
            .unwrap()
            .push((topic.into(), source.into(), payload));
    }
}

#[tokio::test]
async fn one_event_pair_per_matched_request() {
    let rec = Arc::new(Rec::default());
    let mut c = cfg(hello());
    c.publisher = Some(rec.clone());
    let a = app(c);
    assert_eq!(
        a.clone().oneshot(get_req("/hello")).await.unwrap().status(),
        200
    );
    assert_eq!(a.oneshot(get_req("/nope")).await.unwrap().status(), 404);

    let ev = rec.0.lock().unwrap();
    assert_eq!(ev.len(), 2, "an unmatched path publishes nothing");
    assert_eq!(ev[0].0, "kit.api.request.started");
    assert_eq!(ev[0].1, "api.router");
    assert_eq!(ev[0].2, json!({"method": "GET", "path": "/hello"}));
    assert_eq!(ev[1].0, "kit.api.request.ended");
    assert_eq!(ev[1].2["status"], 200);
    assert!(ev[1].2["duration"].as_str().unwrap().ends_with("ms"));
}

#[tokio::test]
async fn the_topic_prefix_is_configurable_and_validated() {
    let rec = Arc::new(Rec::default());
    let mut c = cfg(hello());
    c.publisher = Some(rec.clone());
    c.topics = Topics::with_prefix("acme.web.request").unwrap();
    app(c).oneshot(get_req("/hello")).await.unwrap();
    let ev = rec.0.lock().unwrap();
    assert_eq!(ev[0].0, "acme.web.request.started");
    assert_eq!(ev[1].0, "acme.web.request.ended");

    assert!(Topics::with_prefix("two.parts").is_err());
    assert!(Topics::with_prefix("").is_err());
    assert!(
        Topics::new("a.b.c.started", "a.b.c.finish").is_err(),
        "not past tense"
    );
    assert!(Topics::new("a.b.c.started", "a.b.c.ended").is_ok());
}

// --- CORS, compression, OpenAPI, SSE -------------------------------------

#[tokio::test]
async fn cors_preflight_and_validation() {
    let mut c = cfg(hello());
    c.cors = Some(CorsConfig {
        allow_origins: vec!["https://app.test".into()],
        allow_methods: vec!["GET".into(), "POST".into()],
        max_age: 600,
        ..CorsConfig::default()
    });
    let a = app(c);
    let pre = a
        .oneshot(
            Request::builder()
                .method(Method::OPTIONS)
                .uri("/echo")
                .header("host", "127.0.0.1")
                .header("origin", "https://app.test")
                .header("access-control-request-method", "POST")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(
        pre.headers()["access-control-allow-origin"],
        "https://app.test"
    );
    assert_eq!(pre.headers()["access-control-max-age"], "600");

    let bad = CorsConfig {
        allow_origins: vec!["*".into(), "https://a.test".into()],
        allow_credentials: true,
        ..CorsConfig::default()
    };
    let err = bad.validate().unwrap_err();
    assert!(err.contains("list it alone"), "{err}");
    assert!(err.contains("credentials cannot be allowed"), "{err}");
    let mut c = cfg(hello());
    c.cors = Some(bad);
    assert!(ApiService::build(c).is_err());
}

#[tokio::test]
async fn gzip_applies_to_large_text_only() {
    let big = "x".repeat(4096);
    let routes = Router::new()
        .route(
            "/big",
            get({
                let big = big.clone();
                move || {
                    let big = big.clone();
                    async move { ([("content-type", "application/json")], big) }
                }
            }),
        )
        .route(
            "/small",
            get(|| async { ([("content-type", "application/json")], "{}") }),
        )
        .route(
            "/blob",
            get(|| async {
                (
                    [("content-type", "application/octet-stream")],
                    "x".repeat(4096),
                )
            }),
        );
    let a = app(cfg(routes));
    let ask = |path: &'static str| {
        let a = a.clone();
        async move {
            a.oneshot(
                Request::builder()
                    .uri(path)
                    .header("host", "127.0.0.1")
                    .header("accept-encoding", "gzip, br")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap()
        }
    };
    let big_resp = ask("/big").await;
    assert_eq!(big_resp.headers()["content-encoding"], "gzip");
    let raw = axum::body::to_bytes(big_resp.into_body(), usize::MAX)
        .await
        .unwrap();
    assert_eq!(&raw[..2], &[0x1f, 0x8b], "gzip magic");
    assert!(
        raw.len() < big.len() / 4,
        "a run of one byte compresses: {}",
        raw.len()
    );

    assert!(ask("/small")
        .await
        .headers()
        .get("content-encoding")
        .is_none());
    assert!(ask("/blob")
        .await
        .headers()
        .get("content-encoding")
        .is_none());

    let mut off = cfg(Router::new().route(
        "/big",
        get(|| async { ([("content-type", "text/plain")], "x".repeat(4096)) }),
    ));
    off.compress = false;
    let resp = app(off)
        .oneshot(
            Request::builder()
                .uri("/big")
                .header("host", "127.0.0.1")
                .header("accept-encoding", "gzip")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert!(resp.headers().get("content-encoding").is_none());
}

#[tokio::test]
async fn openapi_is_served_from_the_registered_document() {
    let mut doc = OpenApi::new("Widgets", "1.0.0");
    doc.operation(Operation {
        operation_id: "ping".into(),
        method: "GET".into(),
        path: "/ping".into(),
        summary: "Ping".into(),
        ..Operation::default()
    });
    let mut c = cfg(hello());
    c.openapi = Some(doc.clone());
    let resp = app(c).oneshot(get_req("/openapi.json")).await.unwrap();
    assert_eq!(resp.status(), 200);
    assert_eq!(resp.headers()["content-type"], "application/json");
    assert_eq!(json_of(resp).await, doc.to_value());

    assert_eq!(
        app(cfg(hello()))
            .oneshot(get_req("/openapi.json"))
            .await
            .unwrap()
            .status(),
        404,
        "no document, no route"
    );
}

#[tokio::test]
async fn sse_streams_with_the_right_headers_and_is_not_compressed() {
    let routes = Router::new().route(
        "/events",
        get(|| async {
            let s = futures_util::stream::iter([
                Ok::<_, std::convert::Infallible>(
                    axum::response::sse::Event::default()
                        .event("tick")
                        .data("1"),
                ),
                Ok(axum::response::sse::Event::default().data("2")),
            ]);
            sse(s)
        }),
    );
    let resp = app(cfg(routes))
        .oneshot(
            Request::builder()
                .uri("/events")
                .header("host", "127.0.0.1")
                .header("accept", "text/event-stream")
                .header("accept-encoding", "gzip")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.headers()["content-type"], "text/event-stream");
    assert_eq!(resp.headers()["cache-control"], "no-store");
    assert_eq!(resp.headers()["x-accel-buffering"], "no");
    assert!(resp.headers().get("content-encoding").is_none());
    let body = text(resp).await;
    assert!(body.contains("event: tick\ndata: 1\n\n"), "{body}");
    assert!(body.contains("data: 2\n\n"), "{body}");
}

// --- the listener as a serve::Service -------------------------------------

async fn http_get(addr: &str, path: &str) -> (u16, String) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let mut s = tokio::net::TcpStream::connect(addr).await.unwrap();
    s.write_all(
        format!("GET {path} HTTP/1.1\r\nhost: {addr}\r\nconnection: close\r\n\r\n").as_bytes(),
    )
    .await
    .unwrap();
    let mut raw = String::new();
    s.read_to_string(&mut raw).await.unwrap();
    let status = raw[9..12].parse().unwrap();
    let body = raw
        .split_once("\r\n\r\n")
        .map(|x| x.1)
        .unwrap_or("")
        .to_string();
    (status, body)
}

fn supervisor(svc: &Arc<ApiService>) -> (Supervisor, Vec<String>, ServiceConfigs) {
    let mut reg = ServiceRegistry::new();
    reg.register(svc.clone()).expect("register");
    let mut configs = ServiceConfigs::new();
    configs.insert("api".to_string(), ServiceConfig::enabled());
    (
        Supervisor::new(reg, SupervisorOptions::default()),
        vec!["api".to_string()],
        configs,
    )
}

async fn until(what: &str, f: impl Fn() -> bool) {
    for _ in 0..400 {
        if f() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    panic!("timed out waiting for {what}");
}

#[tokio::test]
async fn the_service_binds_reports_ready_serves_and_stops() {
    let svc = Arc::new(ApiService::build(cfg(hello())).unwrap());
    assert_eq!(svc.name(), "api");
    assert!(!svc.ready());
    assert!(svc.addr().is_none(), "no address before bind");
    assert_eq!(svc.class(), Some(("write-shared".into(), "listen".into())));

    let (sup, selected, configs) = supervisor(&svc);
    let cancel = CancelToken::new();
    let c2 = cancel.clone();
    let run = tokio::spawn(async move { sup.run(c2, &selected, &configs).await });
    let s2 = svc.clone();
    until("ready", move || s2.ready()).await;

    let addr = svc.addr().expect("bound");
    assert!(
        addr.starts_with("127.0.0.1:") && !addr.ends_with(":0"),
        "{addr}"
    );
    let (status, body) = http_get(&addr, "/hello").await;
    assert_eq!(status, 200);
    assert!(body.contains("hi"), "{body}");
    assert_eq!(http_get(&addr, "/readyz").await.0, 200);

    cancel.cancel();
    let res = run.await.unwrap();
    assert_eq!(res.exit_code, 0, "{:?}", res.error);
    assert_eq!(res.ready, ["api"]);
    assert!(!svc.ready());
    assert!(
        tokio::net::TcpStream::connect(&addr).await.is_err(),
        "the listener is closed"
    );
}

#[tokio::test]
async fn readyz_follows_the_listener_and_its_dependencies() {
    let dep = Arc::new(AtomicBool::new(false));
    let d = dep.clone();
    let mut c = cfg(hello());
    c.health_detail = Some(false);
    c.readiness = vec![ReadinessCheck::new("db", move || d.load(Ordering::SeqCst))];
    let svc = Arc::new(ApiService::build(c).unwrap());
    let (sup, selected, configs) = supervisor(&svc);
    let cancel = CancelToken::new();
    let c2 = cancel.clone();
    let run = tokio::spawn(async move { sup.run(c2, &selected, &configs).await });
    let s2 = svc.clone();
    until("ready", move || s2.ready()).await;

    let probe = svc.router().oneshot(get_req("/readyz")).await.unwrap();
    assert_eq!(probe.status(), 503);
    assert_eq!(
        json_of(probe).await,
        json!({"status": "unavailable"}),
        "no detail"
    );
    dep.store(true, Ordering::SeqCst);
    assert_eq!(
        svc.router()
            .oneshot(get_req("/readyz"))
            .await
            .unwrap()
            .status(),
        200
    );

    cancel.cancel();
    run.await.unwrap();
    assert_eq!(
        svc.router()
            .oneshot(get_req("/readyz"))
            .await
            .unwrap()
            .status(),
        503,
        "not ready once stopped"
    );
}

#[tokio::test]
async fn a_bind_failure_is_a_start_failure_before_ready() {
    let taken = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let mut c = cfg(hello());
    c.addr = taken.local_addr().unwrap().to_string();
    let svc = Arc::new(ApiService::build(c).unwrap());
    let (sup, selected, configs) = supervisor(&svc);
    let res = sup.run(CancelToken::new(), &selected, &configs).await;
    assert_ne!(res.exit_code, 0);
    assert!(res.ready.is_empty(), "ready is never reported");
    assert!(res.failed["api"].starts_with("listen:"), "{:?}", res.failed);
    assert!(!svc.ready());
}

#[test]
fn a_bad_trusted_proxy_or_origin_fails_the_build() {
    let mut c = cfg(hello());
    c.trusted_proxies = vec!["proxy.internal".into()];
    let err = ApiService::build(c).err().expect("refused");
    assert!(err.to_string().contains("proxy.internal"), "{err}");
    let _ = StatusCode::OK.into_response();
}

struct Rig {
    app: Router,
    clock: Arc<Fake>,
    sweeps: Arc<std::sync::atomic::AtomicUsize>,
}

impl Rig {
    fn new(max: u32, cap: usize) -> Self {
        Self::with_quota(max, cap, 0)
    }
    fn with_quota(max: u32, cap: usize, quota: usize) -> Self {
        let clock = Arc::new(Fake(Mutex::new(
            SystemTime::UNIX_EPOCH + Duration::from_secs(1000),
        )));
        let sweeps = Arc::new(std::sync::atomic::AtomicUsize::new(0));
        let s = sweeps.clone();
        let mut rl = RateLimit::new(max, Duration::from_secs(40));
        rl.max_clients = cap;
        rl.per_prefix = quota;
        rl.clock = Some(clock.clone());
        rl.on_sweep = Some(Arc::new(move || {
            s.fetch_add(1, Ordering::SeqCst);
        }));
        let mut c = cfg(hello());
        c.rate_limit = Some(rl);
        Rig {
            app: app(c),
            clock,
            sweeps,
        }
    }
    fn advance(&self, secs: u64) {
        *self.clock.0.lock().unwrap() += Duration::from_secs(secs);
    }
    async fn unknown_client(&self) -> u16 {
        let r = get_req("/hello");
        self.app.clone().oneshot(r).await.unwrap().status().as_u16()
    }
    async fn from(&self, ip: &str) -> u16 {
        let sa: SocketAddr = if ip.contains(':') {
            format!("[{ip}]:1")
        } else {
            format!("{ip}:1")
        }
        .parse()
        .unwrap();
        let mut r = get_req("/hello");
        r.extensions_mut().insert(ConnectInfo(sa));
        self.app.clone().oneshot(r).await.unwrap().status().as_u16()
    }
}

#[tokio::test]
async fn rotating_keys_past_the_cap_cannot_beat_the_prefix_budget() {
    let r = Rig::new(1, 8); // 8 exact clients, 2 aggregate buckets
    for i in 1..=8 {
        assert_eq!(r.from(&format!("198.51.100.{i}")).await, 200);
    }
    // Table full, nothing idle: 50 new keys in one /24 share one budget.
    let mut ok = 0;
    for i in 1..=50 {
        if r.from(&format!("192.0.2.{i}")).await == 200 {
            ok += 1;
        }
    }
    assert_eq!(ok, 1, "the whole /24 gets one budget");
    // A second /24 gets the second aggregate bucket; a third shares overflow.
    assert_eq!(r.from("203.0.113.1").await, 200);
    assert_eq!(r.from("203.0.113.2").await, 429);
    assert_eq!(r.from("100.64.0.1").await, 200, "the shared last resort");
    assert_eq!(r.from("100.65.0.1").await, 429);
}

#[tokio::test]
async fn new_keys_never_reset_an_active_clients_budget() {
    let r = Rig::new(1, 4);
    assert_eq!(r.from("198.51.100.1").await, 200);
    assert_eq!(r.from("198.51.100.1").await, 429);
    for i in 2..=4 {
        r.from(&format!("198.51.100.{i}")).await;
    }
    for i in 1..=100 {
        r.from(&format!("10.{}.0.1", i)).await;
    }
    assert_eq!(r.from("198.51.100.1").await, 429, "still limited");
}

#[tokio::test]
async fn idle_buckets_are_reclaimed_and_the_sweep_is_rate_limited() {
    let r = Rig::new(1, 2);
    for i in 0..30 {
        r.from(&format!("198.51.100.{}", i % 2 + 1)).await;
    }
    assert_eq!(
        r.sweeps.load(Ordering::SeqCst),
        1,
        "one sweep for a burst of requests"
    );
    r.advance(9); // past per/4 = 10s? no: 9 < 10
    r.from("198.51.100.1").await;
    assert_eq!(r.sweeps.load(Ordering::SeqCst), 1);
    r.advance(1);
    r.from("198.51.100.1").await;
    assert_eq!(r.sweeps.load(Ordering::SeqCst), 2, "the interval passed");

    // Reclaim: .2 goes idle for a full period, so a newcomer takes an
    // exact slot instead of an aggregate one.
    let r = Rig::new(1, 2);
    assert_eq!(r.from("198.51.100.1").await, 200);
    assert_eq!(r.from("198.51.100.2").await, 200);
    r.advance(41);
    assert_eq!(r.from("192.0.2.5").await, 200);
    assert_eq!(
        r.from("192.0.2.6").await,
        200,
        "own exact bucket, not a shared /24"
    );
    // Both slots are live now; the next newcomer is aggregated.
    assert_eq!(r.from("192.0.2.7").await, 200);
    assert_eq!(r.from("192.0.2.8").await, 429);
}

#[tokio::test]
async fn ipv6_shares_a_64() {
    let r = Rig::new(1, 100);
    assert_eq!(r.from("2001:db8:1:1::1").await, 200);
    assert_eq!(
        r.from("2001:db8:1:1:ffff::2").await,
        429,
        "same /64, same bucket"
    );
    assert_eq!(r.from("2001:db8:1:2::1").await, 200);
}

#[tokio::test]
async fn an_untidy_request_id_is_replaced_and_logs_stay_one_line() {
    let lines = Arc::new(Mutex::new(Vec::<String>::new()));
    let l = lines.clone();
    let mut c = cfg(hello());
    c.log = Some(Arc::new(move |line| {
        l.lock().unwrap().push(line.to_string())
    }));
    let a = app(c);
    for bad in ["a b", "x\"y", &"z".repeat(129), "é"] {
        let resp = a
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/hello")
                    .header("host", "127.0.0.1")
                    .header(
                        "x-request-id",
                        axum::http::HeaderValue::from_bytes(bad.as_bytes()).unwrap(),
                    )
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        let id = resp.headers()["x-request-id"].to_str().unwrap().to_string();
        assert_eq!(id.len(), 32, "{bad:?} was kept: {id}");
    }
    let lines = lines.lock().unwrap();
    assert_eq!(lines.len(), 4);
    assert!(lines
        .iter()
        .all(|l| !l.contains('\n') && l.contains("request_id=\"")));
}

#[tokio::test]
async fn one_prefix_cannot_take_every_slot() {
    let r = Rig::with_quota(1, 8, 4);
    for i in 0..20 {
        r.from(&format!("2001:db8:1:{i:x}::1")).await;
    }
    // The flooding /48 held at its quota; other prefixes still get
    // individual buckets.
    assert_eq!(r.from("192.0.2.1").await, 200);
    assert_eq!(r.from("192.0.2.2").await, 200, "exact, not the shared /24");
    assert_eq!(r.from("192.0.2.1").await, 429);
}

#[tokio::test]
async fn unknown_clients_have_their_own_bucket() {
    let r = Rig::with_quota(1, 4, 1);
    for ip in ["10.0.0.1", "10.1.0.1", "10.2.0.1", "10.3.0.1"] {
        assert_eq!(r.from(ip).await, 200); // exact slots
    }
    assert_eq!(r.from("10.4.0.1").await, 200); // aggregate 1
    assert_eq!(r.from("10.5.0.1").await, 200); // aggregate 2
    assert_eq!(r.from("10.6.0.1").await, 200); // overflow
    assert_eq!(r.from("10.7.0.1").await, 429, "overflow is shared");
    assert_eq!(r.unknown_client().await, 200, "unknown has its own");
    assert_eq!(r.unknown_client().await, 429);
}
