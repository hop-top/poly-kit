//! Server timeouts and the metrics endpoint, over a real listener and
//! through the assembled router. Go is the authority:
//! `go/transport/api` `server_timeouts.go` and `metrics.go`.
#![cfg(feature = "api-server")]

mod api_server_support;

use std::time::{Duration, Instant};

use axum::body::Body;
use axum::extract::Request;
use axum::http::{header, Method, StatusCode};
use axum::middleware::from_fn;
use axum::routing::{get, post};
use axum::Router;
use hop_top_kit::api_server::{
    lift_write_deadline, ApiServerConfig, ApiService, MetricsConfig, ServerTimeouts,
    DEFAULT_METRICS_PATH, DEFAULT_READ_HEADER_TIMEOUT, DEFAULT_READ_TIMEOUT, DEFAULT_WRITE_TIMEOUT,
};
use hop_top_kit::authn::{AuthFunc, AuthnError, Claims};
use serde_json::{json, Value};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tower::ServiceExt;

use api_server_support::{config, start};

fn ms(n: u64) -> Duration {
    Duration::from_millis(n)
}

/// Reads until EOF, bounded; the connection ending is the answer.
async fn read_all(s: &mut TcpStream, limit: Duration) -> (String, bool) {
    let mut buf = Vec::new();
    let eof = tokio::time::timeout(limit, async {
        let mut chunk = [0u8; 4096];
        loop {
            match s.read(&mut chunk).await {
                Ok(0) | Err(_) => return,
                Ok(n) => buf.extend_from_slice(&chunk[..n]),
            }
        }
    })
    .await
    .is_ok();
    (String::from_utf8_lossy(&buf).into_owned(), eof)
}

// --- timeouts ---------------------------------------------------------

#[test]
fn defaults_are_the_corpus_values() {
    let c = corpus();
    let t = ServerTimeouts::default();
    assert_eq!(c["constants"]["timeouts"]["read_header_ms"], 5000);
    assert_eq!(
        t.read_header.as_millis() as u64,
        c["constants"]["timeouts"]["read_header_ms"]
    );
    assert_eq!(
        t.read.as_millis() as u64,
        c["constants"]["timeouts"]["read_ms"]
    );
    assert_eq!(
        t.write.as_millis() as u64,
        c["constants"]["timeouts"]["write_ms"]
    );
    assert_eq!(DEFAULT_READ_HEADER_TIMEOUT, t.read_header);
    assert_eq!(DEFAULT_READ_TIMEOUT, t.read);
    assert_eq!(DEFAULT_WRITE_TIMEOUT, t.write);
}

fn corpus() -> Value {
    let path = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../contracts/parity/api.json");
    serde_json::from_slice(&std::fs::read(path).expect("read api.json")).expect("JSON")
}

#[tokio::test]
async fn a_slow_header_is_cut_at_read_header() {
    let mut cfg = config(Router::new().route("/", get(|| async { "ok" })));
    cfg.timeouts = ServerTimeouts {
        read_header: ms(300),
        read: Duration::ZERO,
        write: Duration::ZERO,
    };
    let run = start(cfg).await;
    let mut s = TcpStream::connect(&run.addr).await.unwrap();
    s.write_all(b"GET / HTTP/1.1\r\nhost: 127.0.0.1\r\n")
        .await
        .unwrap(); // never ends
    let t = Instant::now();
    let (_, eof) = read_all(&mut s, Duration::from_secs(3)).await;
    assert!(eof, "the connection is closed");
    assert!(t.elapsed() < Duration::from_secs(2), "{:?}", t.elapsed());
    run.stop().await;
}

#[tokio::test]
async fn a_zero_read_header_falls_back_to_read() {
    let mut cfg = config(Router::new().route("/", get(|| async { "ok" })));
    cfg.timeouts = ServerTimeouts {
        read_header: Duration::ZERO,
        read: ms(300),
        write: Duration::ZERO,
    };
    let run = start(cfg).await;
    let mut s = TcpStream::connect(&run.addr).await.unwrap();
    s.write_all(b"GET / HTTP/1.1\r\nhost: 127.0.0.1\r\n")
        .await
        .unwrap();
    let t = Instant::now();
    let (_, eof) = read_all(&mut s, Duration::from_secs(3)).await;
    assert!(eof && t.elapsed() < Duration::from_secs(2));
    run.stop().await;
}

