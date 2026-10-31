//! The command projection's served-call gate: `callers:` rules,
//! `permissions:` rules and `max_ops` budgets on the REST surface. Go is
//! the authority (`go/console/cli/service`, `serve_auth.go`); the
//! refusal is 403 `permission_denied` with the bridge's wording.
#![cfg(all(feature = "api-server", feature = "cli-permissions"))]
#![allow(clippy::result_large_err)]

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use axum::body::Body;
use axum::http::Request;
use hop_top_kit::api_server::commands::CommandProjection;
use hop_top_kit::api_server::Authenticated;
use hop_top_kit::authn::Claims;
use hop_top_kit::cli::policy::{CallerRule, PermissionRule, Policy};
use hop_top_kit::cli::{CommandSpec, Config, FlagSpec, Root, ServedGate};
use serde_json::Value;
use tower::ServiceExt;

type Ran = Arc<Mutex<Vec<String>>>;

fn root(ran: &Ran) -> Root {
    let mut cfg = Config::new("fix", "1.0.0", "Fixture");
    cfg.disable_validate = true;
    let mut root = Root::with_config(cfg);
    for (name, tier) in [("add", "write"), ("list", "read"), ("sweep", "destructive")] {
        let ran = ran.clone();
        root.add_command(
            CommandSpec::new(name)
                .about(name)
                .side_effect(tier)
                .flag(FlagSpec::string("kind"))
                .annotate("kit/permissions", "widgets:write")
                .handler(move |_, out, _| {
                    ran.lock().unwrap().push(name.to_string());
                    write!(out, "ran {name}").unwrap();
                    Ok(())
                }),
        );
    }
    root
}

/// Refuses writes to everyone but alice, who gets `max_ops` an hour.
fn policy(max_ops: i64) -> Policy {
    Policy {
        name: "team".into(),
        allow: BTreeMap::from([("write".into(), Vec::new())]),
        callers: vec![CallerRule {
            principal: "alice".into(),
            allow: BTreeMap::from([("write".into(), vec!["*".into()])]),
            max_ops,
            window: Duration::from_secs(3600),
            ..CallerRule::default()
        }],
        ..Policy::default()
    }
}

fn projection(p: Policy, ran: &Ran) -> CommandProjection {
    CommandProjection::new(Arc::new(root(ran)), "fix", "1.0.0")
        .with_served_gate(Arc::new(ServedGate::new(p).unwrap()))
}

/// Who the stack's authentication established, if anyone.
fn claims(who: Option<(&str, &str)>) -> Option<Claims> {
    who.map(|(sub, tenant)| Claims {
        subject: sub.into(),
        tenant: tenant.into(),
        scopes: vec![],
    })
}

async fn call(
    proj: &CommandProjection,
    who: Option<(&str, &str)>,
    method: &str,
    path: &str,
    body: &str,
) -> (u16, Value) {
    let mut req = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json")
        // A claim in a header is not an identity.
        .header("x-caller", "alice")
        .body(Body::from(body.to_string()))
        .unwrap();
    if let Some(c) = claims(who) {
        req.extensions_mut().insert(c);
        req.extensions_mut().insert(Authenticated);
    }
    let resp = proj.router().oneshot(req).await.unwrap();
    let status = resp.status().as_u16();
    let bytes = axum::body::to_bytes(resp.into_body(), 1 << 20)
        .await
        .unwrap();
    (
        status,
        serde_json::from_slice(&bytes).unwrap_or(Value::Null),
    )
}

const ALICE: Option<(&str, &str)> = Some(("alice", "acme"));

#[tokio::test(flavor = "multi_thread")]
async fn a_caller_rule_widens_for_its_caller_only() {
    let ran: Ran = Arc::default();
    let proj = projection(policy(0), &ran);
    let (s, out) = call(&proj, ALICE, "POST", "/v1/commands/add", "{}").await;
    assert_eq!(s, 200, "{out}");
    for who in [Some(("bob", "acme")), None] {
        let (s, out) = call(&proj, who, "POST", "/v1/commands/add", "{}").await;
        assert_eq!((s, out["code"].as_str()), (403, Some("permission_denied")));
        assert!(
            out["message"].as_str().unwrap().starts_with(
                "cmdsurface: permission denied: add on rest: policy: write not allowed for add"
            ),
            "{out}"
        );
    }
    assert_eq!(*ran.lock().unwrap(), ["add"], "only alice's call ran");
}

