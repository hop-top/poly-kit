//! Port of go/runtime/policy's stage_test.go and violated_test.go: the
//! shipped stage ruleset, its allow/deny matrix, the `stage` binding and
//! the stage-violated emit.
#![cfg(feature = "policy")]

mod policy_support;

use std::sync::{Arc, Mutex};

use hop_top_kit::bus::{Context, DynBus, Event};
use hop_top_kit::policy::{
    stage_config, wire, Engine, Op, Phase, PreEntityPayload, ViolationPayload, ViolationPublisher,
    STAGE_YAML,
};
use policy_support::*;
use serde_json::{json, Map, Value};

#[test]
fn shipped_stage_yaml_is_goes() {
    let go = std::fs::read_to_string(go_policy_dir().join("stage.yaml")).unwrap();
    assert_eq!(STAGE_YAML, go, "src/policy/stage.yaml drifted from Go's");
}

#[test]
fn stage_yaml_loads() {
    let cfg = stage_config();
    let names: Vec<&str> = cfg.policies.iter().map(|p| p.name.as_str()).collect();
    assert_eq!(
        names,
        [
            "archived-blocks-all-mutations",
            "sunset-blocks-creates",
            "feature-freeze-blocks-track-create",
            "feature-freeze-blocks-feature-tasks",
            "maintenance-blocks-track-create",
            "public-feedback-allows-feedback-only-tracks",
        ]
    );
}

fn static_stage(
    mode: &'static str,
) -> impl Fn(&str) -> Result<Map<String, Value>, Box<dyn std::error::Error + Send + Sync>> {
    move |_| {
        Ok(json!({"mode": mode, "since": "", "until": null, "reason": "", "allow": [], "deny": []})
            .as_object()
            .unwrap()
            .clone())
    }
}

#[test]
fn stage_yaml_allow_deny_matrix() {
    // (stage, kind, op, track_type, deny rule substring; "" = allowed)
    let cases: &[(&str, &str, &str, &str, &str)] = &[
        ("active", "track", "create", "feature", ""),
        ("active", "task", "create", "fix", ""),
        ("active", "task", "create", "docs", ""),
        ("active", "task", "delete", "feature", ""),
        ("public_feedback", "track", "create", "feedback", ""),
        (
            "public_feedback",
            "track",
            "create",
            "feature",
            "public-feedback",
        ),
        (
            "public_feedback",
            "track",
            "create",
            "fix",
            "public-feedback",
        ),
        ("public_feedback", "task", "create", "feature", ""),
        ("public_feedback", "track", "update", "feature", ""),
        (
            "feature_freeze",
            "track",
            "create",
            "feature",
            "feature-freeze-blocks-track-create",
        ),
        (
            "feature_freeze",
            "task",
            "create",
            "feature",
            "feature-freeze-blocks-feature-tasks",
        ),
        (
            "feature_freeze",
            "task",
            "create",
            "refactor",
            "feature-freeze-blocks-feature-tasks",
        ),
        ("feature_freeze", "task", "create", "fix", ""),
        ("feature_freeze", "task", "create", "chore", ""),
        ("feature_freeze", "task", "create", "docs", ""),
        (
            "maintenance",
            "track",
            "create",
            "feature",
            "maintenance-blocks-track-create",
        ),
        ("maintenance", "task", "create", "fix", ""),
        ("maintenance", "task", "create", "docs", ""),
        ("maintenance", "task", "update", "fix", ""),
        (
            "sunset",
            "track",
            "create",
            "feature",
            "sunset-blocks-creates",
        ),
        ("sunset", "task", "create", "fix", "sunset-blocks-creates"),
        ("sunset", "track", "update", "feature", ""),
        ("sunset", "task", "delete", "fix", ""),
        ("archived", "track", "create", "feature", "archived-blocks"),
        ("archived", "track", "update", "feature", "archived-blocks"),
        ("archived", "task", "delete", "fix", "archived-blocks"),
    ];
    let cfg = stage_config();
    for &(stage, kind, op, track_type, deny) in cases {
        let eng = Engine::builder()
            .cel()
            .stage_resolver(static_stage(stage))
            .build(&cfg)
            .unwrap();
        let act = json!({
            "principal": {"id": "alice", "role": "user"},
            "resource": {"id": "ops", "kind": kind},
            "entity": {"kind": kind, "op": op, "track_type": track_type},
            "context": {},
            "payload": {"scope": "ops"},
        });
        let name = format!("{stage}_{kind}_{op}_{track_type}");
        let got = eng.decide(
            "kit.runtime.entity.pre_validated",
            act.as_object().unwrap().clone(),
        );
        if deny.is_empty() {
            assert!(got.is_ok(), "expected allow for {name}: {got:?}");
        } else {
            let err = got.expect_err(&name);
            assert!(
                err.policy_name.contains(deny),
                "{name}: denied by {}",
                err.policy_name
            );
        }
    }
}

// --- the stage-violated emit ---

