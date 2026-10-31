//! The shared REST server corpus in `contracts/parity/api.json`: client
//! address behind proxies, trusted-proxy parsing, Host and Origin
//! checks, listener hosts, the constants and the OpenAPI documents.
//! Go is the authority; a failure means this port drifted.
#![cfg(feature = "api-server")]

use std::collections::BTreeMap;
use std::net::SocketAddr;
use std::path::PathBuf;

use axum::body::Body;
use axum::extract::{ConnectInfo, Extension};
use axum::http::{Method, Request};
use axum::routing::any;
use axum::Router;
use hop_top_kit::api_server::{
    host_permits, json_response, listener_hosts, parse_trusted_proxies, ApiServerConfig,
    ApiService, ClientIp, ForwardedProto, HostCheckConfig, OpenApi, Operation, Param, PeerAddr,
    RequestBody, Response, Server, Topics, CODE_BODY_TOO_LARGE, CODE_HOST_REJECTED,
    CODE_ORIGIN_REJECTED, CODE_RATE_LIMITED, CODE_UNAUTHENTICATED, DEFAULT_AUTH_CHALLENGE,
    DEFAULT_CSP, DEFAULT_HSTS_MAX_AGE, DEFAULT_MAX_BODY_BYTES, EVENT_SOURCE, LIVENESS_PATH,
    READINESS_PATH,
};
use serde_json::{json, Value};
use tower::ServiceExt;

fn corpus() -> Value {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../../contracts/parity/api.json");
    serde_json::from_slice(&std::fs::read(path).expect("read api.json")).expect("api.json is JSON")
}

fn strings(v: &Value) -> Vec<String> {
    v.as_array()
        .expect("array")
        .iter()
        .map(|s| s.as_str().expect("string").to_string())
        .collect()
}

#[test]
fn constants_match_the_corpus() {
    let c = corpus();
    let k = &c["constants"];
    assert_eq!(k["health"]["liveness"], LIVENESS_PATH);
    assert_eq!(k["health"]["readiness"], READINESS_PATH);
    let t = Topics::default();
    assert_eq!(k["topics"]["request.started"], t.request_start);
    assert_eq!(k["topics"]["request.ended"], t.request_end);
    assert_eq!(k["event_source"], EVENT_SOURCE);
    assert_eq!(k["default_max_body_bytes"], DEFAULT_MAX_BODY_BYTES);
    assert_eq!(k["default_csp"], DEFAULT_CSP);
    assert_eq!(
        k["default_hsts_max_age_seconds"],
        DEFAULT_HSTS_MAX_AGE.as_secs()
    );
    assert_eq!(k["default_auth_challenge"], DEFAULT_AUTH_CHALLENGE);
    assert_eq!(k["request_id_header"], "X-Request-ID");
    for (name, code) in [
        ("host_rejected", CODE_HOST_REJECTED),
        ("origin_rejected", CODE_ORIGIN_REJECTED),
        ("body_too_large", CODE_BODY_TOO_LARGE),
        ("unauthenticated", CODE_UNAUTHENTICATED),
        ("rate_limited", CODE_RATE_LIMITED),
    ] {
        assert_eq!(name, code);
        assert!(k["codes"][name].is_u64(), "{name}");
    }
}

#[test]
fn ports_record_names_the_rust_rows() {
    let c = corpus();
    let go = c["ports"]["go"].as_object().unwrap();
    let rs = c["ports"]["rs"].as_object().unwrap();
    for (row, status) in rs {
        if row == "implementation" {
            continue;
        }
        assert!(go.contains_key(row), "rs row {row} has no go row");
        assert!(
            matches!(status.as_str(), Some("SHIPPED" | "PENDING" | "N/A")),
            "{row}"
        );
    }
}

// --- client address -----------------------------------------------------

