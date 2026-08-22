// Mirrors go/core/netpolicy's suite. Cargo compiles every file under
// tests/ unconditionally, so the whole body is gated on the `api`
// feature — the module lives beneath it and the unresolved imports
// would otherwise break `cargo test` under default features.
#![cfg(feature = "api")]

use hop_top_kit::netpolicy::{is_loopback, GuardedClient, NetError, NetPolicy, OfflineError};

// A policy-marked client must stop an external request before it opens
// a socket. The destination is deliberately unroutable: loopback is
// exempt by design, so a mockito server would legitimately be allowed
// through and prove nothing.
#[tokio::test]
async fn blocks_external_when_offline() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .get("https://example.invalid/v1/thing")
        .send()
        .await
        .expect_err("expected offline refusal");

    let offline = err
        .as_offline()
        .expect("error did not carry the typed offline variant");
    assert_eq!(offline.method, "GET");
    assert_eq!(offline.url, "https://example.invalid/v1/thing");
}

// Loopback stays reachable when offline: --offline means "no network",
// not "cannot talk to myself". A real server on 127.0.0.1 proves the
// request reached the wire rather than being refused.
#[tokio::test]
async fn allows_loopback_when_offline() {
    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("GET", "/health")
        .with_status(204)
        .expect(1)
        .create_async()
        .await;

    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let resp = client
        .get(format!("{}/health", server.url()))
        .send()
        .await
        .expect("loopback request was refused while offline");

    assert_eq!(resp.status().as_u16(), 204);
    mock.assert_async().await;
}

// Every loopback spelling Go exempts must be exempt here too. Whether
// anything is listening on these ports is the host's business: the
// assertion is only that the guard did not refuse the request, so a
// connection-refused transport error and a real response both pass.
#[tokio::test]
async fn loopback_spellings_are_exempt() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    for target in [
        "http://127.0.0.1:8080/health",
        "http://localhost:9000/health",
        "http://[::1]:9000/health",
    ] {
        if let Err(err) = client.get(target).send().await {
            assert!(
                err.as_offline().is_none(),
                "{target}: loopback was refused by the offline guard"
            );
        }
    }
}

// A DNS name is remote even if it might resolve to loopback: resolving
// it is itself network access.
#[tokio::test]
async fn blocks_dns_names_when_offline() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .get("http://my-host.internal/health")
        .send()
        .await
        .expect_err("expected offline refusal");

    assert!(
        err.as_offline().is_some(),
        "DNS-named host was allowed through: {err}"
    );
}

// An online policy must leave traffic entirely unaffected.
#[tokio::test]
async fn allows_external_when_online() {
    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("GET", "/health")
        .with_status(204)
        .expect(1)
        .create_async()
        .await;

    let client = GuardedClient::new(NetPolicy::online()).expect("build client");
    let resp = client
        .get(format!("{}/health", server.url()))
        .send()
        .await
        .expect("online request failed");

    assert_eq!(resp.status().as_u16(), 204);
    mock.assert_async().await;
}

// The typed variant must be matchable through the std error chain, the
// way callers match Go's ErrOffline with errors.Is.
#[tokio::test]
async fn offline_error_is_matchable_via_source_chain() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .get("https://example.invalid/x")
        .send()
        .await
        .expect_err("expected offline refusal");

    // The message names the flag, the way Go's ErrOffline does.
    assert!(
        err.to_string().contains("network disabled by --offline"),
        "message did not name the flag: {err}"
    );
    // And the typed refusal survives boxing as a std error, so a caller
    // holding a `Box<dyn Error>` can still recover it.
    let boxed: Box<dyn std::error::Error> = Box::new(err);
    let recovered = boxed
        .downcast_ref::<NetError>()
        .and_then(NetError::as_offline)
        .expect("offline variant lost through Box<dyn Error>");
    assert_eq!(recovered.method, "GET");
    let _: &OfflineError = recovered;
}

