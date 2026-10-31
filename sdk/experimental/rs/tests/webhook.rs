//! Receiver rules beyond the shared corpus.
#![cfg(feature = "webhook")]

use std::sync::{Arc, Mutex};
use std::time::Duration;

use axum::body::Body;
use axum::http::Request;
use axum::Router;
use hop_top_kit::api_server::webhook::{
    basic_auth, header_key, hmac, HmacConfig, Receiver, WebhookPublisher,
};
use hop_top_kit::kv::SqliteStore;
use serde_json::Value;
use tower::ServiceExt;

#[derive(Default)]
struct Rec(Mutex<usize>);

impl WebhookPublisher for Rec {
    fn publish(&self, _: &str, _: &str, _: Value) -> Result<(), String> {
        *self.0.lock().unwrap() += 1;
        Ok(())
    }
}

fn base(name: &str) -> hop_top_kit::api_server::webhook::ReceiverBuilder {
    Receiver::builder(name)
        .publisher(Arc::new(Rec::default()))
        .verifier(basic_auth("u", "p"))
}

#[test]
fn receiver_names_are_restricted() {
    for bad in ["a/b", "A", "a b", "a.b", ""] {
        assert!(base(bad).build().is_err(), "{bad:?}");
    }
    assert!(base("graph-1_x").build().is_ok());
}

#[test]
fn a_bad_timestamp_is_refused_before_the_mac() {
    let v = hmac(HmacConfig {
        header: "S".into(),
        secret: b"k".to_vec(),
        timestamp_header: "T".into(),
        ..HmacConfig::default()
    })
    .unwrap();
    for ts in ["123.abc", "12 3", "+12", "-5", "", "0012", &"9".repeat(40)] {
        let (parts, _) = Request::builder()
            .header("S", "anything")
            .header("T", ts)
            .body(())
            .unwrap()
            .into_parts();
        let err = v(&parts, b"").unwrap_err();
        assert!(err.contains("timestamp"), "{ts:?}: {err}");
    }
}

#[tokio::test]
async fn a_header_key_alone_never_claims() {
    let dir = tempfile::tempdir().unwrap();
    let store = SqliteStore::new(dir.path().join("k.db").to_str().unwrap()).unwrap();
    let rec = Arc::new(Rec::default());
    let rc = base("t")
        .publisher(rec.clone())
        .idempotency(store, Duration::from_secs(3600), Some(header_key("X-Id")))
        .build()
        .unwrap();
    let app = Router::new().route("/", rc.route());
    let send = |id: &'static str, body: &'static str| {
        let app = app.clone();
        async move {
            app.oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/")
                    .header("authorization", "Basic dTpw")
                    .header("x-id", id)
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap()
            .status()
            .as_u16()
        }
    };
    assert_eq!(send("one", "body").await, 202);
    assert_eq!(
        send("two", "body").await,
        200,
        "changed unsigned header, same delivery"
    );
    assert_eq!(send("one", "other").await, 202);
    assert_eq!(*rec.0.lock().unwrap(), 2);
}
