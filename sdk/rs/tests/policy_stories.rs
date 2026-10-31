//! Port of go/runtime/policy/e2e: the rule files under
//! `go/runtime/policy/e2e/testdata` run through a real bus, the state
//! machine and the entity pre-events.
#![cfg(feature = "policy")]

mod policy_support;

use std::sync::Arc;

use hop_top_kit::bus::{Context, DynBus, Event};
use hop_top_kit::domain::{Rules, State, StateMachine};
use hop_top_kit::policy::{
    parse_config, wire, with_context_attrs, Activation, Config, ContextAttrs, Effect, Engine,
    EvalError, Evaluator, Policy, TOPIC_STATE_PRE_TRANSITIONED,
};
use policy_support::*;
use serde_json::json;

fn story(name: &str) -> hop_top_kit::policy::Config {
    load_go(&format!("e2e/testdata/{name}"))
}

fn sm(bus: &DynBus, rules: &[(&str, &[&str])]) -> StateMachine {
    let rules: Rules = rules
        .iter()
        .map(|(f, t)| (State::from(*f), t.iter().map(|s| State::from(*s)).collect()))
        .collect();
    StateMachine::new(rules, Some(Arc::new(BusAdapter(bus.clone()))))
}

fn bus_with(eng: Arc<Engine>) -> DynBus {
    let bus = DynBus::builder().build();
    // The subscriptions live as long as the bus.
    std::mem::forget(wire(&bus, eng));
    bus
}

fn bg() -> Context {
    Context::background()
}

// --- admin-only-cancel ---

