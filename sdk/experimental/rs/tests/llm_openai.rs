//! The OpenAI adapter against a loopback server, ported from Go
//! `ai/llm/openai`. Nothing leaves the machine. Keys are built at
//! runtime, never literal.

#![cfg(feature = "llm-openai")]

mod llm_support;

use std::collections::BTreeMap;

use hop_top_kit::llm::openai::Adapter;
use hop_top_kit::llm::{
    default_registry, LlmError, Media, MediaKind, Message, Part, Provider, ProviderConfig, Request,
    ResolvedConfig, Role, ToolCall, ToolChoice, ToolChoiceMode, ToolDef,
};
use llm_support::{serve, Canned};
use serde_json::{json, Value};

fn fake_key() -> String {
    format!("{}-{}", "test", "key")
}

fn cfg(scheme: &str, base: &str, model: &str, key: &str) -> ResolvedConfig {
    let mut c = ResolvedConfig {
        provider: ProviderConfig {
            api_key: key.into(),
            base_url: base.into(),
            model: model.into(),
            ..ProviderConfig::default()
        },
        ..ResolvedConfig::default()
    };
    c.uri.scheme = scheme.into();
    c.uri.model = model.into();
    c
}

fn adapter(base: &str) -> Adapter {
    Adapter::new(cfg("openai", base, "gpt-4o", &fake_key())).unwrap()
}

fn completion(content: &str, role: &str, finish: &str) -> String {
    json!({
        "id": "chatcmpl-test", "object": "chat.completion", "created": 1700000000,
        "model": "gpt-4o",
        "choices": [{"index": 0, "message": {"role": role, "content": content}, "finish_reason": finish}],
        "usage": {"prompt_tokens": 10, "completion_tokens": 20, "total_tokens": 30},
    })
    .to_string()
}

fn tool_completion(calls: Value) -> String {
    json!({
        "id": "chatcmpl-tools", "object": "chat.completion", "created": 1700000000,
        "model": "gpt-4o",
        "choices": [{"index": 0, "message": {"role": "assistant", "content": "", "tool_calls": calls}, "finish_reason": "tool_calls"}],
        "usage": {"prompt_tokens": 5, "completion_tokens": 15, "total_tokens": 20},
    })
    .to_string()
}

fn chunks(contents: &[&str]) -> String {
    let mut out = String::new();
    for c in contents {
        out.push_str(&format!(
            "data: {}\n\n",
            json!({"id":"chunk","object":"chat.completion.chunk","created":1700000000,"model":"gpt-4o",
                   "choices":[{"index":0,"delta":{"content":c},"finish_reason":null}]})
        ));
    }
    out.push_str("data: {\"id\":\"chunk\",\"object\":\"chat.completion.chunk\",\"created\":1700000000,\"model\":\"gpt-4o\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n");
    out.push_str("data: [DONE]\n\n");
    out
}

fn hi() -> Request {
    Request {
        messages: vec![Message::user("Hi")],
        ..Request::default()
    }
}

async fn ok_server() -> llm_support::Server {
    serve(vec![Canned::json(
        200,
        &completion("ok", "assistant", "stop"),
    )])
    .await
}

#[tokio::test]
async fn complete_happy_path() {
    let srv = serve(vec![Canned::json(
        200,
        &completion("Hello!", "assistant", "stop"),
    )])
    .await;
    let resp = adapter(&srv.url).complete(&hi()).await.unwrap();
    assert_eq!(resp.content, "Hello!");
    assert_eq!(resp.role, Role::Assistant);
    assert_eq!(resp.finish_reason, "stop");
    assert_eq!(
        (
            resp.usage.prompt_tokens,
            resp.usage.completion_tokens,
            resp.usage.total_tokens
        ),
        (10, 20, 30)
    );
    let got = srv.only();
    assert_eq!(got.path, "/chat/completions");
    assert_eq!(
        got.header("authorization"),
        Some(format!("Bearer {}", fake_key()).as_str())
    );
}

#[tokio::test]
async fn temperature_zero_is_sent_and_unset_is_not() {
    let srv = ok_server().await;
    let mut req = hi();
    req.temperature = Some(0.0);
    adapter(&srv.url).complete(&req).await.unwrap();
    adapter(&srv.url).complete(&hi()).await.unwrap();
    let reqs = srv.requests();
    assert_eq!(reqs[0].json()["temperature"], json!(0.0));
    assert!(reqs[1].json().get("temperature").is_none());
}

