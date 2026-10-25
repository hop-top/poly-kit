//! Ports go/runtime/domain/e2e_statemachine_test.go.
#![cfg(feature = "domain")]

#[path = "domain_statemachine_support/mod.rs"]
mod support;

use std::error::Error;
use std::fmt;
use std::sync::{mpsc, Arc, Mutex};

use hop_top_kit::bus::Context;
use hop_top_kit::domain::{Payload, PublishError, Rules, State, StateMachine};

use support::{rules, within, AWAIT};

fn e2e_rules() -> Rules {
    rules(&[
        ("draft", &["review"]),
        ("review", &["approved", "draft"]),
        ("approved", &["published"]),
        ("published", &[]),
    ])
}

fn bg() -> Context {
    Context::background()
}

#[derive(Debug)]
struct Policy;

impl fmt::Display for Policy {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("policy: blocked by reviewer")
    }
}

impl Error for Policy {}

#[test]
fn e2e_state_machine_valid_transition() {
    let sm = StateMachine::new(e2e_rules(), None);
    sm.transition(&bg(), "draft", "review", false).unwrap();
}

#[test]
fn e2e_state_machine_invalid_transition() {
    let sm = StateMachine::new(e2e_rules(), None);
    let err = sm
        .transition(&bg(), "review", "published", false)
        .unwrap_err();
    assert!(err.is_invalid_transition());
}

#[test]
fn e2e_state_machine_sync_subscriber_veto() {
    let publisher = |_: &Context, topic: &str, _: &str, _: &Payload| -> Result<(), PublishError> {
        if topic == "kit.runtime.state.pre_transitioned" {
            return Err(Box::new(Policy));
        }
        Ok(())
    };
    let sm = StateMachine::new(e2e_rules(), Some(Arc::new(publisher)));
    let err = within(move || sm.transition(&bg(), "draft", "review", false)).unwrap_err();
    assert!(err
        .source()
        .and_then(|e| e.downcast_ref::<Policy>())
        .is_some());
    assert_eq!(
        err.to_string(),
        "pre-transition veto: policy: blocked by reviewer"
    );
}

#[test]
fn e2e_state_machine_force_bypasses_rules() {
    let sm = StateMachine::new(e2e_rules(), None);
    // published has no allowed transitions; force bypasses.
    sm.transition(&bg(), "published", "draft", true).unwrap();
}

#[test]
fn e2e_state_machine_post_transition_async_event() {
    let (tx, rx) = mpsc::channel();
    let tx = Mutex::new(tx);
    let publisher =
        move |_: &Context, topic: &str, _: &str, p: &Payload| -> Result<(), PublishError> {
            if let (true, Payload::Transition(p)) =
                (topic == "kit.runtime.state.post_transitioned", p)
            {
                let _ = tx.lock().unwrap().send(p.clone());
            }
            Ok(())
        };
    let sm = StateMachine::new(e2e_rules(), Some(Arc::new(publisher)));
    within(move || sm.transition(&bg(), "draft", "review", false)).unwrap();

    let p = rx
        .recv_timeout(AWAIT)
        .expect("timed out waiting for post-transition event");
    assert_eq!(p.from, State::from("draft"));
    assert_eq!(p.to, State::from("review"));
    assert!(!p.force);
}

#[test]
fn e2e_state_machine_nil_publisher_transitions_work() {
    let sm = StateMachine::new(e2e_rules(), None);
    sm.transition(&bg(), "draft", "review", false).unwrap();
    sm.transition(&bg(), "review", "approved", false).unwrap();
    sm.transition(&bg(), "approved", "published", false)
        .unwrap();
}
