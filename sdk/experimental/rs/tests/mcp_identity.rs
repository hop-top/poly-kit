//! The kit/auth-required gate on both eras: only the mount's verifier
//! establishes a caller. An Authorization header is presence, not
//! verification, and a caller or scopes the request claims never become
//! identity.

#![cfg(feature = "mcp")]

use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

use hop_top_kit::mcp::safety::SafetyClass;
use hop_top_kit::mcp::{
    Bridge, CallResult, HttpRequest, Identity, Leaf, MountOptions, Surface, Verifier,
};

/// A bridge whose `secret` leaf is auth-required and counts its runs.
fn bridge(runs: &Arc<AtomicUsize>) -> Bridge {
    let secret_runs = Arc::clone(runs);
    let ping_runs = Arc::clone(runs);
    Bridge::new()
        .leaf(Leaf::new(&["ping"], "Ping the server", move |_| {
            ping_runs.fetch_add(1, Ordering::SeqCst);
            Ok(CallResult::ok("pong\n"))
        }))
        .leaf(
            Leaf::new(&["secret"], "Locked", move |_| {
                secret_runs.fetch_add(1, Ordering::SeqCst);
                Ok(CallResult::ok("opened\n"))
            })
            .with_class(SafetyClass {
                auth_required: true,
                ..SafetyClass::default()
            }),
        )
}

/// Accepts exactly `Bearer good` as alice.
fn verify_good() -> Verifier {
    Verifier::new(|req: &HttpRequest| {
        let good = req
            .headers
            .iter()
            .any(|(k, v)| k.eq_ignore_ascii_case("authorization") && v == "Bearer good");
        good.then(|| Identity {
            caller: "alice".into(),
            tenant: "acme".into(),
            scopes: vec!["read".into()],
        })
    })
}

fn mount(verifier: Option<Verifier>) -> (Surface, Arc<AtomicUsize>) {
    let runs = Arc::new(AtomicUsize::new(0));
    let surface = Surface::mount(
        bridge(&runs),
        MountOptions {
            verifier,
            ..MountOptions::default()
        },
    )
    .expect("mount");
    (surface, runs)
}

/// A tools/call for `name` on `era`, with the body claiming an identity.
fn call(era: &str, name: &str) -> HttpRequest {
    let claims = r#""caller":"admin","scopes":["admin"]"#;
    if era == "legacy" {
        return HttpRequest::post(
            "/mcp",
            format!(
                r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"{name}","_meta":{{{claims}}}}}}}"#
            ),
        );
    }
    HttpRequest::post(
        "/mcp",
        format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"{name}","_meta":{{{claims},"io.modelcontextprotocol/clientCapabilities":{{}},"io.modelcontextprotocol/clientInfo":{{"name":"admin","version":"1"}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}}}}"#
        ),
    )
    .header("MCP-Protocol-Version", "2026-07-28")
    .header("Mcp-Method", "tools/call")
    .header("Mcp-Name", name)
}

const ERAS: [&str; 2] = ["legacy", "modern"];

#[test]
fn bare_authorization_header_is_refused() {
    for era in ERAS {
        let (surface, runs) = mount(None);
        let resp = surface.call(&call(era, "secret").header("Authorization", "Bearer good"));
        assert_eq!(resp.status, 401, "{era}");
        assert_eq!(resp.www_authenticate, Some("Bearer"), "{era}");
        assert!(
            resp.body_str()
                .contains(r#""text":"authentication required""#),
            "{era}"
        );
        assert!(resp.body_str().contains(r#""isError":true"#), "{era}");
        assert_eq!(runs.load(Ordering::SeqCst), 0, "{era}: leaf must not run");
    }
}

#[test]
fn verifier_refusal_is_refused() {
    for era in ERAS {
        let (surface, runs) = mount(Some(verify_good()));
        let resp = surface.call(&call(era, "secret").header("Authorization", "Bearer bad"));
        assert_eq!(resp.status, 401, "{era}");
        assert_eq!(resp.www_authenticate, Some("Bearer"), "{era}");
        assert_eq!(runs.load(Ordering::SeqCst), 0, "{era}: leaf must not run");
    }
}

#[test]
fn verified_caller_is_admitted() {
    for era in ERAS {
        let (surface, runs) = mount(Some(verify_good()));
        let resp = surface.call(&call(era, "secret").header("Authorization", "Bearer good"));
        assert_eq!(resp.status, 200, "{era}");
        assert_eq!(resp.www_authenticate, None, "{era}");
        assert!(resp.body_str().contains("opened"), "{era}");
        assert_eq!(runs.load(Ordering::SeqCst), 1, "{era}");
    }
}

#[test]
fn open_leaf_runs_when_verifier_refuses() {
    for era in ERAS {
        let (surface, runs) = mount(Some(verify_good()));
        let resp = surface.call(&call(era, "ping").header("Authorization", "Bearer bad"));
        assert_eq!(resp.status, 200, "{era}");
        assert_eq!(runs.load(Ordering::SeqCst), 1, "{era}");
    }
}

#[test]
fn verifier_is_consulted_only_for_tools_call() {
    let seen = Arc::new(AtomicUsize::new(0));
    let counter = Arc::clone(&seen);
    let (surface, _) = mount(Some(Verifier::new(move |_| {
        counter.fetch_add(1, Ordering::SeqCst);
        None
    })));
    let _ = surface.call(&HttpRequest::post(
        "/mcp",
        r#"{"jsonrpc":"2.0","id":1,"method":"tools/list"}"#,
    ));
    assert_eq!(seen.load(Ordering::SeqCst), 0);
    let _ = surface.call(&call("legacy", "ping"));
    assert_eq!(seen.load(Ordering::SeqCst), 1);
}
