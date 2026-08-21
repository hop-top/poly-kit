//! End-to-end surface behaviors the shared wire fixtures do not cover.
//!
//! The fixtures pin the 17 cases Go recorded; this suite covers the rest
//! of the contract that has no fixture — the MRTR confirmation round
//! trip, cacheable-list tuning, the tasks-extension declaration, and the
//! modern audit metadata.

#![cfg(feature = "mcp")]

use hop_top_kit::mcp::safety::SafetyClass;
use hop_top_kit::mcp::{
    request_meta, Bridge, CacheScope, CallResult, HttpRequest, Leaf, MountOptions, Request, Surface,
};
use serde_json::Value;

const MODERN_META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;

/// `_meta` declaring form-mode elicitation support.
const ELICIT_META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;

fn confirm_bridge() -> Bridge {
    Bridge::new().leaf(
        Leaf::new(&["deploy"], "Deploy", |_| Ok(CallResult::ok("deployed\n"))).with_class(
            SafetyClass {
                requires_confirmation: true,
                ..SafetyClass::default()
            },
        ),
    )
}

fn modern_call(body: String) -> HttpRequest {
    HttpRequest::post("/mcp", body)
        .header("MCP-Protocol-Version", "2026-07-28")
        .header("Mcp-Method", "tools/call")
        .header("Mcp-Name", "deploy")
}

fn body(resp_body: &str) -> Value {
    serde_json::from_str(resp_body).expect("response is JSON")
}

#[test]
fn without_a_key_confirmation_falls_back_to_the_header_gate() {
    let surface = Surface::mount(confirm_bridge(), MountOptions::default()).unwrap();
    let req = modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"deploy",{ELICIT_META}}}}}"#
    ));

    // Even a client declaring elicitation gets the header gate when the
    // mount holds no key material.
    let resp = surface.call(&req);
    assert_eq!(resp.status, 428);
    assert!(resp.body_str().contains("confirmation required"));

    let resp = surface.call(&req.clone().header("X-Confirm-Token", "t"));
    assert_eq!(resp.status, 200);
    assert!(resp.body_str().contains("deployed"));
}

#[test]
fn keyed_mount_keeps_the_header_gate_for_clients_without_elicitation() {
    let surface = Surface::mount(
        confirm_bridge(),
        MountOptions {
            confirmation_key: Some(b"shared-key".to_vec()),
            ..MountOptions::default()
        },
    )
    .unwrap();

    // The client declared no elicitation capability, so the spec forbids
    // sending it an inputRequests; the header gate remains.
    let resp = surface.call(&modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"deploy",{MODERN_META}}}}}"#
    )));
    assert_eq!(resp.status, 428);
    assert!(resp.body_str().contains("confirmation required"));
}

#[test]
fn mrtr_round_trip_prompts_then_accepts() {
    let surface = Surface::mount(
        confirm_bridge(),
        MountOptions {
            confirmation_key: Some(b"shared-key".to_vec()),
            ..MountOptions::default()
        },
    )
    .unwrap();

    // First call: an input_required result carrying the elicitation
    // form and a requestState.
    let first = surface.call(&modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"deploy",{ELICIT_META}}}}}"#
    )));
    assert_eq!(first.status, 200);
    let result = body(first.body_str())["result"].clone();
    assert_eq!(result["resultType"], "input_required");
    assert_eq!(
        result["inputRequests"]["confirm"]["method"],
        "elicitation/create"
    );
    assert_eq!(result["inputRequests"]["confirm"]["params"]["mode"], "form");
    // Interim results are never cacheable.
    assert!(result.get("ttlMs").is_none());
    assert!(result.get("cacheScope").is_none());

    let state = result["requestState"].as_str().expect("requestState").to_owned();

    // Retry with an accept: the call proceeds.
    let accepted = surface.call(&modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"deploy","requestState":"{state}","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}}}}}"#
    )));
    assert_eq!(accepted.status, 200);
    let result = body(accepted.body_str())["result"].clone();
    assert_eq!(result["resultType"], "complete");
    assert_eq!(result["isError"], Value::Bool(false));
    assert!(accepted.body_str().contains("deployed"));
}

