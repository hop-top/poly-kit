//! The anthropic adapter beyond the shared corpus: stream error
//! classification, offline refusal, cancellation, response mapping and
//! secrets kept out of errors. Every request goes to a loopback server.

#![cfg(feature = "llm-anthropic")]

mod llm_support;

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Duration;

use hop_top_kit::llm::anthropic::Adapter;
use hop_top_kit::llm::{
    LlmError, Message, Provider, ProviderConfig, Request, ResolvedConfig, ToolDef,
};
use hop_top_kit::netpolicy::NetPolicy;
use llm_support::{eventually, serve, Canned};

const SECRET: &str = "fake-base-url-secret-must-not-leak";

fn cfg(base: &str, key: &str) -> ResolvedConfig {
    ResolvedConfig {
        provider: ProviderConfig {
            api_key: key.into(),
            base_url: base.into(),
            model: "claude-opus-5-5".into(),
            ..ProviderConfig::default()
        },
        ..ResolvedConfig::default()
    }
}

fn adapter(base: &str) -> Adapter {
    Adapter::builder(cfg(base, "k"))
        .env(|_| None)
        .build()
        .unwrap()
}

fn hi() -> Request {
    Request {
        messages: vec![Message::user("Hi")],
        ..Request::default()
    }
}

const START: &str = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n";

fn error_event(kind: &str) -> String {
    format!(
        "{START}event: error\ndata: {{\"type\":\"error\",\"error\":{{\"type\":\"{kind}\",\"message\":\"m\"}}}}\n\n"
    )
}

async fn drain_error(base: &str) -> LlmError {
    let a = adapter(base);
    let mut s = a.stream(&hi()).await.unwrap();
    loop {
        match s.next().await {
            Ok(Some(t)) if t.done => panic!("stream ended without the error"),
            Ok(Some(_)) => {}
            Ok(None) => panic!("stream ended without the error"),
            Err(e) => {
                assert!(matches!(s.next().await, Ok(None)), "end after an error");
                return e;
            }
        }
    }
}

#[tokio::test]
async fn mid_stream_overloaded_is_fallbackable_http_529() {
    let srv = serve(vec![Canned::sse(&error_event("overloaded_error"))]).await;
    let err = drain_error(&srv.url).await;
    assert!(
        matches!(err, LlmError::HttpStatus { status: 529, .. }),
        "{err:?}"
    );
    assert!(err.is_fallbackable());
}

#[tokio::test]
async fn mid_stream_rate_limit_is_a_rate_limit() {
    let srv = serve(vec![Canned::sse(&error_event("rate_limit_error"))]).await;
    let err = drain_error(&srv.url).await;
    assert!(matches!(err, LlmError::RateLimit { .. }), "{err:?}");
    assert!(err.is_fallbackable());
}

#[tokio::test]
async fn mid_stream_invalid_request_is_not_fallbackable() {
    let srv = serve(vec![Canned::sse(&error_event("invalid_request_error"))]).await;
    let err = drain_error(&srv.url).await;
    assert!(matches!(err, LlmError::Api { status: 400, .. }), "{err:?}");
    assert!(!err.is_fallbackable());
}

#[tokio::test]
async fn offline_refusal_is_never_retried() {
    let sleeps = Arc::new(AtomicUsize::new(0));
    let s = sleeps.clone();
    let mut c = cfg("https://api.anthropic.invalid", "k");
    c.net_policy = NetPolicy::offline();
    let a = Adapter::builder(c)
        .env(|_| None)
        .sleeper(Arc::new(move |_| {
            s.fetch_add(1, Ordering::SeqCst);
            Box::pin(async { true })
        }))
        .build()
        .unwrap();
    let err = a.complete(&hi()).await.unwrap_err();
    assert!(matches!(err, LlmError::Offline(_)), "{err:?}");
    assert!(!err.is_fallbackable());
    assert_eq!(sleeps.load(Ordering::SeqCst), 0);
}

#[tokio::test]
async fn retries_disabled_send_once() {
    let srv = serve(vec![Canned {
        status: 529,
        headers: vec![("retry-after-ms".into(), "1".into())],
        body: r#"{"type":"error","error":{"type":"overloaded_error","message":"x"}}"#.into(),
        hang: false,
    }])
    .await;
    let a = Adapter::builder(cfg(&srv.url, "k"))
        .env(|_| None)
        .max_retries(0)
        .build()
        .unwrap();
    assert!(a.complete(&hi()).await.is_err());
    assert_eq!(srv.attempts(), 1);
}