#[test]
fn admin_only_cancel_denies_non_admin() {
    let eng = engine_as(
        &story("admin-only-cancel.yaml"),
        principal("alice", "engineer", "ctx"),
    );
    let bus = bus_with(eng);
    let err = sm(&bus, &[("OPEN", &["CANCELED"])])
        .transition(&bg(), "OPEN", "CANCELED", false)
        .unwrap_err();
    let text = err.to_string();
    assert!(text.contains("only admin or orchestrator"), "{text}");
    assert!(
        text.contains(r#"policy "admin-only-cancel" denied"#),
        "{text}"
    );
}

#[test]
fn admin_only_cancel_denial_is_typed_from_decide() {
    let eng = engine_as(
        &story("admin-only-cancel.yaml"),
        principal("alice", "engineer", "ctx"),
    );
    let ev = Event::new(
        "kit.runtime.state.pre_transitioned",
        "domain.statemachine",
        json!({"From": "OPEN", "To": "CANCELED", "Force": false}),
    );
    let err = eng.on_transition(&bg(), &ev).unwrap_err();
    assert_eq!(err.policy_name, "admin-only-cancel");
    assert_eq!(err.topic, "kit.runtime.state.pre_transitioned");
    assert!(err.is_conflict());
}

#[test]
fn admin_only_cancel_allows_admin_and_orchestrator() {
    for role in ["admin", "orchestrator"] {
        let eng = engine_as(
            &story("admin-only-cancel.yaml"),
            principal("u", role, "ctx"),
        );
        let bus = bus_with(eng);
        sm(&bus, &[("OPEN", &["CANCELED"])])
            .transition(&bg(), "OPEN", "CANCELED", false)
            .unwrap_or_else(|e| panic!("{role}: {e}"));
    }
}

// --- delete-requires-note ---

#[test]
fn delete_requires_note() {
    let eng = engine_as(
        &story("delete-requires-note.yaml"),
        principal("alice", "engineer", "ctx"),
    );
    let bus = bus_with(eng);
    let svc = Service::new(&bus);

    let err = svc.delete(&bg(), "task-42").unwrap_err();
    assert!(err.to_string().contains("delete requires --note"), "{err}");
    assert!(
        svc.repo.deleted.lock().unwrap().is_empty(),
        "veto aborts before the repo"
    );

    let ctx = with_context_attrs(
        &bg(),
        ContextAttrs {
            note: "cleanup after duplicate".into(),
            ..Default::default()
        },
    );
    svc.delete(&ctx, "task-42").unwrap();
    assert_eq!(svc.repo.deleted.lock().unwrap().len(), 1);

    // Non-delete ops are unaffected.
    svc.create(&bg(), json!({"ID": "task-1", "Title": "kickoff"}))
        .unwrap();
}

// --- multi-policy-deny-wins ---

fn freeze() -> Context {
    with_context_attrs(
        &bg(),
        ContextAttrs {
            request_attrs: json!({"freeze": true}).as_object().unwrap().clone(),
            ..Default::default()
        },
    )
}

#[test]
fn deny_overrides_compose() {
    let cfg = story("multi-policy-deny-wins.yaml");

    // All allow.
    let bus = bus_with(engine_as(&cfg, principal("alice", "engineer", "ctx")));
    let svc = Service::new(&bus);
    svc.create(&bg(), json!({"ID": "t1", "Title": "kickoff"}))
        .unwrap();
    assert_eq!(svc.repo.created.lock().unwrap().len(), 1);

    // The freeze flag trips the second policy.
    let err = svc
        .create(&freeze(), json!({"ID": "t2", "Title": "kickoff"}))
        .unwrap_err();
    assert!(err.to_string().contains("change freeze window"), "{err}");
    assert_eq!(svc.repo.created.lock().unwrap().len(), 1);

    // Both deny: the first in file order supplies the message.
    let bus = bus_with(engine_as(&cfg, principal("", "", "none")));
    let svc = Service::new(&bus);
    let err = svc
        .create(&freeze(), json!({"ID": "t3", "Title": "kickoff"}))
        .unwrap_err();
    let text = err.to_string();
    assert!(text.contains("authenticated principal"), "{text}");
    assert!(text.contains("writes-need-authenticated-role"), "{text}");
    assert!(svc.repo.created.lock().unwrap().is_empty());
}

// --- writes-need-role ---

#[test]
fn writes_need_authenticated_role() {
    let cfg = story("writes-need-role.yaml");
    let bus = bus_with(engine_as(&cfg, principal("", "", "none")));
    let svc = Service::new(&bus);
    for err in [
        svc.create(&bg(), json!({"ID": "t1", "Title": "x"}))
            .unwrap_err(),
        svc.update(&bg(), json!({"ID": "t1", "Title": "renamed"}))
            .unwrap_err(),
        svc.delete(&bg(), "t1").unwrap_err(),
    ] {
        assert!(err.to_string().contains("authenticated principal"), "{err}");
    }

    let bus = bus_with(engine_as(&cfg, principal("alice", "engineer", "ctx")));
    Service::new(&bus)
        .create(&bg(), json!({"ID": "t1", "Title": "x"}))
        .unwrap();
}

// --- force-not-bypass ---

#[test]
fn force_does_not_bypass_non_admin() {
    let eng = engine_as(
        &story("force-not-bypass.yaml"),
        principal("alice", "engineer", "ctx"),
    );
    let bus = bus_with(eng);
    // No rules at all, and force: the state machine lets it through, so
    // only the policy can stop it.
    let err = sm(&bus, &[])
        .transition(&bg(), "OPEN", "CANCELED", true)
        .unwrap_err();
    assert!(
        err.to_string().contains("only admin or orchestrator"),
        "{err}"
    );
}

#[test]
fn force_with_admin_allowed() {
    let eng = engine_as(
        &story("force-not-bypass.yaml"),
        principal("ops", "admin", "ctx"),
    );
    let bus = bus_with(eng);
    sm(&bus, &[("OPEN", &["CANCELED"])])
        .transition(&bg(), "OPEN", "CANCELED", true)
        .unwrap();
}

// --- unmatched event passes ---

#[test]
fn state_machine_unaffected_by_entity_policy() {
    let eng = engine_as(
        &story("delete-requires-note.yaml"),
        principal("alice", "engineer", "ctx"),
    );
    let bus = bus_with(eng);
    sm(&bus, &[("OPEN", &["DONE"])])
        .transition(&bg(), "OPEN", "DONE", false)
        .unwrap();
}

// --- compile fails loud ---

#[test]
fn compile_fails_loud_bad_cel() {
    let cfg = cfg(r#"policies:
  - name: broken-rule
    on: kit.runtime.entity.pre_validated
    when: 'this is not cel ?? !!'
    effect: allow
    otherwise: deny
    message: "broken"
"#);
    let err = Engine::with_cel(&cfg).err().expect("compile error at boot");
    assert!(err.to_string().contains("broken-rule"), "{err}");
}

/// Go's cel-go type-checks at boot, so `principal == "admin"` (a map
/// against a string) fails to build. cel-rust has no checker: the engine
/// builds and `==` across types is plain false, so the rule resolves to
/// its `otherwise` (here deny) at the first event.
#[test]
fn type_error_is_false_at_evaluation_not_a_boot_failure() {
    let cfg = cfg(r#"policies:
  - name: type-mismatch
    on: kit.runtime.entity.pre_validated
    when: 'principal == "admin"'
    effect: allow
    otherwise: deny
    message: "mismatch"
"#);
    let eng = Engine::with_cel(&cfg).unwrap();
    let mut act = bare_activation();
    act.insert("principal".into(), json!({"role": "admin"}));
    let err = eng
        .decide("kit.runtime.entity.pre_validated", act)
        .unwrap_err();
    assert_eq!(err.message, "mismatch");
}

/// An identifier outside the activation is an evaluation error, which
/// denies whatever the rule's effects are.
#[test]
fn unknown_identifier_denies_at_evaluation() {
    let cfg = cfg(r#"policies:
  - name: typo
    on: kit.runtime.entity.pre_validated
    when: 'princpal.role == "admin"'
    effect: allow
    otherwise: allow
"#);
    let eng = Engine::with_cel(&cfg).unwrap();
    let err = eng
        .decide("kit.runtime.entity.pre_validated", bare_activation())
        .unwrap_err();
    assert!(
        err.message.starts_with("evaluation error:"),
        "{}",
        err.message
    );
}

// --- async rejected ---

#[test]
fn async_subscription_rejected_at_parse_time() {
    let err = parse_config(
        br#"policies:
  - name: would-be-async
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: deny
    async: true
"#,
    )
    .unwrap_err();
    let text = err.to_string();
    assert!(text.contains("async not supported"), "{text}");
    assert!(text.contains("would-be-async"), "names the policy: {text}");
}

// --- swap the evaluator ---

struct Fake(std::collections::HashMap<String, String>);

impl Evaluator for Fake {
    fn compile(&mut self, name: &str, expr: &str) -> Result<(), EvalError> {
        self.0.insert(name.into(), expr.into());
        Ok(())
    }
    fn eval(&self, name: &str, act: &Activation) -> Result<bool, EvalError> {
        let expr = self.0.get(name).map(String::as_str).unwrap_or_default();
        if expr == "always" {
            return Ok(true);
        }
        if let Some(want) = expr.strip_prefix("role-is:") {
            let role = act["principal"]["role"].as_str().unwrap_or_default();
            return Ok(role == want);
        }
        Ok(false)
    }
}

fn fake_cfg() -> Config {
    Config {
        policies: vec![Policy {
            name: "fake-admin-only".into(),
            on: TOPIC_STATE_PRE_TRANSITIONED.into(),
            when: "role-is:admin".into(),
            effect: Effect::Allow,
            otherwise: Effect::Deny,
            message: "fake evaluator: only admin allowed".into(),
        }],
    }
}

#[test]
fn swap_evaluator_to_custom() {
    for (role, ok) in [("engineer", false), ("admin", true)] {
        let eng = Engine::builder()
            .evaluator(Fake(Default::default()))
            .principal_resolver(move |_| principal("alice", role, "ctx"))
            .build(&fake_cfg())
            .unwrap();
        let bus = bus_with(Arc::new(eng));
        let got = sm(&bus, &[("OPEN", &["CANCELED"])]).transition(&bg(), "OPEN", "CANCELED", false);
        match (ok, got) {
            (true, Ok(())) => {}
            (false, Err(e)) => assert!(
                e.to_string().contains("fake evaluator: only admin allowed"),
                "{e}"
            ),
            (ok, got) => panic!("{role}: want ok={ok}, got {got:?}"),
        }
    }
}

// --- the default CEL wiring, minimal ---

#[test]
fn use_cel_default_minimal_wiring() {
    let eng = Arc::new(
        Engine::builder()
            .cel()
            .principal_resolver(|_| principal("alice", "engineer", "ctx"))
            .build(&story("admin-only-cancel.yaml"))
            .unwrap(),
    );
    let bus = DynBus::builder().build();
    let unwire = wire(&bus, eng);
    let machine = sm(&bus, &[("OPEN", &["CANCELED"])]);
    let err = machine
        .transition(&bg(), "OPEN", "CANCELED", false)
        .unwrap_err();
    assert!(
        err.to_string().contains("only admin or orchestrator"),
        "{err}"
    );
    unwire.unwire();
    machine
        .transition(&bg(), "OPEN", "CANCELED", false)
        .unwrap();
}