/// What a handler behind the stack saw of the caller.
async fn echo(
    client: Option<Extension<ClientIp>>,
    peer: Option<Extension<PeerAddr>>,
    proto: Option<Extension<ForwardedProto>>,
) -> axum::response::Response {
    json_response(
        200,
        &json!({
            "client": client.map(|c| c.0 .0.to_string()),
            "peer": peer.map(|p| p.0 .0.to_string()),
            "proto": proto.map(|p| p.0 .0),
        }),
    )
}

fn isolated(trusted: Vec<String>) -> Router {
    let mut cfg = ApiServerConfig::new("0.0.0.0:0", Router::new().route("/", any(echo)));
    cfg.host_check = false;
    cfg.origin_check = false;
    cfg.security_headers = false;
    cfg.compress = false;
    cfg.trusted_proxies = trusted;
    cfg.log = Some(std::sync::Arc::new(|_| {}));
    ApiService::build(cfg).expect("build").router()
}

async fn seen(app: Router, peer: &str, headers: &Value) -> Value {
    let mut req = Request::builder().uri("/");
    for (k, vs) in headers.as_object().unwrap() {
        for v in vs.as_array().unwrap() {
            req = req.header(k, v.as_str().unwrap());
        }
    }
    let mut req = req.body(Body::empty()).unwrap();
    // Go spells the peer as host:port with brackets for IPv6; so does
    // SocketAddr.
    let sa: SocketAddr = peer.parse().expect("peer");
    req.extensions_mut().insert(ConnectInfo(sa));
    let resp = app.oneshot(req).await.unwrap();
    let bytes = axum::body::to_bytes(resp.into_body(), usize::MAX)
        .await
        .unwrap();
    serde_json::from_slice(&bytes).unwrap()
}

#[tokio::test]
async fn client_address_corpus() {
    let c = corpus();
    let cases = c["client_address"].as_array().unwrap();
    assert!(cases.len() >= 30);
    for case in cases {
        let name = case["name"].as_str().unwrap();
        let app = isolated(strings(&case["trusted"]));
        let got = seen(app, case["peer"].as_str().unwrap(), &case["headers"]).await;
        let want = &case["want"];
        assert_eq!(got["client"], want["client"], "{name}: client");
        let want_peer = want["peer"].as_str().unwrap();
        if want_peer.is_empty() {
            assert!(got["peer"].is_null(), "{name}: peer {}", got["peer"]);
        } else {
            assert_eq!(got["peer"], want_peer, "{name}: peer");
        }
        let https = matches!(got["proto"].as_str(), Some("https" | "wss"));
        assert_eq!(https, want["https"], "{name}: https");
    }
}

#[test]
fn trusted_proxy_parsing_corpus() {
    let c = corpus();
    let t = &c["trusted_proxies"];
    let got = parse_trusted_proxies(&strings(&t["parse"]["input"])).unwrap();
    let got: Vec<String> = got.iter().map(|p| p.to_string()).collect();
    assert_eq!(got, strings(&t["parse"]["canonical"]));
    for bad in strings(&t["invalid"]) {
        let err = parse_trusted_proxies(std::slice::from_ref(&bad)).unwrap_err();
        assert!(err.contains(&bad), "{bad}: {err}");
    }
}

// --- host and origin -----------------------------------------------------

#[test]
fn host_corpus() {
    let c = corpus();
    for case in c["hosts"].as_array().unwrap() {
        let cfg = HostCheckConfig {
            allow: strings(&case["allow"]),
            refuse: None,
        };
        assert_eq!(
            host_permits(&cfg, case["host"].as_str().unwrap()),
            case["permitted"].as_bool().unwrap(),
            "{}",
            case["name"]
        );
    }
    for case in c["listener_hosts"].as_array().unwrap() {
        let (hosts, wildcard) = listener_hosts(case["addr"].as_str().unwrap());
        assert_eq!(
            wildcard,
            case["wildcard"].as_bool().unwrap(),
            "{}",
            case["addr"]
        );
        assert_eq!(hosts, strings(&case["hosts"]), "{}", case["addr"]);
    }
}

