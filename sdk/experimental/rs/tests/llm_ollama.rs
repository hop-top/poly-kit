//! The Ollama adapter against a loopback server, ported from Go
//! `ai/llm/ollama`. Nothing leaves the machine.

#![cfg(feature = "llm-ollama")]

mod llm_support;

use hop_top_kit::llm::ollama::Adapter;
use hop_top_kit::llm::{
    default_registry, Capability, LlmError, Media, MediaKind, Message, Part, Provider,
    ProviderConfig, Request, ResolvedConfig, Role, ToolCall, ToolChoice, ToolChoiceMode,
};
use llm_support::{serve, Canned, Server};
use serde_json::json;

fn adapter(base: &str, key: &str) -> Adapter {
    Adapter::new(ResolvedConfig {
        provider: ProviderConfig {
            api_key: key.into(),
            base_url: base.into(),
            model: "llama3".into(),
            ..ProviderConfig::default()
        },
        ..ResolvedConfig::default()
    })
    .unwrap()
}

fn chat(content: &str) -> String {
    json!({"message": {"role": "assistant", "content": content}, "done": true}).to_string()
}

async fn ok() -> Server {
    serve(vec![Canned::json(200, &chat("ok"))]).await
}

fn hi() -> Request {
    Request {
        messages: vec![Message::user("Hi")],
        ..Request::default()
    }
}

#[tokio::test]
async fn complete_happy_path() {
    let srv = serve(vec![Canned::json(
        200,
        r#"{"message":{"role":"assistant","content":"Hello! How can I help?"},"done":true,"eval_count":15,"prompt_eval_count":8}"#,
    )])
    .await;
    let resp = adapter(&srv.url, "").complete(&hi()).await.unwrap();
    assert_eq!(resp.content, "Hello! How can I help?");
    assert_eq!(resp.role, Role::Assistant);
    assert_eq!(
        (resp.usage.prompt_tokens, resp.usage.completion_tokens, resp.usage.total_tokens),
        (8, 15, 23)
    );
    assert_eq!(resp.finish_reason, "stop");
    let got = srv.only();
    assert_eq!(got.path, "/api/chat");
    assert_eq!(got.header("content-type"), Some("application/json"));
    assert_eq!(got.header("authorization"), None);
    let b = got.json();
    assert_eq!(b["model"], "llama3");
    assert_eq!(b["stream"], false);
}

#[tokio::test]
async fn temperature_options_and_model_override() {
    let srv = ok().await;
    let a = adapter(&srv.url, "");
    let mut req = hi();
    req.temperature = Some(0.7);
    a.complete(&req).await.unwrap();
    req.temperature = Some(0.0);
    req.model = "other".into();
    a.complete(&req).await.unwrap();
    a.complete(&hi()).await.unwrap();
    let r = srv.requests();
    assert_eq!(r[0].json()["options"]["temperature"], json!(0.7));
    assert_eq!(r[1].json()["options"]["temperature"], json!(0.0));
    assert_eq!(r[1].json()["model"], "other");
    assert!(r[2].json().get("options").is_none());
}

#[tokio::test]
async fn stream_happy_path() {
    let body = [
        json!({"message":{"role":"assistant","content":"Hello"},"done":false}),
        json!({"message":{"role":"assistant","content":" world"},"done":false}),
        json!({"message":{"role":"assistant","content":""},"done":true,"total_duration":1000000,"eval_count":5}),
    ]
    .iter()
    .map(|v| format!("{v}\n"))
    .collect::<String>();
    let srv = serve(vec![Canned::json(200, &body)]).await;
    let mut s = adapter(&srv.url, "").stream(&hi()).await.unwrap();
    let t = s.next().await.unwrap().unwrap();
    assert_eq!((t.content.as_str(), t.done), ("Hello", false));
    let t = s.next().await.unwrap().unwrap();
    assert_eq!((t.content.as_str(), t.done), (" world", false));
    assert!(s.next().await.unwrap().unwrap().done);
    assert!(matches!(s.next().await, Ok(None)));
    assert_eq!(srv.only().json()["stream"], true);
    assert_eq!(s.outcome().unwrap().content, "Hello world");
}

#[tokio::test]
async fn stream_without_done_ends_normally() {
    let srv = serve(vec![Canned::json(
        200,
        "{\"message\":{\"content\":\"a\"},\"done\":false}\n\n{\"message\":{\"content\":\"b\"},\"done\":false}",
    )])
    .await;
    let mut s = adapter(&srv.url, "").stream(&hi()).await.unwrap();
    assert_eq!(s.next().await.unwrap().unwrap().content, "a");
    assert_eq!(s.next().await.unwrap().unwrap().content, "b");
    assert!(s.next().await.unwrap().unwrap().done);
    assert!(matches!(s.next().await, Ok(None)));
}

