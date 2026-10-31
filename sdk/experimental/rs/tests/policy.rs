//! Port of go/runtime/policy's policy_test.go: rule parsing, engine
//! construction, deny-overrides decisions, the principal resolver and
//! the bus integrations.
#![cfg(feature = "policy")]

mod policy_support;

use std::sync::Arc;

use hop_top_kit::bus::{Context, DynBus, Event};
use hop_top_kit::domain::{Rules, State, StateMachine, StateMachineError};
use hop_top_kit::policy::{
    parse_config, wire, with_context_attrs, with_principal, ContextAttrs, Decision, Effect, Engine,
    EngineBuilder, PolicyDeniedError,
};
use policy_support::*;
use serde_json::{json, Map};

// --- parse_config: schema validation ---

#[test]
fn parse_config_ok() {
    let cfg = cfg(r#"policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: admin only
"#);
    assert_eq!(cfg.policies.len(), 1);
    assert_eq!(cfg.policies[0].name, "p1");
    assert_eq!(cfg.policies[0].effect, Effect::Allow);
    assert_eq!(cfg.policies[0].otherwise, Effect::Deny);
    assert_eq!(cfg.policies[0].message, "admin only");
}

#[test]
fn parse_config_bad_topic() {
    let err = parse_config(
        br#"policies:
  - name: p1
    on: kit.runtime.entity.created
    when: 'true'
    effect: allow
    otherwise: deny
"#,
    )
    .unwrap_err();
    assert!(err.to_string().contains("not a veto-able topic"), "{err}");
}

#[test]
fn parse_config_duplicate_name() {
    let err = parse_config(
        br#"policies:
  - name: dup
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
  - name: dup
    on: kit.runtime.entity.pre_persisted
    when: 'true'
    effect: allow
    otherwise: deny
"#,
    )
    .unwrap_err();
    assert!(err.to_string().contains("duplicate name"), "{err}");
}

#[test]
fn parse_config_missing_fields() {
    let cases = [
        (
            "missing name",
            "policies:\n  - on: kit.runtime.entity.pre_validated\n    when: 'true'\n    effect: allow\n    otherwise: deny\n",
            "name required",
        ),
        (
            "missing when",
            "policies:\n  - name: p1\n    on: kit.runtime.entity.pre_validated\n    effect: allow\n    otherwise: deny\n",
            "'when' required",
        ),
        (
            "missing effect",
            "policies:\n  - name: p1\n    on: kit.runtime.entity.pre_validated\n    when: 'true'\n    otherwise: deny\n",
            "effect required (allow|deny)",
        ),
        (
            "missing otherwise",
            "policies:\n  - name: p1\n    on: kit.runtime.entity.pre_validated\n    when: 'true'\n    effect: allow\n",
            "otherwise required (allow|deny)",
        ),
        (
            "bad effect value",
            "policies:\n  - name: p1\n    on: kit.runtime.entity.pre_validated\n    when: 'true'\n    effect: maybe\n    otherwise: deny\n",
            "effect \"maybe\" invalid (want allow|deny)",
        ),
    ];
    for (name, body, want) in cases {
        let err = parse_config(body.as_bytes()).expect_err(name);
        assert!(err.to_string().contains(want), "{name}: {err}");
    }
}

#[test]
fn parse_config_rejects_async() {
    let err = parse_config(
        br#"policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
    async: true
"#,
    )
    .unwrap_err();
    assert!(err.to_string().contains("async not supported"), "{err}");
}

#[test]
fn parse_config_malformed_yaml_and_empty() {
    let err = parse_config(b"policies: [\n").unwrap_err();
    assert!(err.to_string().starts_with("policy: parse yaml:"), "{err}");
    assert!(parse_config(b"").unwrap().policies.is_empty());
    assert!(parse_config(b"policies:\n").unwrap().policies.is_empty());
}

// --- Engine construction: CEL compile errors surface at boot ---

#[test]
fn new_engine_bad_cel() {
    let cfg = cfg(r#"policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'this is not cel ?? !!'
    effect: allow
    otherwise: deny
"#);
    assert!(Engine::with_cel(&cfg).is_err());
}

/// The core refuses to build without an explicit evaluator. (Go's
/// nil-config case cannot arise: `build` takes a reference.)
#[test]
fn new_engine_no_evaluator() {
    let err = EngineBuilder::default()
        .build(&Default::default())
        .err()
        .unwrap();
    assert!(err.to_string().contains("no evaluator configured"), "{err}");
}

// --- decide: matrix of allow/deny outcomes ---

#[test]
fn decide_no_policies_default_allow() {
    let eng = Engine::with_cel(&Default::default()).unwrap();
    eng.decide("kit.runtime.entity.pre_validated", bare_activation())
        .unwrap();
}

#[test]
fn decide_unmatched_topic_default_allow() {
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: admin-only-cancel
    on: kit.runtime.state.pre_transitioned
    when: 'true'
    effect: allow
    otherwise: deny
"#))
    .unwrap();
    eng.decide("kit.runtime.entity.pre_persisted", bare_activation())
        .unwrap();
}

#[test]
fn decide_allow_on_match() {
    let eng = Engine::with_cel(&load_go("testdata/policies.yaml")).unwrap();
    let mut act = bare_activation();
    act.insert(
        "principal".into(),
        json!({"id": "u1", "role": "admin", "source": "ctx"}),
    );
    eng.decide("kit.runtime.entity.pre_validated", act).unwrap();
}

#[test]
fn decide_deny_on_no_match() {
    let eng = Engine::with_cel(&load_go("testdata/policies.yaml")).unwrap();
    // writes-need-role: when=principal.role!="", effect=allow,
    // otherwise=deny. With an empty role the otherwise fires.
    let err = eng
        .decide("kit.runtime.entity.pre_validated", bare_activation())
        .unwrap_err();
    assert_eq!(err.policy_name, "writes-need-role");
    assert_eq!(err.topic, "kit.runtime.entity.pre_validated");
    assert_eq!(err.decision, Decision::Deny);
}

#[test]
fn decide_deny_overrides_first_deny_message_wins() {
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: p1-allow
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
  - name: p2-deny
    on: kit.runtime.entity.pre_validated
    when: 'false'
    effect: allow
    otherwise: deny
    message: "first deny"
  - name: p3-deny
    on: kit.runtime.entity.pre_validated
    when: 'false'
    effect: allow
    otherwise: deny
    message: "second deny"
"#))
    .unwrap();
    let err = eng
        .decide("kit.runtime.entity.pre_validated", Map::new())
        .unwrap_err();
    assert_eq!(err.policy_name, "p2-deny", "first denying policy wins");
    assert!(err.to_string().contains("first deny"));
}

#[test]
fn decide_all_allow_allows() {
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
  - name: p2
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
"#))
    .unwrap();
    eng.decide("kit.runtime.entity.pre_validated", Map::new())
        .unwrap();
}

#[test]
fn decide_evaluation_error_denies() {
    // `principal.role` on an empty principal: no such key.
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: needs-role
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: allow
"#))
    .unwrap();
    let mut act = Map::new();
    act.insert("principal".into(), json!({}));
    let err = eng
        .decide("kit.runtime.entity.pre_validated", act)
        .unwrap_err();
    assert_eq!(err.policy_name, "needs-role");
    assert!(
        err.message.starts_with("evaluation error: "),
        "{}",
        err.message
    );
}

#[test]
fn decide_non_bool_expression_denies() {
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: not-bool
    on: kit.runtime.entity.pre_validated
    when: '1 + 1'
    effect: allow
    otherwise: allow
"#))
    .unwrap();
    let err = eng
        .decide("kit.runtime.entity.pre_validated", Map::new())
        .unwrap_err();
    assert!(
        err.message.contains("did not return bool"),
        "{}",
        err.message
    );
}

// --- PolicyDeniedError ---

#[test]
fn policy_denied_error_is_conflict_and_text() {
    let e = PolicyDeniedError {
        policy_name: "x".into(),
        topic: "kit.runtime.entity.pre_validated".into(),
        message: "nope".into(),
        decision: Decision::Deny,
    };
    assert!(e.is_conflict());
    assert!(hop_top_kit::policy::is_policy_denied(&e));
    assert_eq!(e.to_string(), r#"policy "x" denied: nope"#);
    let bare = PolicyDeniedError {
        message: String::new(),
        ..e
    };
    assert_eq!(bare.to_string(), r#"policy "x" denied"#);
}

// --- default_principal_resolver: ctx source (env cases: policy_env.rs) ---

#[test]
fn default_principal_resolver_from_ctx() {
    let want = principal("u", "admin", "ctx");
    let ctx = with_principal(&Context::background(), want.clone());
    assert_eq!(hop_top_kit::policy::default_principal_resolver(&ctx), want);
    // An unset source defaults to "ctx".
    let ctx = with_principal(&Context::background(), principal("u", "admin", ""));
    assert_eq!(
        hop_top_kit::policy::default_principal_resolver(&ctx).source,
        "ctx"
    );
}

// --- Integration: real bus + state machine ---

fn machine(bus: &DynBus, rules: &[(&str, &[&str])]) -> StateMachine {
    let rules: Rules = rules
        .iter()
        .map(|(f, t)| (State::from(*f), t.iter().map(|s| State::from(*s)).collect()))
        .collect();
    StateMachine::new(rules, Some(Arc::new(BusAdapter(bus.clone()))))
}

const ADMIN_CANCEL: &str = r#"policies:
  - name: admin-only-cancel
    on: kit.runtime.state.pre_transitioned
    when: 'payload.To == "CANCELED" && principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: "only admin may cancel"
"#;

#[test]
fn integration_state_machine_veto() {
    let eng = engine_as(&cfg(ADMIN_CANCEL), principal("u", "user", "ctx"));
    let bus = DynBus::builder().build();
    let unwire = wire(&bus, eng);

    let sm = machine(&bus, &[("OPEN", &["CANCELED", "DONE"])]);
    let err = sm
        .transition(&Context::background(), "OPEN", "CANCELED", false)
        .unwrap_err();
    assert!(matches!(err, StateMachineError::Veto(_)));
    assert!(err.to_string().contains("only admin may cancel"), "{err}");
    unwire.unwire();
    // Unwired: the same transition passes.
    sm.transition(&Context::background(), "OPEN", "CANCELED", false)
        .unwrap();
}

#[test]
fn integration_state_machine_allowed() {
    let eng = engine_as(&cfg(ADMIN_CANCEL), principal("u", "admin", "ctx"));
    let bus = DynBus::builder().build();
    let _unwire = wire(&bus, eng);
    let sm = machine(&bus, &[("OPEN", &["CANCELED"])]);
    sm.transition(&Context::background(), "OPEN", "CANCELED", false)
        .unwrap();
}

// --- Integration: real bus + entity pre-events ---

#[test]
fn integration_service_pre_validated_veto() {
    let eng = engine_as(
        &cfg(r#"policies:
  - name: writes-need-role
    on: kit.runtime.entity.pre_validated
    when: 'principal.role != ""'
    effect: allow
    otherwise: deny
    message: "writes require an authenticated principal"
"#),
        principal("u", "", "none"),
    );
    let bus = DynBus::builder().build();
    let _unwire = wire(&bus, eng);

    let svc = Service::new(&bus);
    let err = svc
        .create(&Context::background(), json!({"ID": "1", "Name": "x"}))
        .unwrap_err();
    assert!(err
        .to_string()
        .contains("writes require an authenticated principal"));
    assert!(
        svc.repo.created.lock().unwrap().is_empty(),
        "veto must abort before the repo"
    );
}

#[test]
fn integration_service_pre_persisted_veto() {
    let eng = engine_as(
        &cfg(r#"policies:
  - name: delete-requires-note
    on: kit.runtime.entity.pre_persisted
    when: 'payload.Op != "delete" || context.note != ""'
    effect: allow
    otherwise: deny
    message: "deleting requires --note"
"#),
        principal("u", "admin", "ctx"),
    );
    let bus = DynBus::builder().build();
    let _unwire = wire(&bus, eng);
    let svc = Service::new(&bus);

    // No note: denied on pre_persisted.
    let err = svc.delete(&Context::background(), "1").unwrap_err();
    assert!(
        err.to_string().contains("deleting requires --note"),
        "{err}"
    );

    // With a note on the context: allowed.
    let ctx = with_context_attrs(
        &Context::background(),
        ContextAttrs {
            note: "cleanup".into(),
            ..Default::default()
        },
    );
    svc.delete(&ctx, "1").unwrap();
    assert_eq!(*svc.repo.deleted.lock().unwrap(), vec!["1".to_string()]);
}

#[test]
fn integration_testdata_yaml() {
    let eng = engine_as(
        &load_go("testdata/policies.yaml"),
        principal("u", "admin", "ctx"),
    );
    let bus = DynBus::builder().build();
    let _unwire = wire(&bus, eng);
    Service::new(&bus)
        .create(&Context::background(), json!({"ID": "x", "Name": "y"}))
        .unwrap();
}

// --- The activation the subscriber builds ---

#[test]
fn activation_shapes() {
    let eng = Engine::builder()
        .cel()
        .principal_resolver(|_| principal("alice", "user", "ctx"))
        .build(&Default::default())
        .unwrap();
    let ctx = with_context_attrs(
        &Context::background(),
        ContextAttrs {
            note: "n".into(),
            request_attrs: json!({"freeze": true}).as_object().unwrap().clone(),
        },
    );

    // Entity pre-event: resource/entity come from the entity fields.
    let ev = Event::new(
        "kit.runtime.entity.pre_validated",
        "domain.service",
        json!({"Op": "create", "Phase": "pre_validated", "EntityID": "ops",
               "Entity": {"Kind": "task", "TrackType": "fix"}}),
    );
    let a = eng.activation(&ctx, &ev);
    assert_eq!(
        a["principal"],
        json!({"id": "alice", "role": "user", "source": "ctx"})
    );
    assert_eq!(a["resource"]["id"], "ops");
    assert_eq!(a["resource"]["kind"], "task");
    assert_eq!(a["resource"]["fields"]["TrackType"], "fix");
    assert_eq!(
        a["entity"],
        json!({"kind": "task", "op": "create", "track_type": "fix"})
    );
    assert_eq!(
        a["context"],
        json!({"note": "n", "request_attrs": {"freeze": true}})
    );
    assert_eq!(a["payload"]["Op"], "create");

    // Entity absent (delete): empty fields, empty kind.
    let ev = Event::new(
        "kit.runtime.entity.pre_persisted",
        "domain.service",
        json!({"Op": "delete", "Phase": "pre_persisted", "EntityID": "9", "Entity": null}),
    );
    let a = eng.activation(&Context::background(), &ev);
    assert_eq!(a["resource"], json!({"id": "9", "kind": "", "fields": {}}));
    assert_eq!(
        a["entity"],
        json!({"kind": "", "op": "delete", "track_type": ""})
    );
    assert_eq!(a["context"], json!({"note": "", "request_attrs": {}}));

    // Any other object: the payload is the resource's fields.
    let ev = Event::new(
        "kit.runtime.state.pre_transitioned",
        "domain.statemachine",
        json!({"From": "A", "To": "B", "Force": true, "id": "r1", "kind": "k", "op": "x"}),
    );
    let a = eng.activation(&Context::background(), &ev);
    assert_eq!(a["resource"]["id"], "r1");
    assert_eq!(a["resource"]["kind"], "k");
    assert_eq!(a["resource"]["fields"]["To"], "B");
    assert_eq!(
        a["entity"],
        json!({"kind": "k", "op": "x", "track_type": ""})
    );

    // A non-object payload is an empty map.
    let ev = Event::new("kit.runtime.state.pre_transitioned", "s", json!("text"));
    assert_eq!(
        eng.activation(&Context::background(), &ev)["payload"],
        json!({})
    );
}

/// The CEL surface the shipped rules and the docs lean on.
#[test]
fn cel_surface_in_rules() {
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: surface
    on: kit.runtime.entity.pre_validated
    when: 'principal.role in ["a", "b"] && size(payload.items) == 2 && payload.name.matches("^x[0-9]+$") && has(payload.name) && payload.items.exists(i, i == 2) && !has(payload.missing)'
    effect: allow
    otherwise: deny
"#))
    .unwrap();
    let mut act = bare_activation();
    act.insert("principal".into(), json!({"role": "b"}));
    act.insert("payload".into(), json!({"items": [1, 2], "name": "x42"}));
    eng.decide("kit.runtime.entity.pre_validated", act.clone())
        .unwrap();
    act.insert("payload".into(), json!({"items": [1, 2], "name": "y"}));
    eng.decide("kit.runtime.entity.pre_validated", act)
        .unwrap_err();
}
