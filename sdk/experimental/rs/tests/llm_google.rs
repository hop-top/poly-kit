//! The Google adapter against a loopback server, ported from Go
//! `ai/llm/google`. Nothing leaves the machine. Keys are built at
//! runtime, never literal.

#![cfg(feature = "llm-google")]

mod llm_support;

use std::collections::BTreeMap;

use hop_top_kit::llm::google::Adapter;
use hop_top_kit::llm::{
    default_registry, LlmError, Media, MediaKind, Message, Part, Provider, ProviderConfig,
    ProviderData, Request, ResolvedConfig, Role, ToolCall, ToolChoice, ToolChoiceMode, ToolDef,
};
use llm_support::{serve, Canned, Server};
use serde_json::{json, Value};

fn leak_key() -> String {
    format!("{}-{}", "fake-gemini-key", "must-not-leak")
}

fn cfg(base: &str, model: &str, key: &str) -> ResolvedConfig {
    ResolvedConfig {
        provider: ProviderConfig {
            api_key: key.into(),
            base_url: base.into(),
            model: model.into(),
            ..ProviderConfig::default()
        },
        ..ResolvedConfig::default()
    }
}

fn adapter(base: &str) -> Adapter {
    Adapter::new(cfg(base, "gemini-2.0-flash", &leak_key())).unwrap()
}

fn gemini_response(text: &str, finish: &str, prompt: u64, cand: u64) -> String {
    json!({
        "candidates": [{"content": {"role": "model", "parts": [{"text": text}]}, "finishReason": finish}],
        "usageMetadata": {"promptTokenCount": prompt, "candidatesTokenCount": cand, "totalTokenCount": prompt + cand},
    })
    .to_string()
}

fn sse_chunk(text: &str, finish: &str) -> String {
    let mut c = json!({"candidates": [{"content": {"role": "model", "parts": [{"text": text}]}}]});
    if !finish.is_empty() {
        c["candidates"][0]["finishReason"] = json!(finish);
    }
    format!("data: {c}\n\n")
}

async fn ok() -> Server {
    serve(vec![Canned::json(
        200,
        &gemini_response("done", "STOP", 1, 1),
    )])
    .await
}

fn hi() -> Request {
    Request {
        messages: vec![Message::user("Hi")],
        ..Request::default()
    }
}

fn call(id: &str, name: &str, args: &str) -> ToolCall {
    ToolCall {
        id: id.into(),
        name: name.into(),
        arguments: args.into(),
        ..ToolCall::default()
    }
}

fn sig(s: &str) -> ProviderData {
    ProviderData::from([("google".to_string(), json!({ "thought_signature": s }))])
}

/// Runs one tool-calling round and returns the `contents` sent.
async fn contents(msgs: Vec<Message>) -> Result<Value, LlmError> {
    let srv = ok().await;
    adapter(&srv.url)
        .call_with_tools(
            &Request {
                messages: msgs,
                ..Request::default()
            },
            &[ToolDef::named("get_weather")],
        )
        .await?;
    Ok(srv.only().json()["contents"].clone())
}

#[tokio::test]
async fn complete_happy_path_and_request_shape() {
    let srv = serve(vec![Canned::json(
        200,
        &gemini_response("Hello!", "STOP", 10, 20),
    )])
    .await;
    let mut req = Request {
        messages: vec![
            Message::system("be brief"),
            Message::user("Hi"),
            Message::assistant("hello"),
            Message::user("again"),
        ],
        temperature: Some(0.0),
        max_tokens: 64,
        stop_sequences: vec!["END".into()],
        model: "gemini-x".into(),
        ..Request::default()
    };
    let resp = adapter(&srv.url).complete(&req).await.unwrap();
    assert_eq!(
        (
            resp.content.as_str(),
            resp.role,
            resp.finish_reason.as_str()
        ),
        ("Hello!", Role::Assistant, "stop")
    );
    assert_eq!(
        (
            resp.usage.prompt_tokens,
            resp.usage.completion_tokens,
            resp.usage.total_tokens
        ),
        (10, 20, 30)
    );
    let got = srv.only();
    assert_eq!(got.path, "/models/gemini-x:generateContent");
    assert_eq!(got.header("x-goog-api-key"), Some(leak_key().as_str()));
    let b = got.json();
    assert_eq!(
        b["systemInstruction"],
        json!({"role":"user","parts":[{"text":"be brief"}]})
    );
    assert_eq!(
        b["contents"][1],
        json!({"role":"model","parts":[{"text":"hello"}]})
    );
    assert_eq!(
        b["generationConfig"],
        json!({"temperature":0.0,"maxOutputTokens":64,"stopSequences":["END"]})
    );
    // Nothing set, no generationConfig.
    req = hi();
    let srv = ok().await;
    adapter(&srv.url).complete(&req).await.unwrap();
    assert!(srv.only().json().get("generationConfig").is_none());
}

