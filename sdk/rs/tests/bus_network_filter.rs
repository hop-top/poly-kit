//! `TopicFilter`: Go's `network_filter_test.go`. Deny wins first, an
//! absent allow list passes everything, an empty one passes nothing.

#![cfg(feature = "bus-network")]

use hop_top_kit::bus::network::TopicFilter;

#[test]
fn topic_filter_nil_allow_passes_all() {
    let f = TopicFilter::default();
    assert!(
        f.matches("anything.here"),
        "nil allow should pass all topics"
    );
}

#[test]
fn topic_filter_deny_blocks_first() {
    let f = TopicFilter::allow(["task.#"]).with_deny(["task.secret"]);
    assert!(
        !f.matches("task.secret"),
        "deny should block even when allow matches"
    );
    assert!(
        f.matches("task.created"),
        "non-denied allow topic should pass"
    );
}

#[test]
fn topic_filter_allow_filters() {
    let f = TopicFilter::allow(["task.*", "track.*"]);
    assert!(
        f.matches("task.created"),
        "task.created should match task.*"
    );
    assert!(
        f.matches("track.updated"),
        "track.updated should match track.*"
    );
    assert!(
        !f.matches("llm.request"),
        "llm.request should not match allow list"
    );
}

#[test]
fn topic_filter_deny_only() {
    let f = TopicFilter::deny(["internal.#"]);
    assert!(
        !f.matches("internal.debug"),
        "internal.debug should be denied"
    );
    assert!(
        f.matches("task.created"),
        "non-denied topic should pass with nil allow"
    );
}

#[test]
fn topic_filter_empty_allow_blocks_all() {
    let f = TopicFilter::allow(Vec::<String>::new());
    assert!(!f.matches("anything"), "empty allow list should block all");
}

#[test]
fn topic_filter_glob_patterns() {
    let f = TopicFilter::allow(["llm.#"]).with_deny(["llm.internal.*"]);
    assert!(f.matches("llm.request"), "llm.request should pass");
    assert!(
        f.matches("llm.request.start"),
        "llm.request.start should pass with #"
    );
    assert!(
        !f.matches("llm.internal.debug"),
        "llm.internal.debug should be denied"
    );
}