/// A body that trickles: each chunk arrives inside any per-chunk idle
/// bound, but the whole request overruns `read`.
#[tokio::test]
async fn a_trickled_body_is_cut_at_the_total_read_deadline() {
    let app = Router::new().route(
        "/up",
        post(|body: Body| async move {
            match axum::body::to_bytes(body, usize::MAX).await {
                Ok(b) => (StatusCode::OK, format!("{}", b.len())),
                Err(_) => (StatusCode::REQUEST_TIMEOUT, "read deadline".to_string()),
            }
        }),
    );
    let mut cfg = config(app);
    cfg.timeouts = ServerTimeouts {
        read_header: Duration::from_secs(5),
        read: ms(400),
        write: Duration::ZERO,
    };
    let run = start(cfg).await;
    let mut s = TcpStream::connect(&run.addr).await.unwrap();
    s.write_all(b"POST /up HTTP/1.1\r\nhost: 127.0.0.1\r\ncontent-length: 20\r\n\r\n")
        .await
        .unwrap();
    let t = Instant::now();
    for _ in 0..20 {
        if s.write_all(b"x").await.is_err() {
            break;
        }
        tokio::time::sleep(ms(100)).await; // 100ms apart: 2s for 20
        if t.elapsed() > ms(1500) {
            break;
        }
    }
    let (raw, _) = read_all(&mut s, Duration::from_secs(2)).await;
    assert!(raw.starts_with("HTTP/1.1 408"), "{raw}");
    assert!(
        t.elapsed() < Duration::from_millis(1800),
        "{:?}",
        t.elapsed()
    );
    run.stop().await;
}

#[tokio::test]
async fn a_handler_past_write_is_cut_without_a_response() {
    let app = Router::new().route(
        "/slow",
        get(|| async {
            tokio::time::sleep(Duration::from_secs(3)).await;
            "late"
        }),
    );
    let mut cfg = config(app);
    cfg.timeouts = ServerTimeouts {
        read_header: Duration::from_secs(5),
        read: Duration::from_secs(5),
        write: ms(300),
    };
    let run = start(cfg).await;
    let mut s = TcpStream::connect(&run.addr).await.unwrap();
    s.write_all(b"GET /slow HTTP/1.1\r\nhost: 127.0.0.1\r\n\r\n")
        .await
        .unwrap();
    let t = Instant::now();
    let (raw, eof) = read_all(&mut s, Duration::from_secs(2)).await;
    assert!(eof, "closed");
    assert!(!raw.contains("late"), "{raw}");
    assert!(t.elapsed() < Duration::from_secs(2));
    run.stop().await;
}

fn stream_route() -> Router {
    Router::new().route(
        "/stream",
        get(|| async {
            let s = futures_util::stream::unfold(0u8, |i| async move {
                if i == 6 {
                    return None;
                }
                tokio::time::sleep(ms(150)).await;
                Some((Ok::<_, std::io::Error>(format!("c{i};")), i + 1))
            });
            Body::from_stream(s)
        }),
    )
}

#[tokio::test]
async fn a_streamed_response_past_write_is_cut_unless_lifted() {
    let t = ServerTimeouts {
        read_header: Duration::from_secs(5),
        read: Duration::from_secs(5),
        write: ms(400),
    };
    for (lifted, want_all) in [(false, false), (true, true)] {
        let app = if lifted {
            stream_route().route_layer(from_fn(lift_write_deadline))
        } else {
            stream_route()
        };
        let mut cfg = config(app);
        cfg.compress = false;
        cfg.timeouts = t;
        let run = start(cfg).await;
        let mut s = TcpStream::connect(&run.addr).await.unwrap();
        s.write_all(b"GET /stream HTTP/1.1\r\nhost: 127.0.0.1\r\nconnection: close\r\n\r\n")
            .await
            .unwrap();
        let (raw, _) = read_all(&mut s, Duration::from_secs(4)).await;
        let all = raw.contains("c5;");
        assert_eq!(all, want_all, "lifted={lifted}: {raw}");
        if !lifted {
            assert!(raw.contains("c0;"), "the early chunks went out: {raw:?}");
        }
        run.stop().await;
    }
}

#[tokio::test]
async fn a_sized_response_keeps_its_content_length() {
    let app = Router::new().route("/n", get(|| async { "twelve bytes" }));
    let run = start(config(app)).await;
    let mut s = TcpStream::connect(&run.addr).await.unwrap();
    s.write_all(b"GET /n HTTP/1.1\r\nhost: 127.0.0.1\r\nconnection: close\r\n\r\n")
        .await
        .unwrap();
    let (raw, _) = read_all(&mut s, Duration::from_secs(2)).await;
    let lower = raw.to_lowercase();
    assert!(lower.contains("content-length: 12"), "{raw}");
    assert!(!lower.contains("transfer-encoding"), "{raw}");
    run.stop().await;
}

