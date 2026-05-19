// Integration tests for telemetry::client. Gated on the `telemetry`
// feature since the cargo test harness compiles every tests/*.rs
// unconditionally.
#![cfg(feature = "telemetry")]
// Each test holds ENV_LOCK (std::sync::Mutex) across .await calls in
// the client's async API. Clippy flags this as a potential deadlock
// hazard, but the lock is intentionally held to serialise process-
// global env mutation across the integration binary — dropping it
// mid-await would defeat the serialisation. Using tokio::sync::Mutex
// would add real overhead for what is purely a test-coordination lock.
#![allow(clippy::await_holding_lock)]

use hop_top_kit::telemetry::{Client, ClientOptions, SinkKind};
use serde_json::{json, Value};
use std::io::Write;
use std::sync::Mutex;
use std::time::Duration;

// All tests below mutate process-global env vars (consent file path,
// install_id state dir, mode). std::env::set_var is not thread-safe;
// serialise across this integration binary.
static ENV_LOCK: Mutex<()> = Mutex::new(());

/// Set up a temp XDG layout with a granted consent file + writable
/// state dir for installation_id, plus mode=full. Returns the
/// tempdir so it stays alive for the test body.
fn setup_env(mode: &str) -> tempfile::TempDir {
    let tmp = tempfile::tempdir().unwrap();
    let cfg = tmp.path().join("config");
    let state = tmp.path().join("state");
    std::fs::create_dir_all(cfg.join("kit")).unwrap();
    std::fs::create_dir_all(&state).unwrap();
    let mut f = std::fs::File::create(cfg.join("kit").join("config.yaml")).unwrap();
    writeln!(
        f,
        "kit:\n  telemetry:\n    consent:\n      state: granted\n      prompt_version: 1\n      decision_source: config\n      decided_at: \"2026-05-19T00:00:00Z\"\n"
    )
    .unwrap();

    std::env::set_var("XDG_CONFIG_HOME", cfg.to_str().unwrap());
    std::env::set_var("XDG_STATE_HOME", state.to_str().unwrap());
    std::env::set_var("KIT_TELEMETRY_MODE", mode);
    std::env::remove_var("KIT_APP_PREFIX");
    tmp
}

fn teardown_env() {
    std::env::remove_var("XDG_CONFIG_HOME");
    std::env::remove_var("XDG_STATE_HOME");
    std::env::remove_var("KIT_TELEMETRY_MODE");
}

#[tokio::test]
async fn record_writes_jsonl_line() {
    let _g = ENV_LOCK.lock().unwrap();
    let tmp = setup_env("full");
    let sink_path = tmp.path().join("events.jsonl");

    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some(sink_path.to_string_lossy().into_owned()),
        queue_size: 16,
        ..Default::default()
    })
    .expect("client construction");

    client.record("test.event", json!({"k": "v"})).unwrap();
    client.record("test.event2", json!({"n": 1})).unwrap();

    // Give the drain task a beat to flush + then shutdown.
    client.shutdown(Duration::from_millis(200)).await.unwrap();

    let content = std::fs::read_to_string(&sink_path).expect("sink file");
    let lines: Vec<&str> = content.lines().collect();
    assert_eq!(lines.len(), 2, "got: {content}");
    for line in &lines {
        let v: Value = serde_json::from_str(line).unwrap();
        assert_eq!(v["schema_version"], "1");
        assert_eq!(v["sdk_lang"], "rs");
        assert_eq!(v["mode"], "full");
        assert!(v["installation_id"].as_str().unwrap().len() == 64);
    }
    teardown_env();
}

#[tokio::test]
async fn mode_off_is_no_op() {
    let _g = ENV_LOCK.lock().unwrap();
    let tmp = setup_env("off");
    let sink_path = tmp.path().join("events.jsonl");

    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some(sink_path.to_string_lossy().into_owned()),
        queue_size: 16,
        ..Default::default()
    })
    .expect("client construction");

    for _ in 0..5 {
        client.record("nope", json!({})).unwrap();
    }
    client.shutdown(Duration::from_millis(100)).await.unwrap();

    // File never created (or empty if pre-existed).
    let content = std::fs::read_to_string(&sink_path).unwrap_or_default();
    assert!(content.is_empty(), "expected no events, got: {content}");
    teardown_env();
}

#[tokio::test]
async fn consent_denied_is_no_op() {
    let _g = ENV_LOCK.lock().unwrap();
    let tmp = setup_env("full");
    // Overwrite consent file with denied state.
    let cfg = tmp.path().join("config").join("kit").join("config.yaml");
    std::fs::write(
        &cfg,
        "kit:\n  telemetry:\n    consent:\n      state: denied\n      prompt_version: 1\n",
    )
    .unwrap();

    let sink_path = tmp.path().join("events.jsonl");
    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some(sink_path.to_string_lossy().into_owned()),
        queue_size: 16,
        ..Default::default()
    })
    .expect("client construction");

    client.record("denied.event", json!({"k": "v"})).unwrap();
    client.shutdown(Duration::from_millis(100)).await.unwrap();

    let content = std::fs::read_to_string(&sink_path).unwrap_or_default();
    assert!(content.is_empty());
    teardown_env();
}