#[derive(Default)]
struct Rec(Mutex<Vec<(String, ViolationPayload)>>);

impl ViolationPublisher for Rec {
    fn publish(&self, topic: &str, payload: &ViolationPayload) {
        self.0
            .lock()
            .unwrap()
            .push((topic.to_string(), payload.clone()));
    }
}

impl Rec {
    fn snapshot(&self) -> Vec<(String, ViolationPayload)> {
        self.0.lock().unwrap().clone()
    }
}

fn act(extra_entity: Value) -> Map<String, Value> {
    json!({
        "principal": {"id": "alice", "role": "user"},
        "resource": {"id": "ops", "kind": "track", "fields": {}},
        "entity": extra_entity,
        "context": {},
        "payload": {"scope": "ops"},
    })
    .as_object()
    .unwrap()
    .clone()
}

const ARCHIVED_CREATE: &str = r#"policies:
  - name: archived-blocks-mutations
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived" && entity.op == "create"'
    effect: deny
    otherwise: allow
    message: "scope archived; mutations denied"
"#;

#[test]
fn decide_stage_driven_deny_emits_violated() {
    let rec = Arc::new(Rec::default());
    let eng = Engine::builder()
        .cel()
        .stage_resolver(static_stage("archived"))
        .violation_publisher(rec.clone())
        .build(&cfg(ARCHIVED_CREATE))
        .unwrap();
    let err = eng
        .decide(
            "kit.runtime.entity.pre_validated",
            act(json!({"kind": "track", "op": "create", "track_type": "feature"})),
        )
        .unwrap_err();
    assert_eq!(err.message, "scope archived; mutations denied");

    let evs = rec.snapshot();
    assert_eq!(evs.len(), 1, "exactly one violated emit");
    assert_eq!(evs[0].0, "kit.runtime.stage.violated");
    assert_eq!(
        evs[0].1,
        ViolationPayload {
            scope: "ops".into(),
            stage: "archived".into(),
            topic: "kit.runtime.entity.pre_validated".into(),
            entity: "track".into(),
            principal: "alice".into(),
            message: "scope archived; mutations denied".into(),
        }
    );
}

#[test]
fn violation_payload_json_matches_go_tags() {
    let p = ViolationPayload {
        scope: "ops".into(),
        stage: "archived".into(),
        topic: "t".into(),
        ..Default::default()
    };
    assert_eq!(
        serde_json::to_string(&p).unwrap(),
        r#"{"scope":"ops","stage":"archived","topic":"t"}"#,
        "entity, principal and message are omitempty"
    );
}