#[tokio::test]
async fn finish_reasons_map() {
    for (raw, want) in [
        ("MAX_TOKENS", "length"),
        ("SAFETY", "content_filter"),
        ("OTHER", "OTHER"),
    ] {
        let srv = serve(vec![Canned::json(200, &gemini_response("x", raw, 1, 1))]).await;
        assert_eq!(
            adapter(&srv.url)
                .complete(&hi())
                .await
                .unwrap()
                .finish_reason,
            want
        );
    }
}

#[tokio::test]
async fn plain_stream_happy_path() {
    let body = format!(
        "{}{}{}",
        sse_chunk("Hello", ""),
        sse_chunk(" world", ""),
        sse_chunk("", "STOP")
    );
    let srv = serve(vec![Canned::sse(&body)]).await;
    let mut s = adapter(&srv.url).stream(&hi()).await.unwrap();
    assert_eq!(s.next().await.unwrap().unwrap().content, "Hello");
    assert_eq!(s.next().await.unwrap().unwrap().content, " world");
    assert!(s.next().await.unwrap().unwrap().done);
    assert!(matches!(s.next().await, Ok(None)));
    let got = srv.only();
    assert_eq!(
        got.path,
        "/models/gemini-2.0-flash:streamGenerateContent?alt=sse"
    );
}

#[tokio::test]
async fn plain_stream_without_finish_ends_with_done() {
    let srv = serve(vec![Canned::sse(&sse_chunk("partial", ""))]).await;
    let mut s = adapter(&srv.url).stream(&hi()).await.unwrap();
    assert_eq!(s.next().await.unwrap().unwrap().content, "partial");
    assert!(s.next().await.unwrap().unwrap().done);
}

