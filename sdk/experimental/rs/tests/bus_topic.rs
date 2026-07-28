//! Topic matching + both validators. Ports `go/runtime/bus`
//! `event_test.go` (match cases), `topics_test.go`, `validate_test.go`.

#![cfg(feature = "bus")]

use hop_top_kit::bus::{prefix_topics, validate, validate_topic, Topic};

// --- Topic::matches --------------------------------------------------

#[test]
fn match_exact() {
    let topic = Topic::from("llm.request");
    assert!(topic.matches("llm.request"), "exact match should succeed");
    assert!(
        !topic.matches("llm.response"),
        "different topic should not match"
    );
}

#[test]
fn match_single_wildcard() {
    let topic = Topic::from("llm.request");
    assert!(topic.matches("llm.*"), "llm.* should match llm.request");
    assert!(topic.matches("*.request"), "*.request should match");

    let deep = Topic::from("llm.request.start");
    assert!(
        !deep.matches("llm.*"),
        "llm.* should NOT match llm.request.start (too deep)"
    );
}

#[test]
fn match_multi_wildcard() {
    let cases: &[(&str, &str, bool)] = &[
        ("llm.request", "llm.#", true),
        ("llm.request.start", "llm.#", true),
        ("llm", "llm.#", true),
        ("tool.exec", "llm.#", false),
        ("llm.request.start", "#", true),
        ("anything", "#", true),
    ];
    for (topic, pattern, want) in cases {
        assert_eq!(
            Topic::from(*topic).matches(pattern),
            *want,
            "Topic({topic:?}).matches({pattern:?})"
        );
    }
}

#[test]
fn match_hash_must_be_last_segment() {
    // Per MQTT convention, # anywhere but the end never matches.
    assert!(!Topic::from("llm.request").matches("#.request"));
}

// --- validate (published-topic contract) -----------------------------

#[test]
fn validate_valid() {
    for t in [
        "crm.sales.deal.created",
        "a.b.c.d",
        "app1.cat2.obj3.act4",
        "snake_case.with_under.score_ok.action_x",
    ] {
        assert!(
            validate(&Topic::from(t)).is_ok(),
            "validate({t:?}) unexpected error"
        );
    }
}

#[test]
fn validate_invalid() {
    let long = format!("{}.bbb.ccc.ddd", "a".repeat(130));
    let cases: &[(&str, &str)] = &[
        ("", "empty"),
        ("too.few.segments", "expected 4 segments"),
        ("a.b.c", "expected 4 segments"),
        ("a.b.c.d.e", "expected 4 segments"),
        ("crm.sales..created", "empty segment"),
        ("CRM.sales.deal.created", "invalid character"),
        ("crm.sales.deal.Created", "invalid character"),
        ("1crm.sales.deal.created", "must start with"),
        ("_crm.sales.deal.created", "must start with"),
        ("crm.sales.deal.created!", "invalid character"),
        ("crm.*.deal.created", "wildcards"),
        ("crm.sales.deal.#", "wildcards"),
        (&long, "exceeds max"),
    ];
    for (topic, reason) in cases {
        let t = Topic::from(*topic);
        let err = validate(&t).expect_err(&format!("validate({topic:?}) expected error"));
        assert_eq!(err.topic, t, "error carries the offending topic");
        assert!(
            err.to_string().contains(reason),
            "validate({topic:?}): error {:?} missing reason substring {reason:?}",
            err.to_string()
        );
    }
}

// --- validate_topic (construction-time convention) -------------------

#[test]
fn validate_topic_valid() {
    for t in [
        "kit.runtime.entity.created",
        "kit.runtime.entity.updated",
        "kit.runtime.entity.deleted",
        "kit.runtime.state.pre_transitioned",
        "kit.runtime.state.post_transitioned",
        "kit.ai.request.started",
        "kit.ai.response.received",
        "kit.api.request.ended",
        "kit.core.breaker.tripped",
        "kit.core.breaker.half_opened",
        "kit.core.upgrade.snoozed",
        "wsm.runtime.workspace.created",
        "myapp.payments.invoice.paid",
    ] {
        assert!(
            validate_topic(&Topic::from(t)).is_ok(),
            "validate_topic({t:?}) unexpected error"
        );
    }
}