#[test]
fn decide_non_stage_deny_no_violated_emit() {
    let rec = Arc::new(Rec::default());
    let eng = Engine::builder()
        .cel()
        .stage_resolver(static_stage("active"))
        .violation_publisher(rec.clone())
        .build(&cfg(r#"policies:
  - name: admin-only
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: "admin only"
"#))
        .unwrap();
    eng.decide(
        "kit.runtime.entity.pre_validated",
        act(json!({"kind": "track", "op": "create"})),
    )
    .unwrap_err();
    assert!(rec.snapshot().is_empty(), "non-stage deny must not emit");
}

#[test]
fn decide_nil_violation_publisher_preserves_behavior() {
    let eng = Engine::builder()
        .cel()
        .stage_resolver(static_stage("archived"))
        .build(&cfg(r#"policies:
  - name: archived-blocks
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
"#))
        .unwrap();
    eng.decide("kit.runtime.entity.pre_validated", act(json!({})))
        .unwrap_err();
}

fn archived_rule(stage: &'static str) -> Engine {
    Engine::builder()
        .cel()
        .stage_resolver(static_stage(stage))
        .build(&cfg(r#"policies:
  - name: archived-blocks
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
"#))
        .unwrap()
}

#[test]
fn decide_stage_binding_populated() {
    archived_rule("active")
        .decide("kit.runtime.entity.pre_validated", act(json!({})))
        .unwrap();
    archived_rule("archived")
        .decide("kit.runtime.entity.pre_validated", act(json!({})))
        .unwrap_err();
}

#[test]
fn decide_no_stage_resolver_stage_binding_empty() {
    let eng = Engine::with_cel(&cfg(r#"policies:
  - name: archived-blocks
    on: kit.runtime.entity.pre_validated
    when: 'has(stage.mode) && stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
"#))
    .unwrap();
    eng.decide("kit.runtime.entity.pre_validated", act(json!({})))
        .expect("no resolver = empty stage map = no match");
}

#[test]
fn decide_resolver_error_or_no_scope_gives_empty_stage() {
    let rules = cfg(r#"policies:
  - name: archived-blocks
    on: kit.runtime.entity.pre_validated
    when: 'has(stage.mode) && stage.mode == "archived"'
    effect: deny
    otherwise: allow
"#);
    let failing = Engine::builder()
        .cel()
        .stage_resolver(|_| Err("boom".into()))
        .build(&rules)
        .unwrap();
    failing
        .decide("kit.runtime.entity.pre_validated", act(json!({})))
        .unwrap();

    let archived = Engine::builder()
        .cel()
        .stage_resolver(static_stage("archived"))
        .build(&rules)
        .unwrap();
    let mut no_scope = act(json!({}));
    no_scope.insert("payload".into(), json!({}));
    no_scope.insert("resource".into(), json!({}));
    archived
        .decide("kit.runtime.entity.pre_validated", no_scope)
        .unwrap();

    // A scope resolver overrides the default lookup.
    let by_entity = Engine::builder()
        .cel()
        .stage_resolver(|scope| {
            assert_eq!(scope, "custom");
            Ok(json!({"mode": "archived"}).as_object().unwrap().clone())
        })
        .scope_resolver(|_| "custom".to_string())
        .build(&rules)
        .unwrap();
    by_entity
        .decide("kit.runtime.entity.pre_validated", no_scope_clone())
        .unwrap_err();
}

fn no_scope_clone() -> Map<String, Value> {
    let mut a = act(json!({}));
    a.insert("payload".into(), json!({}));
    a
}

#[test]
fn decide_stage_gated_topic_guard() {
    let rec = Arc::new(Rec::default());
    let eng = Engine::builder()
        .cel()
        .stage_resolver(static_stage("archived"))
        .violation_publisher(rec.clone())
        .build(&cfg(r#"policies:
  - name: archived-blocks-on-pre-persisted
    on: kit.runtime.entity.pre_persisted
    when: 'stage.mode == "archived"'
    effect: deny
    otherwise: allow
    message: "archived"
"#))
        .unwrap();
    eng.decide("kit.runtime.entity.pre_persisted", act(json!({})))
        .unwrap_err();
    assert_eq!(
        rec.snapshot().len(),
        1,
        "pre_persisted is stage-gated; emit expected"
    );
}

#[test]
fn stage_reference_is_a_word_scan() {
    let rec = Arc::new(Rec::default());
    // `mystage`/`stage_x` are not the binding: a denial from them
    // never emits.
    let eng = Engine::builder()
        .cel()
        .violation_publisher(rec.clone())
        .build(&cfg(r#"policies:
  - name: not-stage
    on: kit.runtime.entity.pre_validated
    when: 'payload.mystage == "x" || payload.stage_x == "x"'
    effect: allow
    otherwise: deny
"#))
        .unwrap();
    let mut a = act(json!({}));
    a.insert("payload".into(), json!({"mystage": "y", "stage_x": "y"}));
    eng.decide("kit.runtime.entity.pre_validated", a)
        .unwrap_err();
    assert!(rec.snapshot().is_empty());
}

/// Domain pre-event, through the subscriber and engine, to the emit.
#[test]
fn subscriber_wiring_end_to_end_violated() {
    let rec = Arc::new(Rec::default());
    let eng = Arc::new(
        Engine::builder()
            .cel()
            .stage_resolver(static_stage("archived"))
            .violation_publisher(rec.clone())
            .build(&cfg(r#"policies:
  - name: archived-blocks-create
    on: kit.runtime.entity.pre_validated
    when: 'stage.mode == "archived" && entity.op == "create"'
    effect: deny
    otherwise: allow
    message: "archived"
"#))
            .unwrap(),
    );
    let bus = DynBus::builder().build();
    let _unwire = wire(&bus, eng);

    let payload = PreEntityPayload {
        op: Op::Create,
        phase: Phase::PreValidated,
        entity_id: "ops".into(),
        entity: None,
    };
    let err = bus
        .publish_ctx(
            &Context::background(),
            &Event::new(
                "kit.runtime.entity.pre_validated",
                "domain.service",
                payload.to_value(),
            ),
        )
        .unwrap_err();
    assert!(err.to_string().contains("archived"));
    let evs = rec.snapshot();
    assert_eq!(evs.len(), 1);
    assert_eq!(evs[0].0, "kit.runtime.stage.violated");
    assert_eq!(evs[0].1.scope, "ops", "scope falls back to resource.id");
}

/// The topic gate holds even for a rule built in code on a topic the
/// parser would have refused.
#[test]
fn decide_non_gated_topic_never_emits() {
    use hop_top_kit::policy::{Config, Effect, Policy};
    let rec = Arc::new(Rec::default());
    let eng = Engine::builder()
        .cel()
        .stage_resolver(static_stage("archived"))
        .violation_publisher(rec.clone())
        .build(&Config {
            policies: vec![Policy {
                name: "off-seam".into(),
                on: "kit.runtime.entity.created".into(),
                when: r#"stage.mode == "archived""#.into(),
                effect: Effect::Deny,
                otherwise: Effect::Allow,
                message: String::new(),
            }],
        })
        .unwrap();
    eng.decide("kit.runtime.entity.created", act(json!({})))
        .unwrap_err();
    assert!(rec.snapshot().is_empty());
}