#[test]
fn mrtr_decline_refuses_the_call() {
    let surface = Surface::mount(
        confirm_bridge(),
        MountOptions {
            confirmation_key: Some(b"shared-key".to_vec()),
            ..MountOptions::default()
        },
    )
    .unwrap();

    let first = surface.call(&modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"deploy",{ELICIT_META}}}}}"#
    )));
    let state = body(first.body_str())["result"]["requestState"]
        .as_str()
        .unwrap()
        .to_owned();

    for action in ["decline", "cancel"] {
        let resp = surface.call(&modern_call(format!(
            r#"{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"deploy","requestState":"{state}","inputResponses":{{"confirm":{{"action":"{action}"}}}},{ELICIT_META}}}}}"#
        )));
        assert_eq!(resp.status, 200);
        let result = body(resp.body_str())["result"].clone();
        assert_eq!(result["isError"], Value::Bool(true));
        assert!(resp.body_str().contains("confirmation declined"));
        assert!(
            !resp.body_str().contains("deployed"),
            "{action} must not run the leaf"
        );
    }
}

#[test]
fn tampered_request_state_is_never_honored() {
    let surface = Surface::mount(
        confirm_bridge(),
        MountOptions {
            confirmation_key: Some(b"shared-key".to_vec()),
            ..MountOptions::default()
        },
    )
    .unwrap();

    // A forged state presented with an accept must re-prompt, never
    // execute the leaf.
    let resp = surface.call(&modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"deploy","requestState":"v1.9999999999.AAAA","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}}}}}"#
    )));
    assert_eq!(resp.status, 200);
    let result = body(resp.body_str())["result"].clone();
    assert_eq!(
        result["resultType"], "input_required",
        "a forged state must re-prompt"
    );
    assert!(
        !resp.body_str().contains("deployed"),
        "a forged state must never run the leaf"
    );
}

#[test]
fn state_minted_for_one_argument_set_does_not_authorize_another() {
    let bridge = Bridge::new().leaf(
        Leaf::new(&["deploy"], "Deploy", |args| {
            Ok(CallResult::ok(format!(
                "deployed {}",
                args.get("env").and_then(Value::as_str).unwrap_or("?")
            )))
        })
        .with_class(SafetyClass {
            requires_confirmation: true,
            ..SafetyClass::default()
        }),
    );
    let surface = Surface::mount(
        bridge,
        MountOptions {
            confirmation_key: Some(b"shared-key".to_vec()),
            ..MountOptions::default()
        },
    )
    .unwrap();

    // Approve a staging deploy...
    let first = surface.call(&modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"deploy","arguments":{{"env":"staging"}},{ELICIT_META}}}}}"#
    )));
    let state = body(first.body_str())["result"]["requestState"]
        .as_str()
        .unwrap()
        .to_owned();

    // ...then present that approval for a production deploy.
    let resp = surface.call(&modern_call(format!(
        r#"{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"deploy","arguments":{{"env":"production"}},"requestState":"{state}","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}}}}}"#
    )));
    let result = body(resp.body_str())["result"].clone();
    assert_eq!(
        result["resultType"], "input_required",
        "approval is bound to the arguments it was granted for"
    );
    assert!(!resp.body_str().contains("deployed production"));
}

#[test]
fn mrtr_never_relaxes_the_destructive_ceiling() {
    // A leaf that is both destructive and confirmation-gated stays
    // blocked even after an accepted confirmation: the policy gate runs
    // inside invoke, after the confirmation gate.
    let bridge = Bridge::new().leaf(
        Leaf::new(&["nuke"], "Nuke", |_| Ok(CallResult::ok("nuked\n"))).with_class(SafetyClass {
            destructive: true,
            requires_confirmation: true,
            ..SafetyClass::default()
        }),
    );
    let surface = Surface::mount(
        bridge,
        MountOptions {
            confirmation_key: Some(b"shared-key".to_vec()),
            ..MountOptions::default()
        },
    )
    .unwrap();

    let call = |b: String| {
        HttpRequest::post("/mcp", b)
            .header("MCP-Protocol-Version", "2026-07-28")
            .header("Mcp-Method", "tools/call")
            .header("Mcp-Name", "nuke")
    };

    let first = surface.call(&call(format!(
        r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"nuke",{ELICIT_META}}}}}"#
    )));
    let state = body(first.body_str())["result"]["requestState"]
        .as_str()
        .unwrap()
        .to_owned();

    let resp = surface.call(&call(format!(
        r#"{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"nuke","requestState":"{state}","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}}}}}"#
    )));
    assert_eq!(resp.status, 200);
    assert!(
        resp.body_str().contains("destructive command blocked"),
        "confirmation must not lift the destructive ceiling"
    );
    assert!(!resp.body_str().contains("nuked"));
}

