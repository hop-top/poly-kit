//! The Triton client against a loopback server, ported from Go
//! `ai/llm/triton`.

#![cfg(feature = "llm-triton")]

mod llm_support;

use hop_top_kit::llm::triton::{Client, Scorer};
use hop_top_kit::llm::{default_registry, LlmError, Provider, ProviderConfig, ResolvedConfig};
use llm_support::{serve, Canned};
use serde_json::json;

fn client(base: &str, model: &str, key: &str) -> Result<Client, LlmError> {
    Client::new(ResolvedConfig {
        provider: ProviderConfig {
            base_url: base.into(),
            model: model.into(),
            api_key: key.into(),
            ..ProviderConfig::default()
        },
        ..ResolvedConfig::default()
    })
}

fn out(data: serde_json::Value) -> String {
    json!({"outputs": [{"name": "output", "shape": [1, 1], "datatype": "FP64", "data": data}]})
        .to_string()
}

#[test]
fn validation_and_defaults() {
    let e = client("", "", "").unwrap_err().to_string();
    assert!(e.contains("model name is required"), "{e}");
    let c = client("", "mf", "").unwrap();
    assert_eq!(
        (c.model_name(), c.base_url().as_str()),
        ("mf", "http://localhost:8000")
    );
    assert_eq!(
        client("http://triton:9000/", "bert", "")
            .unwrap()
            .base_url(),
        "http://triton:9000"
    );
    assert!(client("", "mf", "").unwrap().capabilities().is_empty());
}

#[tokio::test]
async fn score_success() {
    let srv = serve(vec![Canned::json(200, &out(json!([0.75])))]).await;
    let s = client(&srv.url, "mf", "")
        .unwrap()
        .score(&[1.0, 2.0, 3.0])
        .await
        .unwrap();
    assert!((s - 0.75).abs() < 1e-3);
    let got = srv.only();
    assert_eq!(got.path, "/v2/models/mf/infer");
    assert_eq!(got.header("content-type"), Some("application/json"));
    assert_eq!(got.header("authorization"), None);
    assert_eq!(
        got.json(),
        json!({"inputs": [{"name": "input", "shape": [1, 3], "datatype": "FP32", "data": [[1.0, 2.0, 3.0]]}]})
    );
}

#[tokio::test]
async fn f32_inputs_keep_their_short_form() {
    let srv = serve(vec![Canned::json(200, &out(json!([0.5])))]).await;
    client(&srv.url, "mf", "")
        .unwrap()
        .score(&[1.1])
        .await
        .unwrap();
    assert_eq!(srv.only().json()["inputs"][0]["data"], json!([[1.1]]));
}

#[tokio::test]
async fn failures() {
    let srv = serve(vec![Canned::json(500, "internal error")]).await;
    let e = client(&srv.url, "mf", "")
        .unwrap()
        .score(&[1.0])
        .await
        .unwrap_err();
    assert!(e.to_string().contains("HTTP 500"), "{e}");
    assert!(e.is_fallbackable());
    assert_eq!(srv.attempts(), 1);
    let srv = serve(vec![Canned::json(200, r#"{"outputs":[]}"#)]).await;
    let e = client(&srv.url, "mf", "")
        .unwrap()
        .score(&[1.0])
        .await
        .unwrap_err();
    assert!(e.to_string().contains("no outputs"), "{e}");
    let srv = serve(vec![Canned::json(200, &out(json!([])))]).await;
    let e = client(&srv.url, "mf", "")
        .unwrap()
        .score(&[1.0])
        .await
        .unwrap_err();
    assert!(e.to_string().contains("empty output data"), "{e}");
    let e = client("http://127.0.0.1:1", "mf", "")
        .unwrap()
        .score(&[f32::NAN])
        .await
        .unwrap_err();
    assert!(e.to_string().contains("non-finite"), "{e}");
}

#[tokio::test]
async fn key_is_sent_when_set() {
    for (key, want) in [
        ("", None),
        ("  ", None),
        ("fake-triton-key", Some("Bearer fake-triton-key")),
    ] {
        let srv = serve(vec![Canned::json(200, &out(json!([1.0])))]).await;
        client(&srv.url, "mf", key)
            .unwrap()
            .score(&[1.0])
            .await
            .unwrap();
        assert_eq!(srv.only().header("authorization"), want, "{key:?}");
    }
}

#[tokio::test]
async fn base_url_credentials_never_surface() {
    const SECRET: &str = "fake-base-url-secret-must-not-leak";
    for base in [
        format!("http://{SECRET}@127.0.0.1:1"),
        format!("http://user:{SECRET}@127.0.0.1:1"),
        format!("http://127.0.0.1:1?key={SECRET}"),
    ] {
        match client(&base, "mf", "") {
            Ok(c) => {
                let e = c.score(&[1.0]).await.unwrap_err();
                assert!(!e.to_string().contains(SECRET), "{e}");
            }
            Err(e) => assert!(!e.to_string().contains(SECRET), "{e}"),
        }
    }
    let e = client(&format!("http://h\x7f?key={SECRET}"), "mf", "")
        .unwrap_err()
        .to_string();
    assert!(!e.contains(SECRET), "{e}");
}

#[test]
fn registered_by_scheme() {
    let p = default_registry().resolve("triton://mf").unwrap();
    assert!(p.capabilities().is_empty());
}
