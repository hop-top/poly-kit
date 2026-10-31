//! The `tool_name_separator` mount option: tool names join the leaf path
//! with it in `tools/list`, resolve back through it on `tools/call` in
//! both eras, and a separator a command name contains fails the mount.

#![cfg(feature = "mcp")]

use hop_top_kit::mcp::safety::SafetyClass;
use hop_top_kit::mcp::{Bridge, CallResult, HttpRequest, Leaf, MountError, MountOptions, Surface};
use serde_json::Value;

const MODERN_META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;
const ELICIT_META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;

fn bridge() -> Bridge {
    Bridge::new()
        .leaf(Leaf::new(&["ping"], "Ping the server", |_| {
            Ok(CallResult::ok("pong\n"))
        }))
        .leaf(Leaf::new(&["widget", "add"], "Add a widget", |_| {
            Ok(CallResult::ok("added\n"))
        }))
        .leaf(
            Leaf::new(&["vault", "burn"], "Burn the vault", |_| {
                Ok(CallResult::ok("burned\n"))
            })
            .with_class(SafetyClass {
                requires_confirmation: true,
                ..SafetyClass::default()
            }),
        )
}

/// `data_sync` and `data sync` read alike under `_`.
fn colliding_bridge() -> Bridge {
    Bridge::new()
        .leaf(Leaf::new(&["data_sync"], "Sync", |_| {
            Ok(CallResult::ok(""))
        }))
        .leaf(Leaf::new(&["data", "sync"], "Sync too", |_| {
            Ok(CallResult::ok(""))
        }))
}

fn with_separator(sep: &str) -> MountOptions {
    MountOptions {
        tool_name_separator: sep.into(),
        ..MountOptions::default()
    }
}

fn json(resp_body: &str) -> Value {
    serde_json::from_str(resp_body).expect("response is JSON")
}

fn listed(surface: &Surface) -> Vec<String> {
    let resp = surface.call(&HttpRequest::post(
        "/mcp",
        r#"{"jsonrpc":"2.0","id":1,"method":"tools/list"}"#,
    ));
    json(resp.body_str())["result"]["tools"]
        .as_array()
        .expect("tools array")
        .iter()
        .map(|t| t["name"].as_str().expect("name").to_owned())
        .collect()
}

fn legacy_call(surface: &Surface, name: &str) -> Value {
    let resp = surface.call(&HttpRequest::post(
        "/mcp",
        format!(r#"{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"{name}"}}}}"#),
    ));
    json(resp.body_str())
}

fn modern_call(surface: &Surface, name: &str, meta: &str) -> Value {
    let req = HttpRequest::post(
        "/mcp",
        format!(
            r#"{{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{{"name":"{name}",{meta}}}}}"#
        ),
    )
    .header("MCP-Protocol-Version", "2026-07-28")
    .header("Mcp-Method", "tools/call")
    .header("Mcp-Name", name);
    json(surface.call(&req).body_str())
}

#[test]
fn the_default_separator_is_a_dot() {
    assert_eq!(MountOptions::default().tool_name_separator, ".");
    let surface = Surface::mount(bridge(), MountOptions::default()).unwrap();
    assert!(listed(&surface).contains(&"widget.add".to_owned()));
}

#[test]
fn a_separator_renames_listed_tools() {
    for sep in ["_", "-", "__"] {
        let surface = Surface::mount(bridge(), with_separator(sep)).unwrap();
        let names = listed(&surface);
        assert!(
            names.contains(&format!("widget{sep}add")),
            "{sep}: {names:?}"
        );
        assert!(names.contains(&"ping".to_owned()));
        assert!(!names.contains(&"widget.add".to_owned()));
    }
}

#[test]
fn a_legacy_call_resolves_through_the_separator() {
    let surface = Surface::mount(bridge(), with_separator("_")).unwrap();
    assert_eq!(
        legacy_call(&surface, "widget_add")["result"]["isError"],
        false
    );
    let dotted = legacy_call(&surface, "widget.add");
    assert!(dotted["error"]["message"]
        .as_str()
        .unwrap()
        .contains("unknown tool: widget.add"));
}

#[test]
fn a_modern_call_resolves_through_the_separator() {
    let surface = Surface::mount(bridge(), with_separator("-")).unwrap();
    let body = modern_call(&surface, "widget-add", MODERN_META);
    assert!(body.get("error").is_none(), "{body}");
    assert_eq!(body["result"]["isError"], false);
}

#[test]
fn the_confirmation_prompt_names_the_tool_under_the_separator() {
    let surface = Surface::mount(
        bridge(),
        MountOptions {
            confirmation_key: Some(b"k".to_vec()),
            ..with_separator("_")
        },
    )
    .unwrap();
    let body = modern_call(&surface, "vault_burn", ELICIT_META);
    assert_eq!(
        body["result"]["inputRequests"]["confirm"]["params"]["message"],
        r#"Approve execution of "vault_burn"?"#
    );
}

#[test]
fn an_empty_separator_is_a_mount_error() {
    let err = Surface::mount(bridge(), with_separator("")).unwrap_err();
    assert_eq!(err, MountError::EmptyToolNameSeparator);
    assert_eq!(err.to_string(), "mcp: tool_name_separator: empty separator");
}

#[test]
fn a_separator_inside_a_command_name_is_a_mount_error() {
    let err = Surface::mount(colliding_bridge(), with_separator("_")).unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"mcp: tool_name_separator: command "data_sync" in "data_sync" contains separator "_""#
    );
}

#[test]
fn a_separator_no_command_name_contains_mounts() {
    assert!(Surface::mount(colliding_bridge(), with_separator("-")).is_ok());
}