#[tokio::test]
async fn origin_corpus() {
    let c = corpus();
    for case in c["origin"].as_array().unwrap() {
        let name = case["name"].as_str().unwrap();
        let mut cfg = ApiServerConfig::new(
            "0.0.0.0:0",
            Router::new().route("/", any(|| async { "ok" })),
        );
        cfg.host_check = false;
        cfg.security_headers = false;
        cfg.compress = false;
        cfg.allow_origins = strings(&case["allow"]);
        cfg.log = Some(std::sync::Arc::new(|_| {}));
        let app = ApiService::build(cfg).unwrap().router();
        let mut req = Request::builder()
            .method(Method::from_bytes(case["method"].as_str().unwrap().as_bytes()).unwrap())
            .uri("/")
            .header("host", case["host"].as_str().unwrap());
        for (k, v) in case["headers"].as_object().unwrap() {
            req = req.header(k, v.as_str().unwrap());
        }
        let resp = app.oneshot(req.body(Body::empty()).unwrap()).await.unwrap();
        let permitted = case["permitted"].as_bool().unwrap();
        assert_eq!(resp.status() == 200, permitted, "{name}");
        if !permitted {
            assert_eq!(resp.status(), 403, "{name}");
            let bytes = axum::body::to_bytes(resp.into_body(), usize::MAX)
                .await
                .unwrap();
            let body: Value = serde_json::from_slice(&bytes).unwrap();
            assert_eq!(body["code"], "origin_rejected", "{name}");
            assert_eq!(body["message"], case["message"], "{name}");
        }
    }
    for bad in strings(&c["origin_invalid"]) {
        let mut cfg = ApiServerConfig::new("0.0.0.0:0", Router::new());
        cfg.allow_origins = vec![bad.clone()];
        assert!(ApiService::build(cfg).is_err(), "{bad}");
    }
}

// --- request id ----------------------------------------------------------

#[tokio::test]
async fn request_id_corpus() {
    let c = corpus();
    assert_eq!(
        c["constants"]["request_id_pattern"],
        "^[A-Za-z0-9._-]{1,128}$"
    );
    let rows = c["request_id"].as_array().unwrap();
    assert!(!rows.is_empty());
    let minted = |id: &str| id.len() == 32 && id.bytes().all(|b| matches!(b, b'0'..=b'9' | b'a'..=b'f'));
    for case in rows {
        let name = case["name"].as_str().unwrap();
        let mut cfg = ApiServerConfig::new(
            "0.0.0.0:0",
            Router::new().route("/", any(|| async { "ok" })),
        );
        cfg.host_check = false;
        cfg.origin_check = false;
        cfg.security_headers = false;
        cfg.compress = false;
        cfg.log = Some(std::sync::Arc::new(|_| {}));
        let app = ApiService::build(cfg).unwrap().router();
        let mut req = Request::builder().uri("/").header("host", "127.0.0.1");
        let sent = case.get("header").and_then(Value::as_str);
        if let Some(h) = sent {
            req = req.header(
                "x-request-id",
                axum::http::HeaderValue::from_bytes(h.as_bytes()).unwrap(),
            );
        }
        let resp = app.oneshot(req.body(Body::empty()).unwrap()).await.unwrap();
        let got = resp.headers()["x-request-id"].to_str().unwrap().to_string();
        if case["kept"].as_bool().unwrap() {
            assert_eq!(Some(got.as_str()), sent, "{name}");
        } else {
            assert!(minted(&got), "{name}: {got}");
            assert_ne!(Some(got.as_str()), sent, "{name}");
        }
    }
}

// --- OpenAPI -------------------------------------------------------------

fn schema(v: &Value) -> Option<Value> {
    (!v.is_null()).then(|| v.clone())
}

