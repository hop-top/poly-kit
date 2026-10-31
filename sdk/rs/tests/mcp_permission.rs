//! The `permission` mount hook: Go's `PermissionFunc` slot on the MCP
//! surface. Asked per `tools/call` for the caller the verifier
//! established, after the destructive ceiling and before the leaf runs,
//! on both eras and over stdio; a refusal is an `isError` result at HTTP
//! 200 (Go's `cmdsurface.ErrPermissionDenied` text) and is audited.

#![cfg(feature = "mcp")]

use std::io::Cursor;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};

use hop_top_kit::mcp::safety::SafetyClass;
use hop_top_kit::mcp::{
    AuditRecord, Bridge, CallResult, HttpRequest, Identity, Leaf, MountOptions, PermissionHook,
    Surface, Verifier,
};
use serde_json::Value;

type Seen = Vec<(Option<Identity>, Vec<String>, String, Value)>;

const ERAS: [&str; 2] = ["legacy", "modern"];

fn bridge(runs: &Arc<AtomicUsize>) -> Bridge {
    let (a, b) = (runs.clone(), runs.clone());
    Bridge::new()
        .leaf(
            Leaf::new(&["widget", "add"], "Add", move |_| {
                a.fetch_add(1, Ordering::SeqCst);
                Ok(CallResult::ok("added\n"))
            })
            .with_class(SafetyClass {
                side_effect: "write".into(),
                permissions: vec!["widgets:write".into()],
                ..SafetyClass::default()
            }),
        )
        .leaf(
            Leaf::new(&["widget", "delete"], "Delete", move |_| {
                b.fetch_add(1, Ordering::SeqCst);
                Ok(CallResult::ok("deleted\n"))
            })
            .with_class(SafetyClass {
                destructive: true,
                ..SafetyClass::default()
            }),
        )
}

fn verifier() -> Verifier {
    Verifier::new(|req: &HttpRequest| {
        req.headers
            .iter()
            .any(|(k, v)| k.eq_ignore_ascii_case("authorization") && v == "Bearer alice")
            .then(|| Identity {
                caller: "alice".into(),
                tenant: "acme".into(),
                scopes: vec!["w".into()],
            })
    })
}

fn call(era: &str, name: &str, args: &str) -> HttpRequest {
    if era == "legacy" {
        return HttpRequest::post(
            "/mcp",
            format!(
                r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"{name}","arguments":{args}}}}}"#
            ),
        );
    }
    HttpRequest::post(
        "/mcp",
        format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"{name}","arguments":{args},"_meta":{{"io.modelcontextprotocol/clientCapabilities":{{}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}}}}"#
        ),
    )
    .header("MCP-Protocol-Version", "2026-07-28")
    .header("Mcp-Method", "tools/call")
    .header("Mcp-Name", name)
}

fn mount(hook: PermissionHook, runs: &Arc<AtomicUsize>) -> (Surface, Arc<Mutex<Vec<AuditRecord>>>) {
    let records: Arc<Mutex<Vec<AuditRecord>>> = Arc::default();
    let sink = records.clone();
    let surface = Surface::mount(
        bridge(runs),
        MountOptions {
            verifier: Some(verifier()),
            permission: Some(hook),
            audit: Some(Arc::new(move |r| sink.lock().unwrap().push(r.clone()))),
            ..MountOptions::default()
        },
    )
    .expect("mount");
    (surface, records)
}