#[test]
fn cache_hints_are_tunable_on_cacheable_lists_only() {
    let bridge = Bridge::new().leaf(Leaf::new(&["ping"], "Ping", |_| Ok(CallResult::ok("pong\n"))));
    let surface = Surface::mount(
        bridge,
        MountOptions {
            cache_ttl_ms: 30_000,
            cache_scope: CacheScope::Public,
            ..MountOptions::default()
        },
    )
    .unwrap();

    for (method, header) in [("tools/list", "tools/list"), ("server/discover", "server/discover")] {
        let resp = surface.call(
            &HttpRequest::post(
                "/mcp",
                format!(
                    r#"{{"jsonrpc":"2.0","id":1,"method":"{method}","params":{{{MODERN_META}}}}}"#
                ),
            )
            .header("MCP-Protocol-Version", "2026-07-28")
            .header("Mcp-Method", header),
        );
        let result = body(resp.body_str())["result"].clone();
        assert_eq!(result["ttlMs"], 30_000);
        assert_eq!(result["cacheScope"], "public");
    }
}

#[test]
fn tasks_extension_is_declared_only_when_enabled() {
    let discover = |tasks_enabled: bool| {
        let surface = Surface::mount(
            Bridge::new(),
            MountOptions {
                tasks_enabled,
                ..MountOptions::default()
            },
        )
        .unwrap();
        let resp = surface.call(
            &HttpRequest::post(
                "/mcp",
                format!(
                    r#"{{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{{{MODERN_META}}}}}"#
                ),
            )
            .header("MCP-Protocol-Version", "2026-07-28")
            .header("Mcp-Method", "server/discover"),
        );
        body(resp.body_str())["result"].clone()
    };

    // Off by default, so the byte-exact fixture stays green.
    assert!(discover(false).get("extensions").is_none());

    let enabled = discover(true);
    assert!(enabled["extensions"]["io.modelcontextprotocol/tasks"].is_object());
}

#[test]
fn audit_metadata_carries_spec_version_and_client_identity() {
    let req = Request::from_slice(
        br#"{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"probe","version":"1.2.3"}}}}"#,
    )
    .unwrap();
    let extra = request_meta(&req).expect("modern envelope").audit_extra();
    assert_eq!(
        extra,
        vec![
            ("mcp_spec_version", "2026-07-28".to_string()),
            ("mcp_client_name", "probe".to_string()),
            ("mcp_client_version", "1.2.3".to_string()),
        ]
    );

    // Without clientInfo only the spec version is recorded.
    let req = Request::from_slice(
        br#"{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}"#,
    )
    .unwrap();
    assert_eq!(
        request_meta(&req).unwrap().audit_extra(),
        vec![("mcp_spec_version", "2026-07-28".to_string())]
    );
}

#[test]
fn structured_data_appears_as_content_block_and_structured_content() {
    let bridge = Bridge::new().leaf(Leaf::new(&["stat"], "Stat", |_| {
        Ok(CallResult {
            stdout: "ok\n".into(),
            data: Some(serde_json::json!({"b": 2, "a": 1})),
            ..CallResult::default()
        })
    }));
    let surface = Surface::mount(bridge, MountOptions::default()).unwrap();

    let resp = surface.call(
        &HttpRequest::post(
            "/mcp",
            format!(
                r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"stat",{MODERN_META}}}}}"#
            ),
        )
        .header("MCP-Protocol-Version", "2026-07-28")
        .header("Mcp-Method", "tools/call")
        .header("Mcp-Name", "stat"),
    );

    let result = body(resp.body_str())["result"].clone();
    assert_eq!(result["structuredContent"], serde_json::json!({"a": 1, "b": 2}));
    // The JSON text block doubles as the serialized fallback, key-sorted.
    let content = result["content"].as_array().unwrap();
    assert_eq!(content[1]["text"], r#"{"a":1,"b":2}"#);
}