fn model(input: &Value) -> OpenApi {
    let mut doc = OpenApi::new(
        input["title"].as_str().unwrap(),
        input["version"].as_str().unwrap(),
    );
    doc.description = input["description"].as_str().unwrap_or("").to_string();
    for s in input["servers"].as_array().into_iter().flatten() {
        doc.servers.push(Server {
            url: s["url"].as_str().unwrap().into(),
            description: s["description"].as_str().unwrap_or("").into(),
        });
    }
    for (name, s) in input["schemas"].as_object().into_iter().flatten() {
        doc.schema(name.clone(), s.clone());
    }
    for o in input["operations"].as_array().unwrap() {
        let mut op = Operation {
            operation_id: o["operation_id"].as_str().unwrap().into(),
            method: o["method"].as_str().unwrap().into(),
            path: o["path"].as_str().unwrap().into(),
            summary: o["summary"].as_str().unwrap_or("").into(),
            description: o["description"].as_str().unwrap_or("").into(),
            tags: o["tags"]
                .as_array()
                .map(|t| t.iter().map(|s| s.as_str().unwrap().to_string()).collect())
                .unwrap_or_default(),
            ..Operation::default()
        };
        for p in o["parameters"].as_array().into_iter().flatten() {
            op.parameters.push(Param {
                name: p["name"].as_str().unwrap().into(),
                location: p["in"].as_str().unwrap().into(),
                required: p["required"].as_bool().unwrap_or(false),
                description: p["description"].as_str().unwrap_or("").into(),
                schema: schema(&p["schema"]),
            });
        }
        if let Some(b) = o["request_body"].as_object() {
            op.request_body = Some(RequestBody {
                description: b
                    .get("description")
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .into(),
                required: b.get("required").and_then(Value::as_bool).unwrap_or(false),
                schema: b.get("schema").and_then(schema),
            });
        }
        let mut responses = BTreeMap::new();
        for (code, r) in o["responses"].as_object().into_iter().flatten() {
            responses.insert(
                code.clone(),
                Response {
                    description: r["description"].as_str().unwrap_or("").into(),
                    schema: schema(&r["schema"]),
                },
            );
        }
        op.responses = responses;
        for (k, v) in o["extensions"].as_object().into_iter().flatten() {
            op.extensions.insert(k.clone(), v.clone());
        }
        doc.operation(op);
    }
    doc
}

#[test]
fn openapi_documents_match_huma() {
    let c = corpus();
    let cases = c["openapi_cases"].as_array().unwrap();
    assert!(cases.len() >= 3);
    for case in cases {
        let got = model(&case["input"]).to_value();
        assert_eq!(got, case["expected"], "{}", case["name"]);
    }
}

#[test]
fn crud_describes_the_five_resource_operations() {
    let mut doc = OpenApi::new("W", "1");
    doc.crud(
        "/widgets",
        "widgets",
        "Widget",
        json!({"type": "object"}),
        &[],
    );
    let v = doc.to_value();
    let paths = v["paths"].as_object().unwrap();
    assert_eq!(
        paths.keys().collect::<Vec<_>>(),
        ["/widgets/", "/widgets/{id}"]
    );
    let ids: Vec<&str> = doc
        .operations
        .iter()
        .map(|o| o.operation_id.as_str())
        .collect();
    assert_eq!(
        ids,
        [
            "widgets-create",
            "widgets-list",
            "widgets-get",
            "widgets-update",
            "widgets-delete"
        ]
    );
    assert_eq!(
        v["paths"]["/widgets/"]["get"]["parameters"]
            .as_array()
            .unwrap()
            .len(),
        4
    );
    assert_eq!(
        v["paths"]["/widgets/{id}"]["delete"]["responses"]["204"]["description"],
        "No Content"
    );

    let mut only = OpenApi::new("W", "1");
    only.crud("/a/b", "t", "S", json!({}), &["get", "list"]);
    let ids: Vec<&str> = only
        .operations
        .iter()
        .map(|o| o.operation_id.as_str())
        .collect();
    assert_eq!(ids, ["a-b-list", "a-b-get"]);
}