#[tokio::test(flavor = "multi_thread")]
async fn a_budget_counts_each_admitted_write_and_refuses_the_next() {
    let ran: Ran = Arc::default();
    let proj = projection(policy(2), &ran);
    for _ in 0..2 {
        assert_eq!(
            call(&proj, ALICE, "POST", "/v1/commands/add", "{}").await.0,
            200
        );
    }
    let (s, out) = call(&proj, ALICE, "POST", "/v1/commands/add", "{}").await;
    assert_eq!((s, out["code"].as_str()), (403, Some("permission_denied")));
    let m = out["message"].as_str().unwrap();
    assert!(
        m.starts_with("cmdsurface: permission denied: add on rest: policy: max_ops budget of 2 per 1h0m0s spent; resets at "),
        "{m}"
    );
    // A read is not a mutating call; another tenant has its own budget.
    assert_eq!(
        call(&proj, ALICE, "GET", "/v1/commands/list", "").await.0,
        200
    );
    let other = Some(("alice", "globex"));
    assert_eq!(
        call(&proj, other, "POST", "/v1/commands/add", "{}").await.0,
        200
    );
    assert_eq!(ran.lock().unwrap().len(), 4);
}

#[tokio::test(flavor = "multi_thread")]
async fn a_rule_reads_the_payload_and_a_refusal_spends_nothing() {
    let ran: Ran = Arc::default();
    let mut p = policy(1);
    p.permissions = vec![PermissionRule {
        name: "no-prod".into(),
        when:
            "has(payload.flags.kind) && payload.flags.kind == 'prod' && context.surface == 'rest'"
                .into(),
        effect: "deny".into(),
        otherwise: "allow".into(),
        message: "prod is closed".into(),
    }];
    let proj = projection(p, &ran);
    let (s, out) = call(
        &proj,
        ALICE,
        "POST",
        "/v1/commands/add",
        r#"{"flags":{"kind":"prod"}}"#,
    )
    .await;
    assert_eq!(s, 403, "{out}");
    assert!(
        out["message"]
            .as_str()
            .unwrap()
            .ends_with(r#"permission rule "no-prod": prod is closed"#),
        "{out}"
    );
    // The refused call spent nothing: the one-call budget is intact.
    let (s, out) = call(
        &proj,
        ALICE,
        "POST",
        "/v1/commands/add",
        r#"{"flags":{"kind":"dev"}}"#,
    )
    .await;
    assert_eq!(s, 200, "{out}");
    assert_eq!(
        call(&proj, ALICE, "POST", "/v1/commands/add", "{}").await.0,
        403
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn rules_see_the_verified_caller() {
    let ran: Ran = Arc::default();
    let p = Policy {
        permissions: vec![PermissionRule {
            name: "tenant".into(),
            when: "principal.established && principal.tenant == 'acme'".into(),
            effect: "allow".into(),
            otherwise: "deny".into(),
            message: "acme only".into(),
        }],
        ..Policy::default()
    };
    let proj = projection(p, &ran);
    assert_eq!(
        call(&proj, ALICE, "POST", "/v1/commands/add", "{}").await.0,
        200
    );
    assert_eq!(
        call(
            &proj,
            Some(("alice", "globex")),
            "POST",
            "/v1/commands/add",
            "{}"
        )
        .await
        .0,
        403
    );
    assert_eq!(
        call(&proj, None, "POST", "/v1/commands/add", "{}").await.0,
        403
    );
    assert_eq!(ran.lock().unwrap().len(), 1);
}

#[tokio::test(flavor = "multi_thread")]
async fn without_a_served_gate_nothing_changes() {
    let ran: Ran = Arc::default();
    let proj = CommandProjection::new(Arc::new(root(&ran)), "fix", "1.0.0");
    assert_eq!(
        call(&proj, None, "POST", "/v1/commands/add", "{}").await.0,
        200
    );
}
