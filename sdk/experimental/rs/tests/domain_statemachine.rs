//! Ports go/runtime/domain/statemachine_test.go, plus the Rust seams:
//! the error enum, the payload's JSON, the context and the spawner.
#![cfg(feature = "domain")]

#[path = "domain_statemachine_support/mod.rs"]
mod support;

use std::error::Error;
use std::fmt;
use std::sync::{mpsc, Arc, Mutex};

use hop_top_kit::bus::Context;
use hop_top_kit::domain::{
    is_invalid_transition, EventPublisher, Payload, PublishError, State, StateMachine,
    StateMachineError, TransitionError, TransitionPayload,
};

use support::{rules, states, within, Manual, Recording, AWAIT};

fn test_rules() -> hop_top_kit::domain::Rules {
    rules(&[
        ("open", &["in_progress", "closed"]),
        ("in_progress", &["open", "closed"]),
        ("closed", &[]),
    ])
}

fn bg() -> Context {
    Context::background()
}

/// A veto error a test can find again in the chain.
#[derive(Debug)]
struct Vetoed(&'static str);

impl fmt::Display for Vetoed {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.0)
    }
}

impl Error for Vetoed {}

fn transition_error(err: StateMachineError) -> TransitionError {
    match err {
        StateMachineError::Invalid(te) => te,
        other => panic!("want Invalid, got {other:?}"),
    }
}

#[test]
fn state_machine_valid_transition() {
    let sm = StateMachine::new(test_rules(), None);
    sm.transition(&bg(), "open", "in_progress", false).unwrap();
}

#[test]
fn state_machine_invalid_transition() {
    let sm = StateMachine::new(test_rules(), None);
    let err = sm.transition(&bg(), "open", "open", false).unwrap_err();
    assert!(err.is_invalid_transition());
    assert!(is_invalid_transition(&err));

    let te = transition_error(err);
    assert_eq!(te.from, State::from("open"));
    assert_eq!(te.to, State::from("open"));
    assert_eq!(te.allowed, Some(states(&["in_progress", "closed"])));
}

#[test]
fn state_machine_no_rules_for_state() {
    let sm = StateMachine::new(test_rules(), None);
    let err = sm.transition(&bg(), "unknown", "open", false).unwrap_err();
    assert!(err.is_invalid_transition());
    assert_eq!(transition_error(err).allowed, None);
}

#[test]
fn state_machine_closed_has_no_transitions() {
    let sm = StateMachine::new(test_rules(), None);
    let err = sm.transition(&bg(), "closed", "open", false).unwrap_err();
    assert!(err.is_invalid_transition());
    // Rules exist but are empty: Go's non-nil empty slice, not "no rules".
    assert_eq!(transition_error(err).allowed, Some(vec![]));
}

#[test]
fn state_machine_force_bypasses_rules() {
    let sm = StateMachine::new(test_rules(), None);
    sm.transition(&bg(), "closed", "open", true).unwrap();
}

#[test]
fn state_machine_nil_publisher() {
    let sm = StateMachine::new(test_rules(), None);
    sm.transition(&bg(), "open", "closed", false).unwrap();
}

#[test]
fn state_machine_pre_transition_veto() {
    let publisher = |_: &Context, topic: &str, _: &str, _: &Payload| -> Result<(), PublishError> {
        if topic == "kit.runtime.state.pre_transitioned" {
            return Err(Box::new(Vetoed("vetoed")));
        }
        Ok(())
    };
    let sm = StateMachine::new(test_rules(), Some(Arc::new(publisher)));
    let err = within(move || sm.transition(&bg(), "open", "in_progress", false)).unwrap_err();

    assert_eq!(err.to_string(), "pre-transition veto: vetoed");
    assert!(!err.is_invalid_transition());
    let source = err.source().expect("veto keeps its cause");
    assert!(
        source.downcast_ref::<Vetoed>().is_some(),
        "errors.Is(err, vetoErr)"
    );
}

#[test]
fn state_machine_post_transition_fires() {
    let (tx, rx) = mpsc::channel();
    let tx = Mutex::new(tx);
    let publisher =
        move |_: &Context, topic: &str, _: &str, p: &Payload| -> Result<(), PublishError> {
            // Go's payload.(domain.TransitionPayload) assertion.
            if let (true, Payload::Transition(p)) =
                (topic == "kit.runtime.state.post_transitioned", p)
            {
                let _ = tx.lock().unwrap().send(p.clone());
            }
            Ok(())
        };
    let sm = StateMachine::new(test_rules(), Some(Arc::new(publisher)));
    within(move || sm.transition(&bg(), "open", "closed", false)).unwrap();

    rx.recv_timeout(AWAIT)
        .expect("timed out waiting for post-transition event");
}

#[test]
fn state_machine_allowed_from() {
    let sm = StateMachine::new(test_rules(), None);

    assert_eq!(
        sm.allowed_from("open"),
        Some(states(&["in_progress", "closed"]))
    );
    assert_eq!(sm.allowed_from("closed"), Some(vec![]));
    assert_eq!(sm.allowed_from("nonexistent"), None);

    // The returned list is a copy: mutating it leaves the rules alone.
    let mut orig = sm.allowed_from("open").unwrap();
    orig[0] = State::from("mutated");
    assert_eq!(
        sm.allowed_from("open").unwrap()[0],
        State::from("in_progress")
    );
}