#[tokio::test]
async fn retry_waits_what_retry_after_asks() {
    let srv = serve(vec![
        Canned {
            status: 429,
            headers: vec![("retry-after".into(), "1.5".into())],
            body: "{}".into(),
            hang: false,
        },
        Canned::json(
            200,
            r#"{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}"#,
        ),
    ])
    .await;
    let waits: Arc<std::sync::Mutex<Vec<Duration>>> = Arc::default();
    let w = waits.clone();
    let a = Adapter::builder(cfg(&srv.url, "k"))
        .env(|_| None)
        .sleeper(Arc::new(move |d| {
            w.lock().unwrap().push(d);
            Box::pin(async { true })
        }))
        .build()
        .unwrap();
    assert_eq!(a.complete(&hi()).await.unwrap().content, "ok");
    assert_eq!(*waits.lock().unwrap(), [Duration::from_millis(1500)]);
}

#[tokio::test]
async fn a_cancelled_wait_ends_the_call() {
    let srv = serve(vec![Canned {
        status: 503,
        headers: vec![],
        body: "{}".into(),
        hang: false,
    }])
    .await;
    let a = Adapter::builder(cfg(&srv.url, "k"))
        .env(|_| None)
        .sleeper(Arc::new(|_| Box::pin(async { false })))
        .build()
        .unwrap();
    assert!(matches!(
        a.complete(&hi()).await.unwrap_err(),
        LlmError::Cancelled
    ));
    assert_eq!(srv.attempts(), 1);
}

#[tokio::test]
async fn dropping_a_stream_closes_the_connection() {
    let srv = serve(vec![Canned {
        hang: true,
        ..Canned::sse(START)
    }])
    .await;
    let a = adapter(&srv.url);
    let mut s = a.stream(&hi()).await.unwrap();
    assert_eq!(s.next().await.unwrap().unwrap().content, "Hel");
    drop(s);
    assert!(eventually(|| srv.closed()).await, "connection still open");
}

#[tokio::test]
async fn complete_and_tool_responses_map() {
    let srv = serve(vec![Canned::json(
        200,
        r#"{"id":"m","type":"message","role":"assistant","model":"m","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"text","text":"Hello"},{"type":"text","text":"!"},{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"NYC"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}"#,
    )])
    .await;
    let a = adapter(&srv.url);
    let resp = a.complete(&hi()).await.unwrap();
    assert_eq!(resp.content, "Hello!");
    assert_eq!(resp.finish_reason, "tool_use");
    assert_eq!(
        (
            resp.usage.prompt_tokens,
            resp.usage.completion_tokens,
            resp.usage.total_tokens
        ),
        (10, 5, 15)
    );

    let tr = a
        .call_with_tools(&hi(), &[ToolDef::named("get_weather")])
        .await
        .unwrap();
    assert_eq!(tr.content, "Hello!");
    assert_eq!(tr.tool_calls.len(), 1);
    assert_eq!(tr.tool_calls[0].id, "toolu_1");
    let args: serde_json::Value = serde_json::from_str(&tr.tool_calls[0].arguments).unwrap();
    assert_eq!(args, serde_json::json!({"city": "NYC"}));
    let body = srv.requests()[1].json();
    assert_eq!(body["model"], "claude-opus-5-5");
    assert!(body.get("stream").is_none());
}