#[test]
fn validate_topic_empty() {
    let err = validate_topic(&Topic::from("")).unwrap_err();
    assert!(err.contains("empty"), "{err}");
}

#[test]
fn validate_topic_wrong_segment_count() {
    for t in [
        "kit.api.request",
        "kit.api",
        "kit.runtime.entity.user.created",
    ] {
        let err = validate_topic(&Topic::from(t)).unwrap_err();
        assert!(err.contains("segments"), "{t}: {err}");
    }
}

#[test]
fn validate_topic_empty_segment() {
    let err = validate_topic(&Topic::from("kit..entity.created")).unwrap_err();
    assert!(err.contains("empty segment"), "{err}");
}

#[test]
fn validate_topic_uppercase() {
    let err = validate_topic(&Topic::from("kit.runtime.Entity.created")).unwrap_err();
    assert!(err.contains("lowercase"), "{err}");
}

#[test]
fn validate_topic_non_alpha() {
    let err = validate_topic(&Topic::from("kit.runtime.entity-thing.created")).unwrap_err();
    assert!(err.contains("lowercase"), "{err}");
}

#[test]
fn validate_topic_present_tense() {
    let err = validate_topic(&Topic::from("kit.api.request.start")).unwrap_err();
    assert!(err.contains("past-tense"), "{err}");
}

#[test]
fn validate_topic_whitelisted_action() {
    // "started" is whitelisted explicitly rather than matched by the
    // simple "ends in ed" heuristic.
    assert!(validate_topic(&Topic::from("kit.ai.request.started")).is_ok());
}

#[test]
fn validate_topic_snake_case_action() {
    for t in [
        "kit.runtime.state.pre_transitioned",
        "kit.runtime.state.post_transitioned",
        "kit.core.breaker.half_opened",
    ] {
        assert!(validate_topic(&Topic::from(t)).is_ok(), "{t}");
    }
}

// --- prefix_topics ---------------------------------------------------

#[test]
fn prefix_topics_happy_path() {
    let (got, err) = prefix_topics("wsm.runtime.workspace", &["created", "updated", "deleted"]);
    assert!(err.is_none(), "{err:?}");
    assert_eq!(got["created"].as_str(), "wsm.runtime.workspace.created");
    assert_eq!(got["updated"].as_str(), "wsm.runtime.workspace.updated");
    assert_eq!(got["deleted"].as_str(), "wsm.runtime.workspace.deleted");
    assert_eq!(got.len(), 3);
}

#[test]
fn prefix_topics_empty_prefix() {
    let (_, err) = prefix_topics("", &["created"]);
    assert!(err.expect("expected error").contains("empty"));
}

#[test]
fn prefix_topics_trailing_dot() {
    let (_, err) = prefix_topics("wsm.runtime.workspace.", &["created"]);
    assert!(err.expect("expected error").contains("must not end"));
}

#[test]
fn prefix_topics_wrong_prefix_segments() {
    for p in ["wsm.runtime", "wsm.runtime.workspace.subpart", "wsm"] {
        let (_, err) = prefix_topics(p, &["created"]);
        let err = err.unwrap_or_else(|| panic!("{p}: expected error"));
        assert!(
            err.contains("segments") || err.contains("empty"),
            "want 'segments' or 'empty' in: {err}"
        );
    }
}

#[test]
fn prefix_topics_invalid_prefix_segment() {
    let (_, err) = prefix_topics("wsm.runtime.Workspace", &["created"]);
    assert!(err.expect("expected error").contains("lowercase"));
}

#[test]
fn prefix_topics_invalid_action_surfaces_validate_error() {
    // present-tense action, fails validate_topic
    let (_, err) = prefix_topics("wsm.runtime.workspace", &["start"]);
    let err = err.expect("expected error");
    assert!(err.contains("past-tense"), "{err}");
    assert!(err.contains("prefix_topics"), "{err}");
}

#[test]
fn prefix_topics_partial_map_on_error() {
    // "created" is valid; "start" is not. The partial map keeps
    // "created" and omits "start".
    let (got, err) = prefix_topics("wsm.runtime.workspace", &["created", "start"]);
    assert!(err.is_some());
    assert!(got.contains_key("created"));
    assert!(!got.contains_key("start"));
}