// Enforcement must sit beneath a naive caller: code that constructs the
// client through the port's shared path and sends a request with no
// offline awareness at all is still refused. This is the guarantee
// test — a marker with no chokepoint fails here.
#[tokio::test]
async fn naive_caller_is_refused() {
    async fn naive(client: &GuardedClient) -> Result<u16, NetError> {
        // No policy check anywhere in this function.
        Ok(client
            .get("https://api.example.com/v1/items")
            .send()
            .await?
            .status()
            .as_u16())
    }

    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = naive(&client)
        .await
        .expect_err("naive caller reached the wire");
    assert!(
        err.as_offline().is_some(),
        "naive caller was not refused: {err}"
    );
}

// Host classification, matching Go's isLoopback table.
#[test]
fn loopback_classification() {
    for host in ["127.0.0.1", "localhost", "::1", "127.1.2.3"] {
        assert!(is_loopback(host), "{host} should be loopback");
    }
    for host in [
        "example.com",
        "my-host.internal",
        "8.8.8.8",
        "localhost.evil.com",
    ] {
        assert!(!is_loopback(host), "{host} should be remote");
    }
}

// The default policy is online: a port that never wires the flag must
// behave exactly as it does today.
#[test]
fn default_policy_is_online() {
    assert!(!NetPolicy::default().is_offline());
    assert!(NetPolicy::offline().is_offline());
    assert!(!NetPolicy::online().is_offline());
    // The bool constructor mirrors Go's WithOffline(ctx, bool).
    assert!(NetPolicy::new(true).is_offline());
    assert!(!NetPolicy::new(false).is_offline());
}

// ApiClient is the port's public HTTP surface: constructing it under an
// offline policy must refuse external calls, and callers must be able
// to tell a refusal apart from an ordinary transport failure.
#[tokio::test]
async fn api_client_honours_offline_policy() {
    use hop_top_kit::api::{ApiClient, OFFLINE_CODE};

    let client = ApiClient::with_policy("https://api.example.com", NetPolicy::offline())
        .expect("build api client");
    let err = client
        .get::<serde_json::Value>("abc")
        .await
        .expect_err("api client reached the wire while offline");

    assert_eq!(err.code, OFFLINE_CODE);
    assert_eq!(err.status, 0);
    assert!(err.message.contains("network disabled by --offline"));
}

// The default ApiClient constructor stays online, so existing callers
// are unaffected until the CLI layer wires the flag.
#[tokio::test]
async fn api_client_default_is_online() {
    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("GET", "/abc")
        .with_status(200)
        .with_header("content-type", "application/json")
        .with_body(r#"{"ok":true}"#)
        .expect(1)
        .create_async()
        .await;

    let client = hop_top_kit::api::ApiClient::new(&server.url());
    let v: serde_json::Value = client.get("abc").await.expect("online request failed");
    assert_eq!(v["ok"], true);
    mock.assert_async().await;
}

// Telemetry is logging-class egress: `--offline` stops traffic the user
// asked for, it is not a second consent gate on diagnostics. Consent and
// telemetry mode already govern whether anything is emitted at all.
//
// The sink builds a plain `reqwest::Client`, never a `GuardedClient`, so
// there is no policy knob a CLI could thread through to mute it. This
// pins the construction path: an Https sink must build without any
// network policy being supplied.
#[tokio::test]
async fn telemetry_https_sink_builds_without_a_net_policy() {
    use hop_top_kit::telemetry::{Client, ClientOptions, SinkKind};

    let built = Client::new(ClientOptions {
        sink: SinkKind::Https,
        endpoint: Some("https://telemetry.example.com/v1/events".into()),
        queue_size: 4,
        ..Default::default()
    });

    assert!(
        built.is_ok(),
        "https telemetry sink must construct with no net policy: \
         --offline must not be able to suppress logging-class egress"
    );
}
