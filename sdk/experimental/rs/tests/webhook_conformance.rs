//! The shared webhook corpus in `contracts/webhook-v1`, run against the
//! Rust receiver. Go is the authority; a failure means this port drifted.
#![cfg(feature = "webhook")]

use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::{Duration, UNIX_EPOCH};

use axum::body::Body;
use axum::http::Request;
use axum::Router;
use base64::Engine;
use hop_top_kit::api_server::webhook::{sign_hmac, HmacConfig, Receiver, WebhookPublisher};
use hop_top_kit::kv::SqliteStore;
use serde_json::Value;
use tower::ServiceExt;

#[derive(Default)]
struct Rec {
    events: Mutex<Vec<(String, Value)>>,
    failures: Mutex<usize>,
}

impl WebhookPublisher for Rec {
    fn publish(&self, topic: &str, _source: &str, payload: Value) -> Result<(), String> {
        let mut f = self.failures.lock().unwrap();
        if *f > 0 {
            *f -= 1;
            return Err("bus down".into());
        }
        self.events.lock().unwrap().push((topic.into(), payload));
        Ok(())
    }
}

fn corpus() -> Value {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../../contracts/webhook-v1/vectors.json");
    serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
}

fn s<'a>(v: &'a Value, k: &str) -> &'a str {
    v[k].as_str().unwrap_or("")
}

fn hmac_cfg(secret: &str, hash: &str, enc: &str, prefix: &str, ts_header: &str) -> HmacConfig {
    HmacConfig {
        header: "X".into(),
        secret: secret.as_bytes().to_vec(),
        hash: hash.into(),
        prefix: prefix.into(),
        encoding: enc.into(),
        timestamp_header: ts_header.into(),
    }
}

#[test]
fn hmac_digests_match_the_corpus() {
    let c = corpus();
    let secret: String = c["secret_parts"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| p.as_str().unwrap())
        .collect();
    let mut checked = 0;
    for v in c["hmac_vectors"].as_array().unwrap() {
        if s(v, "hash") == "sha1" {
            continue; // not supported in this port, by design
        }
        let ts = s(v, "timestamp");
        let cfg = hmac_cfg(
            &secret,
            s(v, "hash"),
            s(v, "encoding"),
            s(v, "prefix"),
            if ts.is_empty() { "" } else { "T" },
        );
        assert_eq!(
            sign_hmac(&cfg, ts, s(v, "body").as_bytes()).unwrap(),
            s(v, "expected"),
            "{v}"
        );
        checked += 1;
    }
    assert!(checked >= 4);
    assert!(sign_hmac(&hmac_cfg(&secret, "sha1", "", "", ""), "", b"x").is_err());
}