#[tokio::test]
async fn bad_stream_chunk_is_a_protocol_error() {
    let srv = serve(vec![Canned::json(200, "not json\n")]).await;
    let mut s = adapter(&srv.url, "").stream(&hi()).await.unwrap();
    assert!(matches!(s.next().await, Err(LlmError::Protocol(_))));
    assert!(matches!(s.next().await, Ok(None)));
}

#[tokio::test]
async fn errors_map_to_the_taxonomy() {
    let cases: [(u16, &str, &str); 7] = [
        (404, r#"{"error":"model \"nope\" not found"}"#, "model"),
        (400, r#"{"error":"model does not exist"}"#, "model"),
        (404, "", "model"),
        (401, "", "auth"),
        (403, "", "permission_denied"),
        (429, "", "rate_limit"),
        (500, "boom", "http_status"),
    ];
    for (status, body, want) in cases {
        let srv = serve(vec![Canned::json(status, body)]).await;
        let a = adapter(&srv.url, "");
        let e = a.complete(&hi()).await.unwrap_err();
        let kind = match &e {
            LlmError::Model { .. } => "model",
            LlmError::Auth { .. } => "auth",
            LlmError::PermissionDenied { .. } => {
                assert!(e.is_auth());
                "permission_denied"
            }
            LlmError::RateLimit { .. } => "rate_limit",
            LlmError::HttpStatus { .. } => "http_status",
            other => panic!("{status}: {other}"),
        };
        assert_eq!(kind, want, "{status} {body}");
        assert_eq!(srv.attempts(), 1, "{status}: Ollama is not retried");
        // The stream path maps the same.
        let e = a.stream(&hi()).await.err().unwrap();
        assert_eq!(std::mem::discriminant(&e), std::mem::discriminant(&a.complete(&hi()).await.unwrap_err()));
    }
    let srv = serve(vec![Canned::json(418, "teapot")]).await;
    let e = adapter(&srv.url, "").complete(&hi()).await.unwrap_err();
    assert!(matches!(e, LlmError::Api { status: 418, .. }), "{e}");
    assert!(e.to_string().contains("teapot"));
}

#[tokio::test]
async fn connection_refused_is_a_transport_error() {
    let e = adapter("http://127.0.0.1:1", "").complete(&hi()).await.unwrap_err();
    assert!(matches!(e, LlmError::Transport { .. }), "{e}");
    assert!(e.is_fallbackable());
}

#[tokio::test]
async fn key_is_sent_as_bearer_when_set() {
    for (key, want) in [
        ("", None),
        ("   ", None),
        ("fake-ollama-key", Some("Bearer fake-ollama-key")),
    ] {
        let srv = ok().await;
        adapter(&srv.url, key).complete(&hi()).await.unwrap();
        assert_eq!(srv.only().header("authorization"), want, "{key:?}");
    }
}

#[tokio::test]
async fn multimodal_images_are_plain_base64() {
    let srv = ok().await;
    let png = Part::Media(Media {
        kind: MediaKind::Image,
        mime_type: "image/png".into(),
        data: vec![0x89, 0x50, 0x4E, 0x47],
    });
    let req = Request {
        messages: vec![Message {
            role: Role::User,
            parts: vec![Part::Text("describe".into()), png],
            ..Message::default()
        }],
        ..Request::default()
    };
    adapter(&srv.url, "").complete(&req).await.unwrap();
    let m = &srv.only().json()["messages"][0];
    assert_eq!(m["role"], "user");
    assert_eq!(m["content"], "describe");
    let img = m["images"][0].as_str().unwrap();
    assert!(!img.starts_with("data:") && !img.is_empty());
}

#[tokio::test]
async fn text_only_parts_concatenate() {
    let srv = ok().await;
    let req = Request {
        messages: vec![Message {
            role: Role::User,
            parts: vec![Part::Text("hello ".into()), Part::Text("world".into())],
            ..Message::default()
        }],
        ..Request::default()
    };
    adapter(&srv.url, "").complete(&req).await.unwrap();
    let m = &srv.only().json()["messages"][0];
    assert_eq!(m["content"], "hello world");
    assert!(m.get("images").is_none());
}

#[tokio::test]
async fn audio_parts_are_unsupported() {
    let audio = Part::Media(Media {
        kind: MediaKind::Audio,
        mime_type: "audio/wav".into(),
        data: vec![1],
    });
    let req = Request {
        messages: vec![Message {
            parts: vec![audio],
            ..Message::default()
        }],
        ..Request::default()
    };
    let e = adapter("http://127.0.0.1:1", "").complete(&req).await.unwrap_err();
    assert!(matches!(e, LlmError::UnsupportedModality { .. }), "{e}");
}

fn call(id: &str, name: &str, args: &str) -> ToolCall {
    ToolCall {
        id: id.into(),
        name: name.into(),
        arguments: args.into(),
        ..ToolCall::default()
    }
}

#[tokio::test]
async fn tool_linkage_wire_shape() {
    let srv = ok().await;
    let req = Request {
        messages: vec![
            Message::user("Weather in NYC and LA?"),
            Message::assistant_calls(
                "Checking.",
                vec![call("call_1", "get_weather", r#"{"city":"NYC"}"#), call("call_2", "get_weather", "")],
            ),
            Message::tool_result("call_1", "sunny"),
            Message::tool_result("call_2", "rainy"),
            Message::tool_result("", "stray"),
        ],
        ..Request::default()
    };
    adapter(&srv.url, "").complete(&req).await.unwrap();
    let m = srv.only().json()["messages"].clone();
    assert_eq!(m[1]["tool_calls"][0], json!({"id":"call_1","function":{"name":"get_weather","arguments":{"city":"NYC"}}}));
    assert_eq!(m[1]["tool_calls"][1]["function"]["arguments"], json!({}));
    assert_eq!(m[2], json!({"role":"tool","content":"sunny","tool_call_id":"call_1","tool_name":"get_weather"}));
    assert_eq!(m[3]["tool_name"], "get_weather");
    assert_eq!(m[4], json!({"role":"tool","content":"stray"}));
    assert!(m[0].get("tool_calls").is_none() && m[0].get("tool_call_id").is_none());
}

#[tokio::test]
async fn tool_linkage_rejects() {
    let cases: Vec<(&str, Message)> = vec![
        ("tool result with Parts", Message { role: Role::Tool, tool_call_id: "c".into(), parts: vec![Part::Text("x".into())], ..Message::default() }),
        ("ToolCalls on user", Message { role: Role::User, tool_calls: vec![call("c", "f", "")], ..Message::default() }),
        ("ToolCallID on user", Message { role: Role::User, content: "x".into(), tool_call_id: "c".into(), ..Message::default() }),
        ("tool call with invalid JSON", Message { role: Role::Assistant, tool_calls: vec![call("c", "f", "{")], ..Message::default() }),
    ];
    let srv = ok().await;
    for (name, m) in cases {
        let req = Request { messages: vec![m], ..Request::default() };
        let e = adapter(&srv.url, "").complete(&req).await.unwrap_err();
        assert!(e.to_string().starts_with("ollama:"), "{name}: {e}");
    }
    assert_eq!(srv.attempts(), 0);
}

#[tokio::test]
async fn tool_choice_is_rejected_without_tools() {
    let srv = ok().await;
    let a = adapter(&srv.url, "");
    let mut req = hi();
    req.tool_choice = Some(ToolChoice { mode: ToolChoiceMode::ANY, name: String::new() });
    let want = r#"ollama: tool_choice "any" needs tools"#;
    assert_eq!(a.complete(&req).await.unwrap_err().to_string(), want);
    assert_eq!(a.stream(&req).await.err().unwrap().to_string(), want);
    assert_eq!(srv.attempts(), 0);
}

#[tokio::test]
async fn base_url_credentials_never_surface() {
    const SECRET: &str = "fake-base-url-secret-must-not-leak";
    for (name, base) in [
        ("userinfo token", format!("http://{SECRET}@127.0.0.1:1")),
        ("userinfo pass", format!("http://user:{SECRET}@127.0.0.1:1")),
        ("query key", format!("http://127.0.0.1:1?key={SECRET}")),
    ] {
        let a = adapter(&base, "");
        let e = a.complete(&hi()).await.unwrap_err();
        assert!(!e.to_string().contains(SECRET), "{name}: {e}");
        let e = a.stream(&hi()).await.err().unwrap();
        assert!(!e.to_string().contains(SECRET), "{name}: {e}");
    }
    let r = Adapter::new(ResolvedConfig {
        provider: ProviderConfig {
            base_url: format!("http://h\x7f?key={SECRET}"),
            model: "m".into(),
            ..ProviderConfig::default()
        },
        ..ResolvedConfig::default()
    });
    let e = r.unwrap_err().to_string();
    assert!(e.contains("REDACTED") && !e.contains(SECRET), "{e}");
}

#[tokio::test]
async fn default_registry_resolves_ollama() {
    let srv = ok().await;
    let host = srv.url.trim_start_matches("http://");
    let p = default_registry().resolve(&format!("ollama://{host}/llama3")).unwrap();
    assert_eq!(p.capabilities(), &[Capability::Complete, Capability::Stream]);
    p.complete(&hi()).await.unwrap();
    assert_eq!(srv.only().path, "/api/chat");
    assert!(default_registry().schemes().contains(&"ollama".to_string()));
}