#[tokio::test]
async fn call_with_tools_and_response_ids() {
    let body = json!({"candidates": [{"content": {"role": "model", "parts": [
        {"functionCall": {"id": "fc-1", "name": "a", "args": {}}},
        {"functionCall": {"name": "b", "args": {"x": 1}}},
        {"functionCall": {"name": "b", "args": {}}},
    ]}, "finishReason": "STOP"}]})
    .to_string();
    let srv = serve(vec![Canned::json(200, &body)]).await;
    let resp = adapter(&srv.url)
        .call_with_tools(&hi(), &[ToolDef::named("a"), ToolDef::named("b")])
        .await
        .unwrap();
    assert_eq!(resp.tool_calls.len(), 3);
    assert_eq!(resp.tool_calls[0].id, "fc-1");
    assert!(!resp.tool_calls[1].id.is_empty() && !resp.tool_calls[2].id.is_empty());
    assert_ne!(resp.tool_calls[1].id, resp.tool_calls[2].id);
    assert_eq!(resp.tool_calls[1].arguments, r#"{"x":1}"#);
    let sent = srv.only().json();
    assert_eq!(
        sent["tools"][0]["functionDeclarations"][0],
        json!({"name":"a","description":""})
    );
}

#[tokio::test]
async fn errors_map_by_body_code_then_status() {
    let cases: [(u16, &str, &str); 8] = [
        (
            404,
            r#"{"error":{"code":404,"message":"Model not found","status":"NOT_FOUND"}}"#,
            "model",
        ),
        (
            401,
            r#"{"error":{"code":401,"message":"API key not valid"}}"#,
            "auth",
        ),
        (
            429,
            r#"{"error":{"code":429,"message":"slow"}}"#,
            "rate_limit",
        ),
        (
            500,
            r#"{"error":{"code":500,"message":"internal"}}"#,
            "http_status",
        ),
        (
            403,
            r#"{"error":{"code":403,"message":"denied"}}"#,
            "permission_denied",
        ),
        (403, "nope", "permission_denied"),
        (401, "nope", "auth"),
        (
            400,
            r#"{"error":{"code":400,"message":"bad","status":"INVALID_ARGUMENT"}}"#,
            "api",
        ),
    ];
    for (status, body, want) in cases {
        let srv = serve(vec![Canned::json(status, body)]).await;
        let e = adapter(&srv.url).complete(&hi()).await.unwrap_err();
        let kind = match &e {
            LlmError::Model { model, .. } => {
                assert_eq!(model, "gemini-2.0-flash");
                "model"
            }
            LlmError::PermissionDenied { .. } => {
                assert!(e.is_auth());
                "permission_denied"
            }
            LlmError::Auth { .. } => {
                assert!(!e.is_fallbackable());
                "auth"
            }
            LlmError::RateLimit { .. } => {
                assert!(e.is_fallbackable());
                "rate_limit"
            }
            LlmError::HttpStatus { status, .. } => {
                assert_eq!(*status, 500);
                assert!(e.is_fallbackable());
                "http_status"
            }
            LlmError::Api { .. } => "api",
            other => panic!("{status} {body}: {other}"),
        };
        assert_eq!(kind, want, "{status} {body}");
        assert_eq!(srv.attempts(), 1, "not retried");
    }
    // An error object on a 200 response maps the same.
    let srv = serve(vec![Canned::json(
        200,
        r#"{"error":{"code":429,"message":"quota"}}"#,
    )])
    .await;
    assert!(matches!(
        adapter(&srv.url).complete(&hi()).await,
        Err(LlmError::RateLimit { .. })
    ));
}

#[tokio::test]
async fn stream_403_event_is_permission_denied() {
    let srv = serve(vec![Canned::sse(
        "data: {\"error\":{\"code\":403,\"message\":\"denied\"}}\n\n",
    )])
    .await;
    let mut s = adapter(&srv.url).stream(&hi()).await.unwrap();
    assert!(matches!(
        s.next().await,
        Err(LlmError::PermissionDenied { .. })
    ));
    assert!(matches!(s.next().await, Ok(None)));
}

#[tokio::test]
async fn bad_chunk_and_status_error_on_tool_stream() {
    let srv = serve(vec![Canned::sse("data: not json\n\n")]).await;
    let mut s = adapter(&srv.url)
        .stream_with_tools(&hi(), &[ToolDef::named("f")])
        .await
        .unwrap();
    assert!(matches!(s.next().await, Err(LlmError::Protocol(_))));
    assert!(s.outcome().is_none());
    let srv = serve(vec![Canned::json(401, "nope")]).await;
    let e = adapter(&srv.url)
        .stream_with_tools(&hi(), &[ToolDef::named("f")])
        .await
        .err()
        .unwrap();
    assert!(matches!(e, LlmError::Auth { .. }));
}

#[tokio::test]
async fn multimodal_parts() {
    let srv = ok().await;
    let img = |mime: &str| {
        Part::Media(Media {
            kind: MediaKind::Image,
            mime_type: mime.into(),
            data: vec![0x89, 0x50],
        })
    };
    let req = Request {
        messages: vec![Message {
            role: Role::User,
            parts: vec![Part::Text("what".into()), img("image/png"), img("")],
            ..Message::default()
        }],
        ..Request::default()
    };
    adapter(&srv.url).complete(&req).await.unwrap();
    let parts = srv.only().json()["contents"][0]["parts"].clone();
    assert_eq!(parts[0], json!({"text":"what"}));
    assert_eq!(parts[1]["inlineData"]["mimeType"], "image/png");
    assert!(!parts[1]["inlineData"]["data"].as_str().unwrap().is_empty());
    assert_eq!(
        parts[2]["inlineData"]["mimeType"], "image/png",
        "no mime defaults to png"
    );
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
    assert!(matches!(
        adapter(&srv.url).complete(&req).await,
        Err(LlmError::UnsupportedModality { .. })
    ));
}

fn tool_round() -> Vec<Message> {
    vec![
        Message::system("sys"),
        Message::user("Weather in NYC and LA?"),
        Message::assistant_calls(
            "Checking.",
            vec![
                call("call_1", "get_weather", r#"{"city":"NYC"}"#),
                call("call_2", "get_weather", r#"{"city":"LA"}"#),
            ],
        ),
        Message::tool_result("call_1", "sunny"),
        Message::tool_result("call_2", r#"{"temp":12}"#),
    ]
}

#[tokio::test]
async fn tool_linkage_wire_shape() {
    let got = contents(tool_round()).await.unwrap();
    assert_eq!(
        got.as_array().unwrap().len(),
        3,
        "tool results merge into one turn"
    );
    let model = &got[1]["parts"];
    assert_eq!(model[0]["text"], "Checking.");
    assert_eq!(
        model[1]["functionCall"],
        json!({"name":"get_weather","args":{"city":"NYC"}})
    );
    assert_eq!(got[2]["role"], "user");
    assert_eq!(
        got[2]["parts"][0]["functionResponse"],
        json!({"name":"get_weather","response":{"output":"sunny"}})
    );
    assert_eq!(
        got[2]["parts"][1]["functionResponse"]["response"],
        json!({"temp":12})
    );
}

#[tokio::test]
async fn tool_linkage_empty_arguments_and_plain_messages() {
    let got = contents(vec![
        Message::user("ping"),
        Message::assistant_calls("", vec![call("c1", "ping", "")]),
        Message::tool_result("c1", "pong"),
    ])
    .await
    .unwrap();
    let model = got[1]["parts"].as_array().unwrap();
    assert_eq!(model.len(), 1, "no empty text part beside functionCall");
    assert_eq!(model[0]["functionCall"]["args"], json!({}));
    let got = contents(vec![Message::user("hi"), Message::assistant("hello")])
        .await
        .unwrap();
    assert_eq!(got[1], json!({"role":"model","parts":[{"text":"hello"}]}));
}

#[tokio::test]
async fn tool_linkage_rejects() {
    let c = Message::assistant_calls("", vec![call("c", "f", "")]);
    let cases: Vec<(&str, Vec<Message>)> = vec![
        (
            "tool result without ToolCallID",
            vec![
                c.clone(),
                Message {
                    role: Role::Tool,
                    content: "x".into(),
                    ..Message::default()
                },
            ],
        ),
        (
            "tool result for unknown call",
            vec![c.clone(), Message::tool_result("nope", "x")],
        ),
        (
            "tool result with Parts",
            vec![
                c.clone(),
                Message {
                    role: Role::Tool,
                    tool_call_id: "c".into(),
                    parts: vec![Part::Text("x".into())],
                    ..Message::default()
                },
            ],
        ),
        (
            "ToolCalls on user",
            vec![Message {
                role: Role::User,
                tool_calls: vec![call("c", "f", "")],
                ..Message::default()
            }],
        ),
        (
            "ToolCallID on user",
            vec![Message {
                role: Role::User,
                content: "x".into(),
                tool_call_id: "c".into(),
                ..Message::default()
            }],
        ),
        (
            "invalid JSON",
            vec![Message::assistant_calls("", vec![call("c", "f", "{")])],
        ),
    ];
    for (name, msgs) in cases {
        let e = contents(msgs).await.unwrap_err();
        assert!(e.to_string().starts_with("gemini:"), "{name}: {e}");
    }
}

#[tokio::test]
async fn thought_signature_parsed_into_provider_data_and_echoed() {
    let body = json!({"candidates": [{"content": {"role": "model", "parts": [
        {"functionCall": {"name": "get_weather", "args": {"n": 0}}, "thoughtSignature": "SIG_A"},
        {"functionCall": {"name": "get_weather", "args": {"n": 1}}},
    ]}, "finishReason": "STOP"}]})
    .to_string();
    let srv = serve(vec![Canned::json(200, &body)]).await;
    let resp = adapter(&srv.url)
        .call_with_tools(&hi(), &[ToolDef::named("get_weather")])
        .await
        .unwrap();
    assert_eq!(
        resp.tool_calls[0].provider_data["google"],
        json!({"thought_signature":"SIG_A"})
    );
    assert!(resp.tool_calls[1].provider_data.is_empty());
    // Parallel calls: the signature rides on its own part only.
    let mut calls = resp.tool_calls.clone();
    calls[0].arguments = r#"{"city":"Paris"}"#.into();
    calls[1].arguments = r#"{"city":"London"}"#.into();
    let got = contents(vec![
        Message::user("Paris and London?"),
        Message::assistant_calls("", calls.clone()),
        Message::tool_result(&calls[0].id, "15C"),
        Message::tool_result(&calls[1].id, "12C"),
    ])
    .await
    .unwrap();
    let model = &got[1]["parts"];
    assert_eq!(model[0]["thoughtSignature"], "SIG_A");
    assert!(model[1].get("thoughtSignature").is_none());
}

#[tokio::test]
async fn thought_signature_sequential_steps_and_skip_value() {
    let mut c1 = call("c1", "get_weather", "");
    c1.provider_data = sig("SIG_A");
    let mut c2 = call("c2", "get_weather", "");
    c2.provider_data = sig("SIG_B");
    let got = contents(vec![
        Message::user("Check AA100, book a taxi if delayed."),
        Message::assistant_calls("", vec![c1]),
        Message::tool_result("c1", "delayed"),
        Message::assistant_calls("Booking.", vec![c2]),
        Message::tool_result("c2", "booked"),
    ])
    .await
    .unwrap();
    assert_eq!(got[1]["parts"][0]["thoughtSignature"], "SIG_A");
    assert!(
        got[3]["parts"][0].get("thoughtSignature").is_none(),
        "text part stays unsigned"
    );
    assert_eq!(got[3]["parts"][1]["thoughtSignature"], "SIG_B");

    // No signatures: earlier turns as given, the current turn's first call skips validation.
    let mut other = call("c2", "get_weather", "");
    other.provider_data = ProviderData::from([(
        "anthropic".to_string(),
        json!({"thinking":"not for gemini"}),
    )]);
    let got = contents(vec![
        Message::user("old question"),
        Message::assistant_calls("", vec![call("o1", "get_weather", "")]),
        Message::tool_result("o1", "old"),
        Message::assistant("old answer"),
        Message::user("Paris and London?"),
        Message::assistant_calls("", vec![call("c1", "get_weather", ""), other]),
        Message::tool_result("c1", "15C"),
        Message::tool_result("c2", "12C"),
    ])
    .await
    .unwrap();
    assert!(got[1]["parts"][0].get("thoughtSignature").is_none());
    assert_eq!(
        got[5]["parts"][0]["thoughtSignature"],
        "skip_thought_signature_validator"
    );
    assert!(got[5]["parts"][1].get("thoughtSignature").is_none());
    assert!(!got[5]["parts"][1]["functionCall"]
        .to_string()
        .contains("thinking"));
}

#[tokio::test]
async fn malformed_provider_data_is_an_error() {
    let mut c = call("c1", "get_weather", "");
    c.provider_data = ProviderData::from([("google".to_string(), json!("SIG_A"))]);
    let e = contents(vec![
        Message::user("go"),
        Message::assistant_calls("", vec![c]),
        Message::tool_result("c1", "x"),
    ])
    .await
    .unwrap_err();
    assert!(e.to_string().starts_with("gemini:"), "{e}");
}

#[tokio::test]
async fn tool_choice_wire_shape_and_rejection() {
    let tools = [ToolDef::named("get_weather"), ToolDef::named("9lives")];
    let choice = |mode: ToolChoiceMode, name: &str| {
        Some(ToolChoice {
            mode,
            name: name.into(),
        })
    };
    let cases = [
        (None, None),
        (
            choice(ToolChoiceMode::AUTO, ""),
            Some(json!({"functionCallingConfig":{"mode":"AUTO"}})),
        ),
        (
            choice(ToolChoiceMode::ANY, ""),
            Some(json!({"functionCallingConfig":{"mode":"ANY"}})),
        ),
        (
            choice(ToolChoiceMode::NONE, ""),
            Some(json!({"functionCallingConfig":{"mode":"NONE"}})),
        ),
        (
            choice(ToolChoiceMode::TOOL, "9lives"),
            Some(
                json!({"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["_9lives"]}}),
            ),
        ),
    ];
    for (tc, want) in cases {
        let srv = ok().await;
        let req = Request {
            tool_choice: tc,
            ..hi()
        };
        adapter(&srv.url)
            .call_with_tools(&req, &tools)
            .await
            .unwrap();
        assert_eq!(srv.only().json().get("toolConfig").cloned(), want);
    }
    let srv = ok().await;
    let a = adapter(&srv.url);
    let req = Request {
        tool_choice: choice(ToolChoiceMode::AUTO, ""),
        ..hi()
    };
    let want = r#"gemini: tool_choice "auto" needs tools"#;
    assert_eq!(a.complete(&req).await.unwrap_err().to_string(), want);
    assert_eq!(a.stream(&req).await.err().unwrap().to_string(), want);
    assert_eq!(srv.attempts(), 0);
}

#[tokio::test]
async fn tool_names_keep_dots_sanitize_the_rest() {
    let srv = ok().await;
    let defs = [
        ToolDef::named("pane.open"),
        ToolDef::named("9lives"),
        ToolDef::named("a b"),
    ];
    let hist = vec![
        Message::user("Hi"),
        Message::assistant_calls("", vec![call("fc_0", "pane.open", "")]),
        Message::tool_result("fc_0", "done"),
    ];
    adapter(&srv.url)
        .call_with_tools(
            &Request {
                messages: hist,
                ..Request::default()
            },
            &defs,
        )
        .await
        .unwrap();
    let b = srv.only().json();
    let decls: Vec<&str> = b["tools"][0]["functionDeclarations"]
        .as_array()
        .unwrap()
        .iter()
        .map(|d| d["name"].as_str().unwrap())
        .collect();
    assert_eq!(decls, ["pane.open", "_9lives", "a_b"]);
    assert_eq!(
        b["contents"][1]["parts"][0]["functionCall"]["name"],
        "pane.open"
    );
    assert_eq!(
        b["contents"][2]["parts"][0]["functionResponse"]["name"],
        "pane.open"
    );
    // A collision fails before sending; strict rejects.
    let e = adapter(&srv.url)
        .call_with_tools(&hi(), &[ToolDef::named("a b"), ToolDef::named("a_b")])
        .await
        .unwrap_err();
    assert!(matches!(e, LlmError::ToolName { .. }));
    let mut c = cfg(&srv.url, "m", "k");
    c.provider.params = BTreeMap::from([("tool_names".to_string(), "strict".to_string())]);
    let e = Adapter::new(c)
        .unwrap()
        .call_with_tools(&hi(), &[ToolDef::named("a b")])
        .await
        .unwrap_err();
    assert!(matches!(e, LlmError::ToolName { .. }));
    assert_eq!(srv.attempts(), 1);
}

#[tokio::test]
async fn key_travels_in_a_header_never_the_url() {
    let srv = ok().await;
    let a = adapter(&srv.url);
    a.complete(&hi()).await.unwrap();
    let got = srv.only();
    assert!(!got.path.contains("key="), "{}", got.path);
    assert!(!got.path.contains(&leak_key()));
}

#[tokio::test]
async fn keys_and_base_url_credentials_never_surface() {
    const SECRET: &str = "fake-base-url-secret-must-not-leak";
    let key = leak_key();
    for base in [
        "http://127.0.0.1:1".to_string(),
        format!("http://{SECRET}@127.0.0.1:1"),
        format!("http://user:{SECRET}@127.0.0.1:1"),
        format!("http://127.0.0.1:1?key={SECRET}"),
    ] {
        // A base carrying userinfo is refused at build; the rest fail in
        // transport. Neither may echo a credential.
        let a = match Adapter::new(cfg(&base, "gemini-2.0-flash", &key)) {
            Ok(a) => a,
            Err(e) => {
                let t = format!("{e} {e:?}");
                assert!(!t.contains(&key) && !t.contains(SECRET), "{base}: {t}");
                continue;
            }
        };
        let errs = [
            a.complete(&hi()).await.unwrap_err(),
            a.stream(&hi()).await.err().unwrap(),
            a.call_with_tools(&hi(), &[]).await.unwrap_err(),
        ];
        for e in errs {
            let t = format!("{e} {e:?}");
            assert!(!t.contains(&key) && !t.contains(SECRET), "{base}: {t}");
        }
    }
    let e = Adapter::new(cfg(&format!("http://h\x7f?key={SECRET}"), "m", "k"))
        .unwrap_err()
        .to_string();
    assert!(e.contains("REDACTED") && !e.contains(SECRET), "{e}");
    // An echoed key in an error body is scrubbed.
    let srv = serve(vec![Canned::json(
        400,
        &format!(r#"{{"error":{{"code":400,"message":"bad {key}"}}}}"#),
    )])
    .await;
    let e = adapter(&srv.url).complete(&hi()).await.unwrap_err();
    assert!(!format!("{e} {e:?}").contains(&key));
}

#[tokio::test]
async fn schemes_resolve_through_the_default_registry() {
    for scheme in ["gemini", "google"] {
        let srv = ok().await;
        let host = srv.url.trim_start_matches("http://");
        let uri = format!("{scheme}://{host}/gemini-2.0-flash?api_key={}", leak_key());
        let p = default_registry().resolve(&uri).unwrap();
        p.complete(&hi()).await.unwrap();
        assert_eq!(srv.only().path, "/models/gemini-2.0-flash:generateContent");
    }
}

/// A key echoed in a stream error event, cut by the message cap, must
/// not survive as a fragment on either stream type.
#[tokio::test]
async fn stream_error_key_straddling_the_cap_is_scrubbed() {
    let key = format!("{}-{}", "AIza", "Q7x9LmN2pR4vT6wZ8bC1dF3gH5jK");
    for pad in [500usize, 508] {
        let msg = format!("{}{key}", "x".repeat(pad));
        let body = format!(
            "data: {}\n\n",
            json!({"error": {"code": 400, "message": msg}})
        );
        for with_tools in [false, true] {
            let srv = serve(vec![Canned::sse(&body)]).await;
            let a = Adapter::new(cfg(&srv.url, "m", &key)).unwrap();
            let mut s = if with_tools {
                a.stream_with_tools(&hi(), &[ToolDef::named("f")]).await
            } else {
                a.stream(&hi()).await
            }
            .unwrap();
            let e = s.next().await.unwrap_err();
            let text = format!("{e} {e:?}");
            for n in 4..=key.len() {
                for i in 0..=key.len() - n {
                    assert!(
                        !text.contains(&key[i..i + n]),
                        "pad {pad}: {:?} leaked",
                        &key[i..i + n]
                    );
                }
            }
        }
    }
}

fn env_key(k: &str) -> Option<String> {
    (k == "GOOGLE_API_KEY").then(|| leak_key())
}

fn builder_with_env_key(
    base: &str,
    llm_base: Option<&'static str>,
    yaml: &str,
) -> Result<Adapter, LlmError> {
    Adapter::builder(cfg(base, "m", ""))
        .env(move |k| match k {
            "LLM_BASE_URL" => llm_base.map(str::to_string),
            other => env_key(other),
        })
        .config_file(hop_top_kit::llm::ConfigFile::parse(yaml).unwrap())
        .build()
}

/// A key found in the environment goes only to a trusted endpoint.
#[test]
fn env_fallback_key_is_refused_for_an_untrusted_endpoint() {
    for base in [
        "https://evil.example/v1",
        "http://evil.example",
        "https://generativelanguage.googleapis.com.evil.example/v1beta",
    ] {
        let e = builder_with_env_key(base, None, "").unwrap_err();
        assert!(matches!(e, LlmError::KeyEndpoint { .. }), "{base}: {e}");
        assert!(!e.to_string().contains(&leak_key()));
    }
    // Trusted: the default, loopback, LLM_BASE_URL, llm.yaml.
    assert!(builder_with_env_key("", None, "").is_ok());
    assert!(builder_with_env_key("http://127.0.0.1:9", None, "").is_ok());
    assert!(builder_with_env_key(
        "https://proxy.example/v1",
        Some("https://proxy.example"),
        ""
    )
    .is_ok());
    let yaml = "providers:\n  google:\n    base_url: https://gw.example/v1\n";
    assert!(builder_with_env_key("https://gw.example/v1", None, yaml).is_ok());
    // Hermetic: no env key at all.
    assert!(Adapter::builder(cfg("", "m", ""))
        .env(env_key)
        .hermetic()
        .build()
        .is_err());
}

/// No key goes over plain http to a remote host, or to a malformed base.
#[test]
fn configured_key_is_checked_against_the_endpoint() {
    for base in ["http://remote.example", "https://user:pw@h.example"] {
        let e = Adapter::new(cfg(base, "m", &leak_key())).unwrap_err();
        assert!(matches!(e, LlmError::KeyEndpoint { .. }), "{base}: {e}");
        assert!(!e.to_string().contains("pw@"), "{e}");
    }
    assert!(Adapter::new(cfg("https://anywhere.example", "m", &leak_key())).is_ok());
}

/// The model reaches the path as one encoded segment.
#[tokio::test]
async fn model_is_encoded_into_the_path() {
    for model in ["../../x", "a?key=b", "a#b", "a/b"] {
        let srv = ok().await;
        let mut req = hi();
        req.model = model.into();
        adapter(&srv.url).complete(&req).await.unwrap();
        let got = srv.only().path;
        assert!(
            got.starts_with("/models/") && got.ends_with(":generateContent"),
            "{model}: {got}"
        );
        assert!(!got.contains("..") || got.contains("%2F"), "{model}: {got}");
        assert!(
            !got.contains('?') && !got.contains('#'),
            "{model}: {got}"
        );
        assert_eq!(got.matches('/').count(), 2, "{model}: {got}");
    }
}

/// A non-streamed error body echoing a key cut by the cap leaks no part.
#[tokio::test]
async fn error_body_key_straddling_the_cap_is_scrubbed() {
    let key = format!("{}-{}", "AIza", "Q7x9LmN2pR4vT6wZ8bC1dF3gH5jK");
    for status in [400u16, 200] {
        let msg = format!("{}{key}", "x".repeat(500));
        let body = json!({"error": {"code": 400, "message": msg}}).to_string();
        let srv = serve(vec![Canned::json(status, &body)]).await;
        let a = Adapter::new(cfg(&srv.url, "m", &key)).unwrap();
        let e = a.complete(&hi()).await.unwrap_err();
        let text = format!("{e} {e:?}");
        for n in 4..=key.len() {
            for i in 0..=key.len() - n {
                assert!(
                    !text.contains(&key[i..i + n]),
                    "{status}: {:?} leaked",
                    &key[i..i + n]
                );
            }
        }
    }
}