#[tokio::test]
async fn request_sequences_match_the_corpus() {
    let c = corpus();
    let secret: String = c["secret_parts"]
        .as_array()
        .unwrap()
        .iter()
        .map(|p| p.as_str().unwrap())
        .collect();
    let sub = |t: &str| t.replace("{{SECRET}}", &secret);
    let now = {
        let t = s(&c, "now");
        // 2026-10-31T12:00:00Z
        assert_eq!(t, "2026-10-31T12:00:00Z");
        UNIX_EPOCH + Duration::from_secs(1_793_448_000)
    };
    let mut ran = 0;
    for case in c["cases"].as_array().unwrap() {
        let name = s(case, "name");
        let cfgv = &case["config"];
        let rec = Arc::new(Rec::default());
        *rec.failures.lock().unwrap() = cfgv["publish_failures"].as_u64().unwrap_or(0) as usize;
        let dir = tempfile::tempdir().unwrap();
        let store = SqliteStore::new(dir.path().join("kv.db").to_str().unwrap()).unwrap();
        let window = Duration::from_secs(cfgv["replay_window_seconds"].as_u64().unwrap_or(0));
        let mut b = Receiver::builder("fixture")
            .publisher(rec.clone())
            .idempotency(store, Duration::from_secs(3600), None)
            .clock(Arc::new(move || now));
        if let Some(n) = cfgv["max_body"].as_u64() {
            b = b.max_body(n as usize);
        }
        b = match s(case, "preset") {
            "graph" => b.graph(&sub(s(cfgv, "client_state"))),
            "pipedrive" => b.pipedrive(s(cfgv, "user"), &sub(s(cfgv, "password")), window),
            "hmac" => b
                .generic_hmac(
                    HmacConfig {
                        header: s(cfgv, "header").into(),
                        secret: sub(s(cfgv, "secret")).into_bytes(),
                        hash: s(cfgv, "hash").into(),
                        prefix: s(cfgv, "prefix").into(),
                        encoding: s(cfgv, "encoding").into(),
                        timestamp_header: s(cfgv, "timestamp_header").into(),
                    },
                    window,
                    s(cfgv, "key_header"),
                )
                .unwrap(),
            other => panic!("preset {other}"),
        };
        let app = Router::new().route("/hook", b.build().unwrap().route());

        for (i, rq) in case["requests"].as_array().unwrap().iter().enumerate() {
            let ctx = format!("{name} #{}", i + 1);
            let body = sub(s(rq, "body"));
            let mut req = Request::builder()
                .method(s(rq, "method"))
                .uri(s(rq, "target"));
            for (k, v) in rq["headers"].as_object().unwrap() {
                req = req.header(k, sub(v.as_str().unwrap()));
            }
            if let Some(sg) = rq["sign"].as_object() {
                let get = |k: &str| sg.get(k).and_then(Value::as_str).unwrap_or("");
                let ts = rq["headers"]
                    .get(get("timestamp_header"))
                    .and_then(Value::as_str)
                    .unwrap_or("");
                let mut signed = body.clone().into_bytes();
                if rq["tamper"].as_bool().unwrap_or(false) {
                    signed.push(b'x');
                }
                let mut cfg = hmac_cfg(
                    &secret,
                    get("hash"),
                    get("encoding"),
                    get("prefix"),
                    get("timestamp_header"),
                );
                cfg.header = get("header").into();
                req = req.header(get("header"), sign_hmac(&cfg, ts, &signed).unwrap());
            }
            if let Some(bs) = rq["basic"].as_object() {
                let cred = format!(
                    "{}:{}",
                    bs["user"].as_str().unwrap(),
                    sub(bs["password"].as_str().unwrap())
                );
                req = req.header(
                    "authorization",
                    format!(
                        "Basic {}",
                        base64::engine::general_purpose::STANDARD.encode(cred)
                    ),
                );
            }
            let resp = app
                .clone()
                .oneshot(req.body(Body::from(body.clone())).unwrap())
                .await
                .unwrap();
            let want = &rq["want"];
            assert_eq!(
                resp.status().as_u16() as u64,
                want["status"].as_u64().unwrap(),
                "{ctx}"
            );
            let ct = resp
                .headers()
                .get("content-type")
                .map(|v| v.to_str().unwrap().to_string());
            let text = String::from_utf8(
                axum::body::to_bytes(resp.into_body(), usize::MAX)
                    .await
                    .unwrap()
                    .to_vec(),
            )
            .unwrap();
            if let Some(code) = want["code"].as_str() {
                let e: Value = serde_json::from_str(&text).unwrap();
                assert_eq!(e["code"], code, "{ctx}");
                assert!(!text.contains(&secret), "{ctx}");
            }
            if let Some(b) = want["body"].as_str() {
                assert_eq!(text, b, "{ctx}");
            }
            if want["body_json"].is_object() {
                assert_eq!(
                    serde_json::from_str::<Value>(&text).unwrap(),
                    want["body_json"],
                    "{ctx}"
                );
            }
            if let Some(w) = want["content_type"].as_str() {
                assert_eq!(ct.as_deref(), Some(w), "{ctx}");
            }
            let events = rec.events.lock().unwrap();
            assert_eq!(
                events.len() as u64,
                want["published"].as_u64().unwrap(),
                "{ctx}"
            );
            if let Some((topic, p)) = events.last() {
                if let Some(t) = want["topic"].as_str() {
                    assert_eq!(topic, t, "{ctx}");
                    assert_eq!(p["receiver"], "fixture", "{ctx}");
                    if let Some(k) = want["key"].as_str() {
                        assert_eq!(p["key"], k, "{ctx}");
                    }
                }
                if let Some(k) = want["kind"].as_str() {
                    assert_eq!(p["kind"], k, "{ctx}");
                }
                if let Some(e) = want["encoding"].as_str() {
                    assert_eq!(p["encoding"], e, "{ctx}");
                    if e == "base64" {
                        assert_eq!(
                            p["body"],
                            base64::engine::general_purpose::STANDARD.encode(&body),
                            "{ctx}"
                        );
                    }
                }
            }
        }
        ran += 1;
    }
    assert!(ran >= 19, "{ran}");
}