#[test]
fn transition_error_message() {
    let te = TransitionError {
        from: "draft".into(),
        to: "published".into(),
        allowed: Some(states(&["review", "archived"])),
    };
    let msg = te.to_string();
    assert!(msg.contains(r#""draft" → "published""#), "{msg}");
    assert!(msg.contains("review archived"), "{msg}");
    assert_eq!(
        msg,
        r#"invalid state transition: "draft" → "published" (allowed: [review archived])"#
    );

    // None = no rules for state.
    let no_rules = TransitionError {
        from: "unknown".into(),
        to: "open".into(),
        allowed: None,
    };
    let msg = no_rules.to_string();
    assert!(msg.contains("no rules for state"), "{msg}");
    assert!(msg.contains(r#""unknown""#), "{msg}");
    assert_eq!(
        msg,
        r#"invalid state transition: no rules for state "unknown" (attempted "unknown" → "open")"#
    );
}

#[test]
fn transition_error_allowed_is_copy() {
    let sm = StateMachine::new(test_rules(), None);
    let err = sm.transition(&bg(), "open", "open", false).unwrap_err();
    let mut te = transition_error(err);
    te.allowed.as_mut().unwrap()[0] = State::from("mutated");

    assert_eq!(
        sm.allowed_from("open").unwrap()[0],
        State::from("in_progress")
    );
}

// --- Rust seams ---

#[test]
fn invalid_transition_error_displays_like_go() {
    let sm = StateMachine::new(test_rules(), None);
    let err = sm.transition(&bg(), "open", "open", false).unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"invalid state transition: "open" → "open" (allowed: [in_progress closed])"#
    );
    // The empty-rules form keeps the brackets.
    let err = sm.transition(&bg(), "closed", "open", false).unwrap_err();
    assert_eq!(
        err.to_string(),
        r#"invalid state transition: "closed" → "open" (allowed: [])"#
    );
}

#[test]
fn transition_error_is_invalid_transition() {
    let te = TransitionError {
        from: "a".into(),
        to: "b".into(),
        allowed: None,
    };
    assert!(te.is_invalid_transition());
    assert!(is_invalid_transition(&te));
    assert!(!is_invalid_transition(&Vetoed("x")));
}

#[test]
fn a_veto_wrapping_a_transition_error_is_invalid_transition() {
    // Go: errors.Is walks %w, so a subscriber that vetoes with a
    // TransitionError makes the wrapped veto match ErrInvalidTransition.
    let publisher = |_: &Context, _: &str, _: &str, _: &Payload| -> Result<(), PublishError> {
        Err(Box::new(TransitionError {
            from: "x".into(),
            to: "y".into(),
            allowed: None,
        }))
    };
    let sm = StateMachine::new(test_rules(), Some(Arc::new(publisher)));
    let err = within(move || sm.transition(&bg(), "open", "closed", false)).unwrap_err();
    assert!(err.is_invalid_transition());
    assert!(err
        .to_string()
        .starts_with("pre-transition veto: invalid state transition"));
}

#[test]
fn a_rejected_transition_publishes_nothing() {
    let rec = Recording::new();
    let sm = StateMachine::new(test_rules(), Some(rec.clone()));
    let err = within(move || sm.transition(&bg(), "closed", "open", false)).unwrap_err();
    assert!(err.is_invalid_transition());
    std::thread::sleep(std::time::Duration::from_millis(50));
    assert!(rec.topics().is_empty(), "{:?}", rec.topics());
}

#[test]
fn a_veto_suppresses_the_post_event() {
    let rec = Recording::new();
    let rec2 = rec.clone();
    let publisher = move |ctx: &Context, topic: &str, src: &str, p: &Payload| {
        rec2.publish(ctx, topic, src, p)?;
        Err::<(), PublishError>(Box::new(Vetoed("no")))
    };
    let spawner = Manual::default();
    let sm = StateMachine::new(test_rules(), Some(Arc::new(publisher)))
        .with_spawner(Arc::new(spawner.clone()));
    let err = within(move || sm.transition(&bg(), "open", "closed", false)).unwrap_err();
    assert_eq!(err.to_string(), "pre-transition veto: no");
    assert_eq!(spawner.len(), 0, "no post task after a veto");
    assert_eq!(rec.topics(), vec!["kit.runtime.state.pre_transitioned"]);
}

#[test]
fn pre_then_post_carry_source_and_payload() {
    let rec = Recording::new();
    let spawner = Manual::default();
    let sm =
        StateMachine::new(test_rules(), Some(rec.clone())).with_spawner(Arc::new(spawner.clone()));
    within(move || sm.transition(&bg(), "open", "closed", true)).unwrap();

    // The post event waits on the spawner: Go's goroutine.
    assert_eq!(rec.topics(), vec!["kit.runtime.state.pre_transitioned"]);
    assert_eq!(spawner.len(), 1);
    spawner.run_all();

    let events = rec.events();
    assert_eq!(
        rec.topics(),
        vec![
            "kit.runtime.state.pre_transitioned",
            "kit.runtime.state.post_transitioned"
        ]
    );
    let want = Payload::Transition(TransitionPayload {
        from: "open".into(),
        to: "closed".into(),
        force: true,
    });
    for e in events {
        assert_eq!(e.source, "domain.statemachine");
        assert_eq!(e.payload, want);
    }
}

#[test]
fn the_publisher_gets_the_callers_context() {
    #[derive(Debug, PartialEq)]
    struct Marker(u32);
    let (tx, rx) = mpsc::channel();
    let tx = Mutex::new(tx);
    let publisher =
        move |ctx: &Context, topic: &str, _: &str, _: &Payload| -> Result<(), PublishError> {
            let v = ctx.value::<Marker>().map(|m| m.0);
            let _ = tx.lock().unwrap().send((topic.to_string(), v));
            Ok(())
        };
    let sm = StateMachine::new(test_rules(), Some(Arc::new(publisher)));
    let ctx = bg().with_value(Marker(7));
    within(move || sm.transition(&ctx, "open", "closed", false)).unwrap();
    let mut got = vec![
        rx.recv_timeout(AWAIT).unwrap(),
        rx.recv_timeout(AWAIT).unwrap(),
    ];
    got.sort();
    assert_eq!(
        got,
        vec![
            ("kit.runtime.state.post_transitioned".to_string(), Some(7)),
            ("kit.runtime.state.pre_transitioned".to_string(), Some(7)),
        ]
    );
}

#[test]
fn payload_json_matches_go_encoding() {
    let p = TransitionPayload {
        from: "open".into(),
        to: "closed".into(),
        force: false,
    };
    assert_eq!(
        p.to_json(),
        r#"{"From":"open","To":"closed","Force":false}"#
    );
    assert_eq!(
        Payload::Transition(p).to_json(),
        r#"{"From":"open","To":"closed","Force":false}"#
    );

    // encoding/json escapes HTML characters and the JS line separators.
    let p = TransitionPayload {
        from: "a<b>&c\u{2028}\u{2029}".into(),
        to: "\u{1}\n\"".into(),
        force: true,
    };
    assert_eq!(
        p.to_json(),
        r#"{"From":"a\u003cb\u003e\u0026c\u2028\u2029","To":"\u0001\n\"","Force":true}"#
    );
}

#[test]
fn a_state_machine_is_shareable_across_threads() {
    fn assert_send_sync<T: Send + Sync>() {}
    assert_send_sync::<StateMachine>();
    assert_send_sync::<StateMachineError>();
}

#[test]
fn a_publisher_trait_object_works() {
    struct Count(Mutex<usize>);
    impl EventPublisher for Count {
        fn publish(&self, _: &Context, _: &str, _: &str, _: &Payload) -> Result<(), PublishError> {
            *self.0.lock().unwrap() += 1;
            Ok(())
        }
    }
    let count = Arc::new(Count(Mutex::new(0)));
    let spawner = Manual::default();
    let sm = StateMachine::new(test_rules(), Some(count.clone()))
        .with_spawner(Arc::new(spawner.clone()));
    within(move || sm.transition(&bg(), "open", "closed", false)).unwrap();
    spawner.run_all();
    assert_eq!(*count.0.lock().unwrap(), 2);
}

#[test]
fn the_default_post_event_does_not_block_the_caller() {
    // Go's goroutine: a slow post subscriber never holds up Transition.
    let (gate_tx, gate_rx) = mpsc::channel::<()>();
    let gate_rx = Mutex::new(gate_rx);
    let rec = Recording::new();
    let rec2 = rec.clone();
    let publisher = move |ctx: &Context, topic: &str, src: &str, p: &Payload| {
        if topic == "kit.runtime.state.post_transitioned" {
            let _ = gate_rx
                .lock()
                .unwrap()
                .recv_timeout(std::time::Duration::from_secs(3));
        }
        rec2.publish(ctx, topic, src, p)
    };
    let sm = StateMachine::new(test_rules(), Some(Arc::new(publisher)));
    within(move || sm.transition(&bg(), "open", "closed", false)).unwrap();

    assert_eq!(
        rec.topics(),
        vec!["kit.runtime.state.pre_transitioned"],
        "transition returned only after the post publish"
    );
    gate_tx.send(()).unwrap();
    rec.await_topic("kit.runtime.state.post_transitioned");
}

#[test]
fn a_go_encoded_payload_decodes() {
    let raw = r#"{"From":"open","To":"closed","Force":true}"#;
    let want = TransitionPayload {
        from: "open".into(),
        to: "closed".into(),
        force: true,
    };
    let p: TransitionPayload = serde_json::from_str(raw).unwrap();
    assert_eq!(p, want);
    assert_eq!(p.to_json(), raw, "round trip keeps Go's bytes");
    let p: Payload = serde_json::from_str(raw).unwrap();
    assert_eq!(p, Payload::Transition(want));
}