#[tokio::test]
async fn base_url_from_config_beats_the_environment() {
    let srv = serve(vec![Canned::json(200, r#"{"content":[],"usage":{}}"#)]).await;
    let env: BTreeMap<String, String> = [(
        "ANTHROPIC_BASE_URL".to_string(),
        "http://127.0.0.1:9".to_string(),
    )]
    .into();
    let a = Adapter::builder(cfg(&srv.url, "k"))
        .env(move |k| env.get(k).cloned())
        .build()
        .unwrap();
    a.complete(&hi()).await.unwrap();
    assert_eq!(srv.attempts(), 1);

    let srv = serve(vec![Canned::json(200, r#"{"content":[],"usage":{}}"#)]).await;
    let env: BTreeMap<String, String> =
        [("ANTHROPIC_BASE_URL".to_string(), format!("{}/", srv.url))].into();
    let a = Adapter::builder(cfg("", "k"))
        .env(move |k| env.get(k).cloned())
        .build()
        .unwrap();
    a.complete(&hi()).await.unwrap();
    assert_eq!(srv.only().path, "/v1/messages");
}

#[test]
fn a_key_is_required() {
    for key in ["", "  "] {
        let err = Adapter::builder(cfg("http://127.0.0.1:9", key))
            .env(|_| None)
            .build()
            .unwrap_err();
        assert_eq!(err.to_string(), "anthropic: API key required");
    }
}

#[test]
fn the_key_never_appears_in_debug() {
    let a = Adapter::builder(cfg("http://127.0.0.1:9", SECRET))
        .env(|_| None)
        .build()
        .unwrap();
    assert!(!format!("{a:?}").contains(SECRET));
}

#[tokio::test]
async fn base_url_credentials_stay_out_of_api_errors() {
    for status in [400u16, 401, 500] {
        let srv = serve(vec![Canned::json(
            status,
            r#"{"type":"error","error":{"type":"invalid_request_error","message":"nope"}}"#,
        )])
        .await;
        let host = srv.url.trim_start_matches("http://");
        // Userinfo gets no credential: refused at build, unechoed.
        let with_user = format!("http://user:{SECRET}@{host}/");
        let refused = Adapter::builder(cfg(&with_user, "k"))
            .env(|_| None)
            .build()
            .unwrap_err()
            .to_string();
        assert!(!refused.contains(SECRET), "{refused}");
        let base = format!("{}/?key={SECRET}", srv.url);
        let a = Adapter::builder(cfg(&base, "k"))
            .env(|_| None)
            .max_retries(0)
            .build()
            .unwrap();
        let msg = a.complete(&hi()).await.unwrap_err().to_string();
        assert!(!msg.contains(SECRET), "{status}: {msg}");
    }
}

#[tokio::test]
async fn transport_errors_stay_redacted() {
    // A listener held for the whole test that closes every connection
    // before answering: a transport failure each time, on a port no
    // other listener can take, as one can a port freed by dropping it.
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = listener.local_addr().unwrap();
    std::thread::spawn(move || {
        for conn in listener.incoming() {
            drop(conn);
        }
    });
    let base = format!("http://{addr}/v1?key={SECRET}");
    let a = Adapter::builder(cfg(&base, "k"))
        .env(|_| None)
        .max_retries(0)
        .build()
        .unwrap();
    let err = a.complete(&hi()).await.unwrap_err();
    assert!(matches!(err, LlmError::Transport { .. }), "{err:?}");
    assert!(err.is_fallbackable());
    let msg = err.to_string();
    assert!(!msg.contains(SECRET), "{msg}");
    assert!(!format!("{err:?}").contains(SECRET));
}

#[test]
fn unparseable_base_url_is_rejected_redacted() {
    for (what, base) in [
        ("userinfo", format!("http://{SECRET}@h\u{7f}/v1")),
        ("query", format!("http://h\u{7f}/v1?key={SECRET}")),
    ] {
        let err = Adapter::builder(cfg(&base, "k"))
            .env(|_| None)
            .build()
            .unwrap_err()
            .to_string();
        assert!(err.contains("anthropic: invalid base URL"), "{what}: {err}");
        assert!(err.contains("REDACTED"), "{what}: {err}");
        assert!(!err.contains(SECRET), "{what}: {err}");

        let b = base.clone();
        let err = Adapter::builder(cfg("", "k"))
            .env(move |k| (k == "ANTHROPIC_BASE_URL").then(|| b.clone()))
            .build()
            .unwrap_err()
            .to_string();
        assert!(
            err.contains("anthropic: invalid ANTHROPIC_BASE_URL"),
            "{what}: {err}"
        );
        assert!(!err.contains(SECRET), "{what}: {err}");
    }
}

#[test]
fn registered_in_the_default_registry() {
    let r = hop_top_kit::llm::default_registry();
    assert!(r.schemes().iter().any(|s| s == "anthropic"));
    let err = r.resolve("anthropic://claude-opus-5-5").err().unwrap();
    assert_eq!(err.to_string(), "anthropic: API key required");
    assert!(r.resolve("anthropic://claude-opus-5-5?api_key=k").is_ok());
}

/// A server's wait is honored only within (0, 60s]; anything else falls
/// back to backoff (at most 500ms before the second attempt).
#[tokio::test]
async fn retry_after_outside_bounds_falls_back_to_backoff() {
    let cases: &[(&str, &str, Option<Duration>)] = &[
        ("retry-after", "1.5", Some(Duration::from_millis(1500))),
        ("retry-after", "60", Some(Duration::from_secs(60))),
        ("retry-after-ms", "250", Some(Duration::from_millis(250))),
        ("retry-after", "0", None),
        ("retry-after", "-5", None),
        ("retry-after", "NaN", None),
        ("retry-after", "inf", None),
        ("retry-after", "soon", None),
        ("retry-after", "61", None),
        ("retry-after", "999999999999", None),
        ("retry-after-ms", "60001", None),
    ];
    for (header, value, want) in cases {
        let srv = serve(vec![
            Canned {
                status: 429,
                headers: vec![(header.to_string(), value.to_string())],
                body: "{}".into(),
                hang: false,
            },
            Canned::json(200, r#"{"content":[],"usage":{}}"#),
        ])
        .await;
        let waits: Arc<std::sync::Mutex<Vec<Duration>>> = Arc::default();
        let w = waits.clone();
        let a = Adapter::builder(cfg(&srv.url, "k"))
            .env(|_| None)
            .sleeper(Arc::new(move |d| {
                w.lock().unwrap().push(d);
                Box::pin(async { true })
            }))
            .build()
            .unwrap();
        a.complete(&hi()).await.unwrap();
        let got = waits.lock().unwrap().clone();
        assert_eq!(got.len(), 1, "{header}: {value}");
        match want {
            Some(d) => assert_eq!(got[0], *d, "{header}: {value}"),
            None => assert!(
                got[0] <= Duration::from_millis(500),
                "{header}: {value}: waited {:?}",
                got[0]
            ),
        }
    }
}

#[test]
fn a_key_never_goes_over_plain_http_to_a_remote_host() {
    let build = |base: &str, env_base: Option<&str>| {
        let env_base = env_base.map(String::from);
        Adapter::builder(cfg(base, "k"))
            .env(move |k| {
                (k == "ANTHROPIC_BASE_URL")
                    .then(|| env_base.clone())
                    .flatten()
            })
            .build()
    };
    for bad in ["http://gateway.example.test", "HTTP://10.0.0.5:8080"] {
        assert!(
            matches!(
                build(bad, None),
                Err(LlmError::KeyEndpoint {
                    reason: hop_top_kit::llm::KeyEndpointReason::Insecure,
                    ..
                })
            ),
            "{bad}"
        );
        assert!(build("", Some(bad)).is_err(), "env {bad}");
    }
    for ok in [
        "http://127.0.0.1:9",
        "http://localhost:9",
        "http://[::1]:9",
        "https://gateway.example.test",
    ] {
        assert!(build(ok, None).is_ok(), "{ok}");
    }
}

/// What the server receives: a hostile base URL gets the key the caller
/// configured but not the environment's token or workspace ID; a trusted
/// (here loopback) one gets all three.
#[tokio::test]
async fn env_credentials_reach_only_trusted_origins() {
    let srv = serve(vec![Canned::json(200, r#"{"content":[],"usage":{}}"#)]).await;
    let env: BTreeMap<String, String> = [
        ("ANTHROPIC_AUTH_TOKEN".to_string(), "fake-token".to_string()),
        ("ANTHROPIC_WORKSPACE_ID".to_string(), "ws-1".to_string()),
    ]
    .into();
    let e = env.clone();
    let a = Adapter::builder(cfg(&srv.url, "k"))
        .env(move |k| e.get(k).cloned())
        .config_file(hop_top_kit::llm::ConfigFile::default())
        .build()
        .unwrap();
    a.complete(&hi()).await.unwrap();
    let got = srv.only();
    assert_eq!(got.header("authorization"), Some("Bearer fake-token"));
    assert_eq!(got.header("anthropic-workspace-id"), Some("ws-1"));

    let hostile = Adapter::builder(cfg("https://evil.example", "k"))
        .env(move |k| env.get(k).cloned())
        .config_file(hop_top_kit::llm::ConfigFile::default())
        .build()
        .unwrap();
    assert_eq!(hostile.credential_headers(), ["x-api-key"]);
    assert!(!format!("{hostile:?}").contains("fake-token"));
}