#[tokio::test]
async fn stream_happy_path() {
    let srv = serve(vec![Canned::sse(&chunks(&["Hel", "lo", "!"]))]).await;
    let mut s = adapter(&srv.url).stream(&hi()).await.unwrap();
    let mut out = String::new();
    while let Some(t) = s.next().await.unwrap() {
        if t.done {
            break;
        }
        out.push_str(&t.content);
    }
    assert_eq!(out, "Hello!");
}

#[tokio::test]
async fn call_with_tools_happy_path() {
    let srv = serve(vec![Canned::json(
        200,
        &tool_completion(json!([{"id":"call_123","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}])),
    )])
    .await;
    let tools = [ToolDef {
        name: "get_weather".into(),
        description: "Get weather for a city".into(),
        parameters: Some(json!({"type":"object","properties":{"city":{"type":"string"}}})),
        ..ToolDef::default()
    }];
    let req = Request {
        messages: vec![Message::user("Weather in NYC?")],
        ..Request::default()
    };
    let resp = adapter(&srv.url)
        .call_with_tools(&req, &tools)
        .await
        .unwrap();
    assert_eq!(resp.tool_calls.len(), 1);
    assert_eq!(resp.tool_calls[0].id, "call_123");
    assert_eq!(resp.tool_calls[0].name, "get_weather");
    assert_eq!(
        serde_json::from_str::<Value>(&resp.tool_calls[0].arguments).unwrap(),
        json!({"city":"NYC"})
    );
    assert_eq!(srv.only().json()["tools"].as_array().unwrap().len(), 1);
}

fn media_message(parts: Vec<Part>) -> Request {
    Request {
        messages: vec![Message {
            role: Role::User,
            parts,
            ..Message::default()
        }],
        ..Request::default()
    }
}

#[tokio::test]
async fn multimodal_image_inline_and_pdf() {
    let srv = ok_server().await;
    let a = adapter(&srv.url);
    let img = Part::Media(Media {
        kind: MediaKind::Image,
        mime_type: "image/jpeg".into(),
        data: vec![0xFF, 0xD8, 0xFF],
    });
    a.complete(&media_message(vec![
        Part::Text("describe this".into()),
        img,
    ]))
    .await
    .unwrap();
    let pdf = Part::Media(Media {
        kind: MediaKind::Image,
        mime_type: "application/pdf".into(),
        data: b"%PDF-1.4 fake".to_vec(),
    });
    a.complete(&media_message(vec![pdf])).await.unwrap();
    let reqs = srv.requests();
    let c0 = &reqs[0].json()["messages"][0]["content"];
    assert_eq!(c0[0], json!({"type":"text","text":"describe this"}));
    assert_eq!(c0[1]["type"], "image_url");
    assert!(c0[1]["image_url"]["url"]
        .as_str()
        .unwrap()
        .starts_with("data:image/jpeg;base64,"));
    let c1 = &reqs[1].json()["messages"][0]["content"];
    assert_eq!(c1[0]["type"], "file");
    assert!(!c1[0]["file"]["file_data"].as_str().unwrap().is_empty());
}

#[tokio::test]
async fn multimodal_rejections() {
    let a = adapter("http://127.0.0.1:9");
    let audio = Part::Media(Media {
        kind: MediaKind::Audio,
        mime_type: "audio/wav".into(),
        data: vec![1],
    });
    assert!(matches!(
        a.complete(&media_message(vec![audio])).await,
        Err(LlmError::UnsupportedModality { .. })
    ));
    let no_mime = Part::Media(Media {
        kind: MediaKind::Image,
        mime_type: String::new(),
        data: vec![1],
    });
    assert!(matches!(
        a.complete(&media_message(vec![no_mime])).await,
        Err(LlmError::InvalidFormat { .. })
    ));
    let mut req = media_message(vec![Part::Text("x".into())]);
    req.messages[0].role = Role::Assistant;
    let e = a.complete(&req).await.unwrap_err();
    assert_eq!(
        e.to_string(),
        r#"openai: message role "assistant" does not support content parts"#
    );
}

#[tokio::test]
async fn blank_key_sends_no_authorization() {
    let cases: [(&str, Option<&str>); 4] = [
        ("", None),
        ("  ", None),
        ("\t", None),
        ("fake-key", Some("Bearer fake-key")),
    ];
    for (key, want) in cases {
        let srv = ok_server().await;
        let a = Adapter::new(cfg("openai", &srv.url, "m", key)).unwrap();
        a.complete(&hi()).await.unwrap();
        assert_eq!(srv.only().header("authorization"), want, "{key:?}");
    }
}

fn tool_round() -> Vec<Message> {
    vec![
        Message::system("sys"),
        Message::user("Weather in NYC and LA?"),
        Message::assistant_calls(
            "Checking.",
            vec![
                ToolCall {
                    id: "call_1".into(),
                    name: "get_weather".into(),
                    arguments: r#"{"city":"NYC"}"#.into(),
                    ..ToolCall::default()
                },
                ToolCall {
                    id: "call_2".into(),
                    name: "get_weather".into(),
                    arguments: r#"{"city":"LA"}"#.into(),
                    ..ToolCall::default()
                },
            ],
        ),
        Message::tool_result("call_1", "sunny"),
        Message::tool_result("call_2", "rainy"),
    ]
}

#[tokio::test]
async fn tool_linkage_wire_shape() {
    let srv = ok_server().await;
    let req = Request {
        messages: tool_round(),
        ..Request::default()
    };
    adapter(&srv.url)
        .call_with_tools(&req, &[ToolDef::named("get_weather")])
        .await
        .unwrap();
    let m = srv.only().json()["messages"].clone();
    assert_eq!(m.as_array().unwrap().len(), 5);
    assert_eq!(m[2]["role"], "assistant");
    assert_eq!(m[2]["content"], "Checking.");
    assert_eq!(m[2]["tool_calls"][1]["id"], "call_2");
    assert_eq!(m[2]["tool_calls"][1]["type"], "function");
    assert_eq!(
        m[2]["tool_calls"][1]["function"]["arguments"],
        r#"{"city":"LA"}"#
    );
    assert_eq!(
        m[3],
        json!({"role":"tool","content":"sunny","tool_call_id":"call_1"})
    );
    assert!(m[0].get("tool_calls").is_none() && m[1].get("tool_call_id").is_none());
}

#[tokio::test]
async fn tool_linkage_empty_content_and_arguments() {
    let srv = ok_server().await;
    let req = Request {
        messages: vec![
            Message::user("hi"),
            Message::assistant_calls(
                "",
                vec![ToolCall {
                    id: "c".into(),
                    name: "f".into(),
                    ..ToolCall::default()
                }],
            ),
            Message::tool_result("c", ""),
        ],
        ..Request::default()
    };
    adapter(&srv.url)
        .call_with_tools(&req, &[ToolDef::named("f")])
        .await
        .unwrap();
    let m = srv.only().json()["messages"].clone();
    assert!(m[1].get("content").is_none());
    assert_eq!(m[1]["tool_calls"][0]["function"]["arguments"], "{}");
}

#[tokio::test]
async fn tool_linkage_rejects() {
    let call = |id: &str, args: &str| ToolCall {
        id: id.into(),
        name: "f".into(),
        arguments: args.into(),
        ..ToolCall::default()
    };
    let cases: Vec<(&str, Message)> = vec![
        (
            "tool result without ToolCallID",
            Message {
                role: Role::Tool,
                content: "x".into(),
                ..Message::default()
            },
        ),
        (
            "tool result with Parts",
            Message {
                role: Role::Tool,
                tool_call_id: "c".into(),
                parts: vec![Part::Text("x".into())],
                ..Message::default()
            },
        ),
        (
            "ToolCalls on user",
            Message {
                role: Role::User,
                tool_calls: vec![call("c", "")],
                ..Message::default()
            },
        ),
        (
            "ToolCallID on user",
            Message {
                role: Role::User,
                content: "x".into(),
                tool_call_id: "c".into(),
                ..Message::default()
            },
        ),
        (
            "tool call without ID",
            Message {
                role: Role::Assistant,
                tool_calls: vec![call("", "")],
                ..Message::default()
            },
        ),
        (
            "tool call with invalid JSON",
            Message {
                role: Role::Assistant,
                tool_calls: vec![call("c", "{")],
                ..Message::default()
            },
        ),
        (
            "assistant ToolCalls in Parts",
            Message {
                role: Role::Assistant,
                tool_calls: vec![call("c", "")],
                parts: vec![Part::Text("x".into())],
                ..Message::default()
            },
        ),
    ];
    let srv = ok_server().await;
    for (name, m) in cases {
        let req = Request {
            messages: vec![m],
            ..Request::default()
        };
        let e = adapter(&srv.url)
            .call_with_tools(&req, &[ToolDef::named("f")])
            .await
            .unwrap_err();
        assert!(e.to_string().starts_with("openai:"), "{name}: {e}");
    }
    assert_eq!(srv.attempts(), 0, "a rejected request is never sent");
}

#[tokio::test]
async fn tool_choice_rejected_before_sending() {
    let srv = ok_server().await;
    let a = adapter(&srv.url);
    let mut req = hi();
    req.tool_choice = Some(ToolChoice {
        mode: ToolChoiceMode::ANY,
        name: String::new(),
    });
    assert_eq!(
        a.complete(&req).await.unwrap_err().to_string(),
        r#"openai: tool_choice "any" needs tools"#
    );
    assert_eq!(
        a.stream(&req).await.err().unwrap().to_string(),
        r#"openai: tool_choice "any" needs tools"#
    );
    req.tool_choice = Some(ToolChoice {
        mode: ToolChoiceMode::TOOL,
        name: "nope".into(),
    });
    assert_eq!(
        a.call_with_tools(&req, &[ToolDef::named("a")])
            .await
            .unwrap_err()
            .to_string(),
        r#"openai: tool_choice names unknown tool "nope""#
    );
    assert_eq!(srv.attempts(), 0);
}

#[tokio::test]
async fn tool_names_sanitized_on_wire_original_back() {
    let long = "a".repeat(65);
    let wire_long = hop_top_kit::llm::OPENAI_TOOL_NAMES.sanitize(&long).unwrap();
    let body = tool_completion(
        json!([{"id":"call_9","type":"function","function":{"name":wire_long,"arguments":"{}"}}]),
    );
    let srv = serve(vec![Canned::json(200, &body)]).await;
    let history = vec![
        Message::user("Hi"),
        Message::assistant_calls(
            "",
            vec![ToolCall {
                id: "call_0".into(),
                name: "pane.open".into(),
                ..ToolCall::default()
            }],
        ),
        Message::tool_result("call_0", "opened"),
    ];
    let defs = vec![ToolDef::named("pane.open"), ToolDef::named(&long)];
    let resp = adapter(&srv.url)
        .call_with_tools(
            &Request {
                messages: history,
                ..Request::default()
            },
            &defs,
        )
        .await
        .unwrap();
    let sent = srv.only().json();
    assert_eq!(sent["tools"][0]["function"]["name"], "pane_open");
    assert_eq!(sent["tools"][1]["function"]["name"], wire_long.as_str());
    assert_eq!(wire_long.len(), 64);
    assert_eq!(
        sent["messages"][1]["tool_calls"][0]["function"]["name"],
        "pane_open"
    );
    assert_eq!(resp.tool_calls[0].name, long);
    assert_eq!(defs[0].name, "pane.open");
}

#[tokio::test]
async fn tool_names_collision_and_strict_fail_before_sending() {
    let srv = ok_server().await;
    let a = adapter(&srv.url);
    let e = a
        .call_with_tools(
            &hi(),
            &[ToolDef::named("pane.open"), ToolDef::named("pane_open")],
        )
        .await
        .unwrap_err();
    assert!(matches!(e, LlmError::ToolName { .. }), "{e}");
    let mut c = cfg("openai", &srv.url, "gpt-4o", "k");
    c.provider.params = BTreeMap::from([("tool_names".to_string(), "strict".to_string())]);
    let strict = Adapter::new(c).unwrap();
    let e = strict
        .call_with_tools(&hi(), &[ToolDef::named("pane.open")])
        .await
        .unwrap_err();
    assert!(matches!(e, LlmError::ToolName { .. }), "{e}");
    let mut c = cfg("openai", &srv.url, "gpt-4o", "k");
    c.provider.params = BTreeMap::from([("tool_names".to_string(), "bogus".to_string())]);
    assert!(Adapter::new(c).is_err());
    assert_eq!(srv.attempts(), 0);
}

#[tokio::test]
async fn hermetic_sends_no_sdk_env_headers() {
    let env = |k: &str| match k {
        "OPENAI_ORG_ID" => Some("org-env".to_string()),
        "OPENAI_PROJECT_ID" => Some("proj-env".to_string()),
        _ => None,
    };
    for hermetic in [false, true] {
        let srv = ok_server().await;
        let mut b = Adapter::builder(cfg("openai", &srv.url, "gpt-4o", &fake_key())).env(env);
        if hermetic {
            b = b.hermetic();
        }
        b.build().unwrap().complete(&hi()).await.unwrap();
        let got = srv.only();
        if hermetic {
            assert_eq!(got.header("openai-organization"), None);
            assert_eq!(got.header("openai-project"), None);
            assert!(got.header("authorization").is_some());
        } else {
            assert_eq!(got.header("openai-organization"), Some("org-env"));
            assert_eq!(got.header("openai-project"), Some("proj-env"));
        }
    }
    // Other schemes never get an OpenAI organization.
    let srv = ok_server().await;
    Adapter::builder(cfg("groq", &srv.url, "m", "k"))
        .env(env)
        .build()
        .unwrap()
        .complete(&hi())
        .await
        .unwrap();
    assert_eq!(srv.only().header("openai-organization"), None);
}

const BASE_URL_SECRET: &str = "fake-base-url-secret-must-not-leak";

#[tokio::test]
async fn base_url_credentials_not_in_api_errors() {
    for status in [400u16, 401] {
        let srv = serve(vec![Canned::json(
            status,
            r#"{"error":{"message":"nope","type":"invalid_request_error"}}"#,
        )])
        .await;
        let base = format!(
            "http://user:{BASE_URL_SECRET}@{}",
            srv.url.trim_start_matches("http://")
        );
        let e = Adapter::new(cfg("openai", &base, "gpt-4o", "k"))
            .unwrap()
            .complete(&hi())
            .await
            .unwrap_err();
        assert!(!e.to_string().contains(BASE_URL_SECRET), "{status}: {e}");
        assert!(
            !format!("{e:?}").contains(BASE_URL_SECRET),
            "{status}: {e:?}"
        );
    }
}

#[tokio::test]
async fn transport_errors_do_not_name_base_url_credentials() {
    let base = format!("http://user:{BASE_URL_SECRET}@127.0.0.1:1");
    let e = Adapter::builder(cfg("openai", &base, "gpt-4o", "k"))
        .max_retries(0)
        .build()
        .unwrap()
        .complete(&hi())
        .await
        .unwrap_err();
    assert!(matches!(e, LlmError::Transport { .. }), "{e}");
    assert!(!e.to_string().contains(BASE_URL_SECRET), "{e}");
}

#[tokio::test]
async fn echoed_key_is_scrubbed() {
    let key = format!("{}-{}", "echoed-key", "must-not-leak");
    let cases = [
        (
            false,
            Canned::json(400, &format!(r#"{{"error":{{"message":"bad key {key}","type":"invalid_request_error"}}}}"#)),
        ),
        (
            true,
            Canned::sse(&format!("data: {{\"error\":{{\"message\":\"bad key {key}\",\"type\":\"server_error\"}}}}\n\n")),
        ),
    ];
    for (stream, canned) in cases {
        let srv = serve(vec![canned]).await;
        let a = Adapter::new(cfg("openai", &srv.url, "gpt-4o", &key)).unwrap();
        let e = if stream {
            let mut s = a.stream(&hi()).await.unwrap();
            loop {
                match s.next().await {
                    Err(e) => break e,
                    Ok(None) => panic!("ended without an error"),
                    Ok(Some(_)) => {}
                }
            }
        } else {
            a.complete(&hi()).await.unwrap_err()
        };
        assert!(!e.to_string().contains(&key), "{e}");
        assert!(!format!("{e:?}").contains(&key), "{e:?}");
        assert!(!format!("{a:?}").contains(&key));
    }
}

#[tokio::test]
async fn host_form_resolves_through_the_registry() {
    let srv = ok_server().await;
    let host = srv.url.trim_start_matches("http://");
    let p = default_registry()
        .resolve(&format!("openai://{host}/my-model"))
        .unwrap();
    p.complete(&hi()).await.unwrap();
    let got = srv.only();
    assert_eq!(got.path, "/v1/chat/completions");
    assert_eq!(got.json()["model"], "my-model");
}

#[test]
fn schemes_are_in_the_default_registry() {
    let schemes = default_registry().schemes();
    for s in hop_top_kit::llm::openai::SCHEMES {
        assert!(schemes.contains(&s.to_string()), "{s}");
    }
}

/// A key cut by the message cap must not survive as a fragment: the
/// stream error text is scrubbed before it is truncated.
#[tokio::test]
async fn stream_error_key_straddling_the_cap_is_scrubbed() {
    let key = format!("{}-{}", "sk", "Q7x9LmN2pR4vT6wZ8bC1dF3gH5jK");
    for pad in [500usize, 504, 508, 511] {
        let msg = format!("{}{key}", "x".repeat(pad));
        let body = format!(
            "data: {}\n\n",
            json!({"error": {"message": msg, "type": "server_error"}})
        );
        let srv = serve(vec![Canned::sse(&body)]).await;
        let a = Adapter::new(cfg("openai", &srv.url, "gpt-4o", &key)).unwrap();
        let mut s = a.stream(&hi()).await.unwrap();
        let e = loop {
            match s.next().await {
                Err(e) => break e,
                Ok(None) => panic!("ended without an error"),
                Ok(Some(_)) => {}
            }
        };
        let text = format!("{e} {e:?}");
        for n in 4..=key.len() {
            for i in 0..=key.len() - n {
                assert!(
                    !text.contains(&key[i..i + n]),
                    "pad {pad}: fragment {:?} leaked",
                    &key[i..i + n]
                );
            }
        }
    }
}