#[tokio::test]
async fn record_is_non_blocking_under_saturation() {
    let _g = ENV_LOCK.lock().unwrap();
    let tmp = setup_env("full");
    let sink_path = tmp.path().join("events.jsonl");

    // Tiny queue; we'll overflow it before the drain catches up.
    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some(sink_path.to_string_lossy().into_owned()),
        queue_size: 2,
        ..Default::default()
    })
    .expect("client construction");

    // Hammer the queue. record() MUST return immediately even when
    // the channel is full; dropped events bump the counter.
    let start = std::time::Instant::now();
    for i in 0..1000 {
        client.record("flood", json!({"i": i})).unwrap();
    }
    let elapsed = start.elapsed();
    // Non-blocking contract: 1000 try_sends should complete in well
    // under a second even with a queue cap of 2. A loose 500ms bound
    // catches blocking regressions without being flaky on CI.
    assert!(
        elapsed < Duration::from_millis(500),
        "record() blocked: took {elapsed:?}"
    );

    // At queue_size=2 with a fast drain, the dropped counter should
    // be > 0 for a 1000-event burst (the drain can't keep up with
    // synchronous bursts at this rate).
    assert!(
        client.dropped_count() > 0,
        "expected dropped_count > 0, got 0; drain may be too fast"
    );

    client.shutdown(Duration::from_millis(200)).await.unwrap();
    teardown_env();
}

#[tokio::test]
async fn anon_mode_drops_caller_attrs() {
    let _g = ENV_LOCK.lock().unwrap();
    let tmp = setup_env("anon");
    let sink_path = tmp.path().join("events.jsonl");

    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some(sink_path.to_string_lossy().into_owned()),
        queue_size: 16,
        ..Default::default()
    })
    .expect("client construction");

    client
        .record("anon.event", json!({"secret": "alice@example.com"}))
        .unwrap();
    client.shutdown(Duration::from_millis(200)).await.unwrap();

    let content = std::fs::read_to_string(&sink_path).expect("sink file");
    let line = content.lines().next().expect("at least one line");
    let v: Value = serde_json::from_str(line).unwrap();
    assert_eq!(v["mode"], "anon");
    // Anon-tier defensive strip drops caller-provided free-form attrs.
    // We surface this as attrs == null.
    assert_eq!(v["attrs"], Value::Null);
    teardown_env();
}

#[tokio::test]
async fn custom_redactor_runs_before_default() {
    let _g = ENV_LOCK.lock().unwrap();
    let tmp = setup_env("full");
    let sink_path = tmp.path().join("events.jsonl");

    // Custom redactor inserts a token-shaped string. The default
    // redactor that runs AFTER must rewrite it to <redacted:token>.
    let custom = |_v: Value| -> Value { json!("sk-AbCdEf1234567890") };

    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some(sink_path.to_string_lossy().into_owned()),
        queue_size: 16,
        redactor: Some(Box::new(custom)),
        ..Default::default()
    })
    .expect("client construction");

    client
        .record("evt", json!({"original": "untouched"}))
        .unwrap();
    client.shutdown(Duration::from_millis(200)).await.unwrap();

    let content = std::fs::read_to_string(&sink_path).expect("sink file");
    let line = content.lines().next().expect("at least one line");
    let v: Value = serde_json::from_str(line).unwrap();
    // Custom redactor swapped attrs to a token-shaped string; default
    // redactor then rewrote it. Defense in depth.
    assert_eq!(v["attrs"].as_str().unwrap(), "<redacted:token>");
    teardown_env();
}

#[tokio::test]
async fn https_sink_posts_ndjson() {
    let _g = ENV_LOCK.lock().unwrap();
    let _tmp = setup_env("full");

    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("POST", "/")
        .match_header("content-type", "application/x-ndjson")
        .with_status(202)
        .create_async()
        .await;

    let client = Client::new(ClientOptions {
        sink: SinkKind::Https,
        endpoint: Some(server.url()),
        queue_size: 16,
        ..Default::default()
    })
    .expect("client construction");

    client.record("evt", json!({"k": "v"})).unwrap();
    // Give reqwest time to fire.
    tokio::time::sleep(Duration::from_millis(300)).await;
    client.shutdown(Duration::from_millis(200)).await.unwrap();

    mock.assert_async().await;
    teardown_env();
}