// --- metrics ----------------------------------------------------------

fn exposition() -> axum::routing::MethodRouter {
    get(|| async { "# TYPE up gauge\nup 1\n" })
}

fn req(method: Method, path: &str) -> Request {
    Request::builder()
        .method(method)
        .uri(path)
        .header("host", "127.0.0.1:8080")
        .body(Body::empty())
        .unwrap()
}

fn metrics_app(mut cfg: ApiServerConfig) -> Router {
    cfg.log = Some(std::sync::Arc::new(|_| {}));
    cfg.metrics = Some(MetricsConfig::new(exposition()));
    ApiService::build(cfg).expect("build").router()
}

fn plain() -> Router {
    Router::new().route("/hello", get(|| async { "hi" }))
}

#[tokio::test]
async fn metrics_answers_get_and_head_at_the_default_path_uncached() {
    assert_eq!(
        DEFAULT_METRICS_PATH,
        corpus()["constants"]["metrics"]["default_path"]
    );
    let app = metrics_app(ApiServerConfig::new("127.0.0.1:8080", plain()));
    let r = app
        .clone()
        .oneshot(req(Method::GET, "/metrics"))
        .await
        .unwrap();
    assert_eq!(r.status(), 200);
    assert_eq!(r.headers()[header::CACHE_CONTROL], "no-store");
    let body = axum::body::to_bytes(r.into_body(), usize::MAX)
        .await
        .unwrap();
    assert_eq!(&body[..], b"# TYPE up gauge\nup 1\n");
    let h = app
        .clone()
        .oneshot(req(Method::HEAD, "/metrics"))
        .await
        .unwrap();
    assert_eq!(h.status(), 200);
    assert_eq!(h.headers()[header::CACHE_CONTROL], "no-store");
    // Another path is the router's.
    let other = app.oneshot(req(Method::GET, "/hello")).await.unwrap();
    assert_eq!(other.status(), 200);
    assert!(other.headers().get(header::CACHE_CONTROL).is_none());
}

#[tokio::test]
async fn metrics_refuses_other_methods_with_the_json_envelope() {
    let app = metrics_app(ApiServerConfig::new("127.0.0.1:8080", plain()));
    let r = app.oneshot(req(Method::POST, "/metrics")).await.unwrap();
    assert_eq!(r.status(), 405);
    assert_eq!(r.headers()[header::ALLOW], "GET, HEAD");
    let v: Value = serde_json::from_slice(
        &axum::body::to_bytes(r.into_body(), usize::MAX)
            .await
            .unwrap(),
    )
    .unwrap();
    assert_eq!(v["code"], "method_not_allowed");
    assert_eq!(v["message"], "the metrics endpoint answers GET and HEAD");
}

#[tokio::test]
async fn a_scrape_needs_no_credentials_and_skips_the_router_guards() {
    let func: AuthFunc = std::sync::Arc::new(|c| match c.authorization {
        Some("Bearer ok") => Ok(Claims {
            subject: "u".into(),
            ..Default::default()
        }),
        _ => Err(AuthnError::NoToken(None)),
    });
    let mut cfg = ApiServerConfig::new("127.0.0.1:8080", plain());
    cfg.auth = Some(func);
    cfg.max_body_bytes = 1;
    let app = metrics_app(cfg);
    assert_eq!(
        app.clone()
            .oneshot(req(Method::GET, "/hello"))
            .await
            .unwrap()
            .status(),
        401,
        "the router is guarded"
    );
    let r = app.oneshot(req(Method::GET, "/metrics")).await.unwrap();
    assert_eq!(r.status(), 200, "the scrape is not");
}

#[tokio::test]
async fn a_scrape_is_inside_the_host_and_origin_checks() {
    let app = metrics_app(ApiServerConfig::new("127.0.0.1:8080", plain()));
    let bad_host = Request::builder()
        .uri("/metrics")
        .header("host", "rebind.evil.test")
        .body(Body::empty())
        .unwrap();
    let r = app.clone().oneshot(bad_host).await.unwrap();
    assert_eq!(r.status(), 403);
    let v: Value = serde_json::from_slice(
        &axum::body::to_bytes(r.into_body(), usize::MAX)
            .await
            .unwrap(),
    )
    .unwrap();
    assert_eq!(v["code"], "host_rejected");
    // Safe methods pass the Origin check; a cross-origin POST is refused
    // by it before the endpoint's own 405.
    let cross = Request::builder()
        .method(Method::POST)
        .uri("/metrics")
        .header("host", "127.0.0.1:8080")
        .header("origin", "https://evil.test")
        .body(Body::empty())
        .unwrap();
    assert_eq!(app.oneshot(cross).await.unwrap().status(), 403);
}