#[test]
fn a_refusal_is_an_error_result_naming_command_surface_and_reason() {
    for era in ERAS {
        let runs = Arc::new(AtomicUsize::new(0));
        let (surface, records) = mount(Arc::new(|_| Err("not for you".into())), &runs);
        let resp = surface.call(&call(era, "widget.add", "{}"));
        assert_eq!(resp.status, 200, "{era}");
        let body = resp.body_str();
        assert!(body.contains(r#""isError":true"#), "{era}: {body}");
        assert!(
            body.contains("cmdsurface: permission denied: widget add on mcp: not for you"),
            "{era}: {body}"
        );
        assert_eq!(runs.load(Ordering::SeqCst), 0, "{era}: leaf must not run");
        let got = records.lock().unwrap();
        assert_eq!(got.len(), 1, "{era}");
        assert_eq!(got[0].error, "cmdsurface: permission denied");
        assert_eq!(got[0].path, ["widget", "add"]);
    }
}

#[test]
fn the_hook_sees_the_verified_caller_leaf_and_arguments() {
    for era in ERAS {
        let runs = Arc::new(AtomicUsize::new(0));
        let seen: Arc<Mutex<Seen>> = Arc::default();
        let s = seen.clone();
        let (surface, _) = mount(
            Arc::new(move |c| {
                s.lock().unwrap().push((
                    c.caller.cloned(),
                    c.leaf.path.clone(),
                    c.leaf.class.side_effect.clone(),
                    Value::Object(c.arguments.clone()),
                ));
                Ok(())
            }),
            &runs,
        );
        let anon = surface.call(&call(era, "widget.add", r#"{"kind":"a"}"#));
        assert!(anon.body_str().contains("added"), "{era}");
        let alice =
            surface.call(&call(era, "widget.add", "{}").header("Authorization", "Bearer alice"));
        assert!(alice.body_str().contains("added"), "{era}");
        let seen = seen.lock().unwrap();
        assert_eq!(seen[0].0, None, "a header no verifier checked is nobody");
        assert_eq!(seen[0].3["kind"], "a");
        assert_eq!(seen[1].0.as_ref().unwrap().caller, "alice");
        assert_eq!(seen[1].1, ["widget", "add"]);
        assert_eq!(seen[1].2, "write");
    }
}

#[test]
fn the_destructive_ceiling_answers_before_the_hook_is_asked() {
    for era in ERAS {
        let runs = Arc::new(AtomicUsize::new(0));
        let asked = Arc::new(AtomicUsize::new(0));
        let a = asked.clone();
        let (surface, _) = mount(
            Arc::new(move |_| {
                a.fetch_add(1, Ordering::SeqCst);
                Ok(())
            }),
            &runs,
        );
        let resp = surface.call(&call(era, "widget.delete", "{}"));
        assert!(
            resp.body_str().contains("destructive command blocked"),
            "{era}"
        );
        // An unknown tool is not a permission question either.
        let _ = surface.call(&call(era, "nope", "{}"));
        assert_eq!(asked.load(Ordering::SeqCst), 0, "{era}");
    }
}

#[test]
fn stdio_asks_the_hook_with_no_caller() {
    let runs = Arc::new(AtomicUsize::new(0));
    let (surface, records) = mount(
        Arc::new(|c| match c.caller {
            None => Err("unestablished".into()),
            Some(_) => Ok(()),
        }),
        &runs,
    );
    let input = r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"widget.add","arguments":{}}}"#
        .to_string()
        + "\n";
    let mut out = Vec::new();
    surface
        .serve_stdio(Cursor::new(input.into_bytes()), &mut out, std::io::sink())
        .unwrap();
    let text = String::from_utf8(out).unwrap();
    assert!(
        text.contains("permission denied: widget add on mcp: unestablished"),
        "{text}"
    );
    assert_eq!(runs.load(Ordering::SeqCst), 0);
    assert_eq!(records.lock().unwrap().len(), 1);
}

#[cfg(feature = "cli-permissions")]
mod served {
    use super::*;
    use std::collections::BTreeMap;
    use std::time::Duration;

    use hop_top_kit::cli::policy::{CallerRule, PermissionRule, Policy};
    use hop_top_kit::cli::ServedGate;
    use hop_top_kit::mcp::permission::served_hook;

    /// Refuses writes to everyone but alice, who gets two an hour; rules
    /// close `kind: prod`.
    fn policy() -> Policy {
        Policy {
            name: "team".into(),
            allow: BTreeMap::from([("write".into(), Vec::new())]),
            callers: vec![CallerRule {
                principal: "alice".into(),
                allow: BTreeMap::from([("write".into(), vec!["*".into()])]),
                max_ops: 2,
                window: Duration::from_secs(3600),
                ..CallerRule::default()
            }],
            permissions: vec![PermissionRule {
                name: "no-prod".into(),
                when: "has(payload.flags.kind) && payload.flags.kind == 'prod' && context.surface == 'mcp'".into(),
                effect: "deny".into(),
                otherwise: "allow".into(),
                message: "prod is closed".into(),
            }],
            ..Policy::default()
        }
    }

    fn mounted(runs: &Arc<AtomicUsize>) -> Surface {
        let gate = Arc::new(ServedGate::new(policy()).unwrap());
        mount(served_hook(gate), runs).0
    }

    fn as_alice(era: &str, args: &str) -> HttpRequest {
        call(era, "widget.add", args).header("Authorization", "Bearer alice")
    }

    #[test]
    fn callers_rules_decide_by_the_verified_caller() {
        for era in ERAS {
            let runs = Arc::new(AtomicUsize::new(0));
            let surface = mounted(&runs);
            assert!(
                surface
                    .call(&as_alice(era, "{}"))
                    .body_str()
                    .contains("added"),
                "{era}"
            );
            let anon = surface.call(&call(era, "widget.add", "{}"));
            assert!(
                anon.body_str().contains("permission denied: widget add on mcp: policy: write not allowed for widget add"),
                "{era}: {}",
                anon.body_str()
            );
            assert_eq!(runs.load(Ordering::SeqCst), 1, "{era}");
        }
    }

    #[test]
    fn the_budget_is_charged_once_per_admitted_call_and_refused_when_spent() {
        for era in ERAS {
            let runs = Arc::new(AtomicUsize::new(0));
            let surface = mounted(&runs);
            // A rules refusal spends nothing...
            let closed = surface.call(&as_alice(era, r#"{"kind":"prod"}"#));
            assert!(
                closed
                    .body_str()
                    .contains(r#"permission rule \"no-prod\": prod is closed"#),
                "{era}: {}",
                closed.body_str()
            );
            // ...so both calls of the budget remain.
            for _ in 0..2 {
                assert!(
                    surface
                        .call(&as_alice(era, "{}"))
                        .body_str()
                        .contains("added"),
                    "{era}"
                );
            }
            let spent = surface.call(&as_alice(era, "{}"));
            assert!(
                spent
                    .body_str()
                    .contains("policy: max_ops budget of 2 per 1h0m0s spent; resets at "),
                "{era}: {}",
                spent.body_str()
            );
            assert_eq!(runs.load(Ordering::SeqCst), 2, "{era}");
        }
    }
}

#[test]
fn a_spelling_of_the_tool_name_never_reaches_a_leaf_the_hook_denies() {
    for era in ERAS {
        let runs = Arc::new(AtomicUsize::new(0));
        let asked: Arc<Mutex<Vec<Vec<String>>>> = Arc::default();
        let a = asked.clone();
        let (surface, _) = mount(
            Arc::new(move |c| {
                a.lock().unwrap().push(c.leaf.path.clone());
                Err("closed".into())
            }),
            &runs,
        );
        for name in [
            "widget add",
            "widget. add",
            "widget.add ",
            " widget.add",
            "widget..add",
            "Widget.add",
            "widget.add\u{0}",
        ] {
            let resp = surface.call(&call(era, name, "{}"));
            assert!(
                !resp.body_str().contains("added"),
                "{era} {name:?}: {}",
                resp.body_str()
            );
        }
        // Whatever resolved was asked about as the leaf's registered path.
        for p in asked.lock().unwrap().iter() {
            assert_eq!(p, &["widget", "add"], "{era}");
        }
        assert_eq!(runs.load(Ordering::SeqCst), 0, "{era}");
    }
}
