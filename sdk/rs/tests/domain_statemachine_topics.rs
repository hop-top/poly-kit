//! Ports go/runtime/domain/statemachine_topics_test.go.
#![cfg(feature = "domain")]

#[path = "domain_statemachine_support/mod.rs"]
mod support;

use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::Arc;

use hop_top_kit::bus::{Context, Topic};
use hop_top_kit::domain::{
    default_state_machine_topics, StateMachine, StateMachineTopics, DEFAULT_POST_TRANSITIONED,
    DEFAULT_PRE_TRANSITIONED,
};

use support::{rules, within, Recording};

fn sm_rules() -> hop_top_kit::domain::Rules {
    rules(&[("open", &["closed"]), ("closed", &[])])
}

fn transition(sm: StateMachine, from: &'static str, to: &'static str) {
    within(move || sm.transition(&Context::background(), from, to, false)).unwrap();
}

#[test]
fn default_state_machine_topics_are_the_kit_baseline() {
    let t = default_state_machine_topics();
    assert_eq!(
        t.pre_transitioned,
        Topic::from("kit.runtime.state.pre_transitioned")
    );
    assert_eq!(
        t.post_transitioned,
        Topic::from("kit.runtime.state.post_transitioned")
    );
    assert_eq!(
        DEFAULT_PRE_TRANSITIONED,
        "kit.runtime.state.pre_transitioned"
    );
    assert_eq!(
        DEFAULT_POST_TRANSITIONED,
        "kit.runtime.state.post_transitioned"
    );
    assert_eq!(StateMachineTopics::default(), t);
    for topic in [&t.pre_transitioned, &t.post_transitioned] {
        hop_top_kit::bus::validate_topic(topic).unwrap();
    }
}

#[test]
fn new_state_machine_defaults_topics() {
    let rec = Recording::new();
    let sm = StateMachine::new(sm_rules(), Some(rec.clone()));
    assert_eq!(sm.topics(), &default_state_machine_topics());

    transition(sm, "open", "closed");
    rec.await_topic("kit.runtime.state.post_transitioned");

    let got = rec.topics();
    assert!(got.contains(&"kit.runtime.state.pre_transitioned".into()));
    assert!(got.contains(&"kit.runtime.state.post_transitioned".into()));
}

#[test]
fn with_topic_prefix_overrides_both() {
    let rec = Recording::new();
    let sm = StateMachine::new(sm_rules(), Some(rec.clone())).with_topic_prefix("myapp.task.state");

    transition(sm, "open", "closed");
    rec.await_topic("myapp.task.state.post_transitioned");

    let got = rec.topics();
    assert!(got.contains(&"myapp.task.state.pre_transitioned".into()));
    assert!(got.contains(&"myapp.task.state.post_transitioned".into()));
    for topic in &got {
        assert!(
            !topic.contains("kit.runtime.state"),
            "prefix override must replace defaults entirely: {topic}"
        );
    }
}

#[test]
fn with_topics_partial_override_keeps_defaults() {
    let rec = Recording::new();
    let sm = StateMachine::new(sm_rules(), Some(rec.clone())).with_topics(StateMachineTopics {
        pre_transitioned: "x.y.z.pre_transitioned".into(),
        post_transitioned: Topic::from(""),
    });

    transition(sm, "open", "closed");
    rec.await_topic("kit.runtime.state.post_transitioned");

    let got = rec.topics();
    assert!(
        got.contains(&"x.y.z.pre_transitioned".into()),
        "Pre override should be honored"
    );
    assert!(
        got.contains(&"kit.runtime.state.post_transitioned".into()),
        "empty Post field should fall back to default"
    );
}

#[test]
fn with_topics_full_override() {
    let rec = Recording::new();
    let sm = StateMachine::new(sm_rules(), Some(rec.clone())).with_topics(StateMachineTopics {
        pre_transitioned: "wsm.runtime.workspace.pre_transitioned".into(),
        post_transitioned: "wsm.runtime.workspace.post_transitioned".into(),
    });

    transition(sm, "open", "closed");
    rec.await_topic("wsm.runtime.workspace.post_transitioned");

    let got = rec.topics();
    assert!(got.contains(&"wsm.runtime.workspace.pre_transitioned".into()));
    assert!(got.contains(&"wsm.runtime.workspace.post_transitioned".into()));
}

#[test]
fn with_topic_prefix_invalid_panics() {
    let cases = [
        ("empty", ""),
        ("too few segments", "myapp.state"),
        ("too many segments", "a.b.c.d"),
        ("trailing dot", "myapp.task.state."),
        ("uppercase", "MyApp.task.state"),
    ];
    for (name, prefix) in cases {
        let res = catch_unwind(AssertUnwindSafe(|| {
            StateMachine::new(sm_rules(), None).with_topic_prefix(prefix)
        }));
        assert!(res.is_err(), "{name}: want a panic for {prefix:?}");
    }
}

#[test]
fn with_topic_prefix_panic_text_matches_go() {
    let res = catch_unwind(|| StateMachine::new(sm_rules(), None).with_topic_prefix("myapp.state"));
    let payload = res.err().expect("panics");
    let msg = payload
        .downcast_ref::<String>()
        .cloned()
        .expect("a String panic payload");
    assert_eq!(
        msg,
        r#"domain.WithSMTopicPrefix("myapp.state"): prefix "myapp.state" has 2 segments; expected 3 (source.category.object)"#
    );
}

// Any consumer wiring a StateMachine, the Go job engines included, gets
// topic customization for free: the pending -> active transition under
// a custom prefix fires the prefixed topics.
#[test]
fn job_engine_inherits_custom_topics() {
    let rules = rules(&[
        ("pending", &["active", "canceled"]),
        (
            "active",
            &["succeeded", "failed", "timeout", "pending", "canceled"],
        ),
    ]);
    let rec = Recording::new();
    let sm = StateMachine::new(rules, Some(rec.clone())).with_topic_prefix("hop.runtime.job");

    transition(sm, "pending", "active");
    rec.await_topic("hop.runtime.job.post_transitioned");

    let got = rec.topics();
    assert!(got.contains(&"hop.runtime.job.pre_transitioned".into()));
    assert!(got.contains(&"hop.runtime.job.post_transitioned".into()));
}

#[test]
fn the_last_topic_option_wins() {
    // Go applies options in order; each replaces both topics.
    let sm = StateMachine::new(sm_rules(), Some(Arc::new(Recording::default())))
        .with_topic_prefix("myapp.task.state")
        .with_topics(StateMachineTopics {
            pre_transitioned: "a.b.c.pre_transitioned".into(),
            post_transitioned: Topic::from(""),
        });
    assert_eq!(
        sm.topics(),
        &StateMachineTopics {
            pre_transitioned: "a.b.c.pre_transitioned".into(),
            post_transitioned: DEFAULT_POST_TRANSITIONED.into(),
        }
    );
}