#[tokio::test]
async fn a_scrape_is_inside_the_edge_layers() {
    let app = metrics_app(ApiServerConfig::new("127.0.0.1:8080", plain()));
    let r = app.oneshot(req(Method::GET, "/metrics")).await.unwrap();
    assert!(r.headers().contains_key("x-request-id"));
    assert_eq!(r.headers()["x-content-type-options"], "nosniff");
}

#[test]
fn metrics_beyond_loopback_needs_allow_remote_and_the_path_must_be_clean() {
    let build = |addr: &str, path: &str, allow| {
        let mut cfg = ApiServerConfig::new(addr, plain());
        cfg.insecure_remote = true;
        let mut m = MetricsConfig::new(exposition());
        m.path = path.to_string();
        m.allow_remote = allow;
        cfg.metrics = Some(m);
        ApiService::build(cfg).map(|_| ())
    };
    let e = build("0.0.0.0:8080", "", false).unwrap_err();
    assert!(e.0.contains("not a loopback address"), "{e}");
    assert!(build("0.0.0.0:8080", "", true).is_ok());
    assert!(build("127.0.0.1:8080", "/custom/m", false).is_ok());
    for bad in ["metrics", "/a//b", "/a/../b", "/a/./b", "/m?x=1"] {
        assert!(build("127.0.0.1:8080", bad, false).is_err(), "{bad}");
    }
}

#[tokio::test]
async fn a_custom_path_replaces_the_default() {
    let mut cfg = ApiServerConfig::new("127.0.0.1:8080", plain());
    cfg.log = Some(std::sync::Arc::new(|_| {}));
    let mut m = MetricsConfig::new(exposition());
    m.path = "/_/prom".into();
    cfg.metrics = Some(m);
    let app = ApiService::build(cfg).unwrap().router();
    assert_eq!(
        app.clone()
            .oneshot(req(Method::GET, "/_/prom"))
            .await
            .unwrap()
            .status(),
        200
    );
    assert_eq!(
        app.oneshot(req(Method::GET, "/metrics"))
            .await
            .unwrap()
            .status(),
        404
    );
    let _ = json!(null);
}

#[tokio::test]
async fn metrics_corpus() {
    let c = corpus();
    assert_eq!(
        c["constants"]["metrics"]["not_allowed_message"],
        "the metrics endpoint answers GET and HEAD"
    );
    for case in c["metrics"].as_array().unwrap() {
        let name = case["name"].as_str().unwrap();
        let mut cfg = ApiServerConfig::new(
            "127.0.0.1:8080",
            Router::new().fallback(|| async { "next" }),
        );
        cfg.log = Some(std::sync::Arc::new(|_| {}));
        let mut m = MetricsConfig::new(get(|| async { "exposition" }));
        if let Some(p) = case["config_path"].as_str() {
            m.path = p.to_string();
        }
        cfg.metrics = Some(m);
        let app = ApiService::build(cfg).unwrap().router();
        let method: Method = case["method"].as_str().unwrap().parse().unwrap();
        let r = app
            .oneshot(req(method.clone(), case["path"].as_str().unwrap()))
            .await
            .unwrap();
        let (parts, body) = r.into_parts();
        let body = axum::body::to_bytes(body, usize::MAX).await.unwrap();
        if case["passes_through"].as_bool().unwrap() {
            assert_eq!(&body[..], b"next", "{name}");
            continue;
        }
        assert_eq!(parts.status.as_u16() as u64, case["status"], "{name}");
        assert_eq!(
            parts
                .headers
                .get(header::CACHE_CONTROL)
                .map_or("", |v| v.to_str().unwrap()),
            case["cache_control"].as_str().unwrap_or(""),
            "{name}"
        );
        if parts.status == 405 {
            assert_eq!(
                parts.headers[header::ALLOW],
                case["allow"].as_str().unwrap()
            );
            let v: Value = serde_json::from_slice(&body).unwrap();
            assert_eq!(v["code"], case["code"], "{name}");
            assert_eq!(
                v["message"],
                c["constants"]["metrics"]["not_allowed_message"]
            );
        } else if method == Method::GET {
            assert_eq!(&body[..], b"exposition", "{name}");
        }
    }
}
