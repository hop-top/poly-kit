//! Topic builder and `parse_topic`. Ports `go/runtime/bus`
//! `builder_test.go` and `parse_test.go`; the shared vectors live in
//! `contracts/bus-v1` and run in `bus_conformance.rs`.

#![cfg(feature = "bus")]

use std::panic::{catch_unwind, AssertUnwindSafe};

use hop_top_kit::bus::{parse_topic, prefixed_topic_of, topic_of, Topic};

// --- builder_test.go ---------------------------------------------------

#[test]
fn topic_of_no_modifier() {
    let got = topic_of("kit", "config", "snapshot").action("reloaded");
    assert_eq!(got, Topic::from("kit.config.snapshot.reloaded"));
}

#[test]
fn topic_of_with_modifier() {
    let got = topic_of("kit", "config", "snapshot")
        .modifier("reload")
        .action("failed");
    assert_eq!(got, Topic::from("kit.config.snapshot_reload.failed"));
}

#[test]
fn topic_of_mod_last_wins() {
    let got = topic_of("kit", "config", "snapshot")
        .modifier("reload")
        .modifier("retry")
        .action("failed");
    assert_eq!(got, Topic::from("kit.config.snapshot_retry.failed"));
}

#[test]
fn topic_of_mod_empty_clears() {
    let got = topic_of("kit", "config", "snapshot")
        .modifier("reload")
        .modifier("")
        .action("reloaded");
    assert_eq!(got, Topic::from("kit.config.snapshot.reloaded"));
}

fn panic_text(f: impl FnOnce()) -> String {
    let err = catch_unwind(AssertUnwindSafe(f)).expect_err("expected a panic");
    err.downcast_ref::<String>()
        .cloned()
        .or_else(|| err.downcast_ref::<&str>().map(|s| s.to_string()))
        .expect("panic payload is text")
}

#[test]
fn topic_of_panics_on_uppercase() {
    let msg = panic_text(|| {
        topic_of("kit", "config", "snapshot").action("Reloaded");
    });
    assert!(
        msg.starts_with("bus.TopicOf: bus: invalid topic: \"kit.config.snapshot.Reloaded\""),
        "{msg}"
    );
}

#[test]
fn topic_of_panics_on_punctuation() {
    panic_text(|| {
        topic_of("kit", "config", "snapshot")
            .modifier("re-load")
            .action("failed");
    });
}

#[test]
fn topic_of_panics_on_non_past_tense() {
    panic_text(|| {
        topic_of("kit", "config", "snapshot").action("create");
    });
}

#[test]
fn try_action_returns_typed_error() {
    let err = topic_of("kit", "config", "snapshot")
        .try_action("create")
        .unwrap_err();
    assert_eq!(err.topic, Topic::from("kit.config.snapshot.create"));
    assert!(err.reason.contains("not past-tense"), "{}", err.reason);
    let ok = topic_of("kit", "config", "snapshot")
        .try_action("reloaded")
        .unwrap();
    assert_eq!(ok.as_str(), "kit.config.snapshot.reloaded");
}

#[test]
fn prefixed_topic_of_no_modifier() {
    let got = prefixed_topic_of("kit", "config", "snapshot", &[]).action("reloaded");
    assert_eq!(got, Topic::from("kit.config.snapshot.reloaded"));
}

#[test]
fn prefixed_topic_of_with_modifier() {
    let got = prefixed_topic_of("kit", "config", "snapshot", &["reload"]).action("failed");
    assert_eq!(got, Topic::from("kit.config.snapshot_reload.failed"));
}

#[test]
fn prefixed_topic_of_last_modifier_wins() {
    let got = prefixed_topic_of("kit", "config", "snapshot", &["reload", "retry"]).action("failed");
    assert_eq!(got, Topic::from("kit.config.snapshot_retry.failed"));
}

#[test]
fn segment_accessors() {
    let b = topic_of("kit", "config", "snapshot").modifier("reload");
    assert_eq!(b.source_seg(), "kit");
    assert_eq!(b.category_seg(), "config");
    assert_eq!(b.object_seg(), "snapshot");
    assert_eq!(b.modifier_seg(), "reload");
}

#[test]
fn segment_replacements() {
    let got = topic_of("kit", "config", "snapshot")
        .source("myapp")
        .category("runtime")
        .object("user")
        .action("created");
    assert_eq!(got, Topic::from("myapp.runtime.user.created"));
}

#[test]
fn builder_is_reusable_after_action() {
    let b = topic_of("kit", "config", "snapshot");
    assert_eq!(
        b.action("reloaded").as_str(),
        "kit.config.snapshot.reloaded"
    );
    assert_eq!(b.action("failed").as_str(), "kit.config.snapshot.failed");
}

// --- parse_test.go -----------------------------------------------------

#[test]
fn parse_no_modifier() {
    let (b, action) = parse_topic("kit.config.snapshot.reloaded").unwrap();
    assert_eq!(action, "reloaded");
    assert_eq!(
        (
            b.source_seg(),
            b.category_seg(),
            b.object_seg(),
            b.modifier_seg()
        ),
        ("kit", "config", "snapshot", "")
    );
}

#[test]
fn parse_with_modifier() {
    let (b, action) = parse_topic("kit.config.snapshot_reload.failed").unwrap();
    assert_eq!(action, "failed");
    assert_eq!(b.object_seg(), "snapshot");
    assert_eq!(b.modifier_seg(), "reload");
}

#[test]
fn parse_multi_underscore_modifier() {
    // Split on the FIRST underscore; the rest is the modifier.
    let (b, action) = parse_topic("kit.config.snapshot_partial_reload.failed").unwrap();
    assert_eq!(action, "failed");
    assert_eq!(b.object_seg(), "snapshot");
    assert_eq!(b.modifier_seg(), "partial_reload");
}

#[test]
fn parse_round_trip() {
    for want in [
        "kit.runtime.entity.created",
        "kit.config.snapshot.reloaded",
        "kit.config.snapshot_reload.failed",
        "kit.config.snapshot_partial_reload.failed",
        "myapp.core.breaker.tripped",
        "crm.sales.deal.created",
    ] {
        let (b, action) = parse_topic(want).unwrap_or_else(|e| panic!("{want}: {e}"));
        assert_eq!(b.action(&action).as_str(), want);
    }
}

#[test]
fn parse_builder_round_trip() {
    let original = topic_of("kit", "config", "snapshot")
        .modifier("reload")
        .action("failed");
    let (b, action) = parse_topic(original.as_str()).unwrap();
    assert_eq!(b.action(&action), original);
}

#[test]
fn parse_invalid_returns_typed_error() {
    for input in [
        "",
        "kit.config.snapshot",
        "kit.config.snapshot.reload.x",
        "Kit.config.snapshot.reloaded",
        "kit.config.snapshot.create",
        "kit.config.snapshot.re-loaded",
    ] {
        let err = parse_topic(input).expect_err(input);
        assert_eq!(err.topic.as_str(), input);
        assert!(!err.reason.is_empty());
        assert!(
            err.to_string().starts_with("bus: invalid topic: "),
            "{input}: {err}"
        );
    }
}

#[test]
fn parse_retarget_with_mod() {
    let (b, action) = parse_topic("kit.config.snapshot.reloaded").unwrap();
    let got = b.modifier("retry").action(&action);
    assert_eq!(got.as_str(), "kit.config.snapshot_retry.reloaded");
}
