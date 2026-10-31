// Parse-side golden tests for the consent loader: which YAML documents
// the backend accepts and what each decodes to. Pins the YAML dialect
// (scalar typing, anchors, flow style, duplicate keys, multi-document
// streams, line endings) so a backend swap cannot change consent
// decisions. Every rejection must collapse to default-deny.
#![cfg(feature = "telemetry")]

use hop_top_kit::telemetry::{consent_path, legacy_consent_path, load_consent};
use std::sync::Mutex;

static ENV_LOCK: Mutex<()> = Mutex::new(());

type Decoded = (bool, i64, String, Option<String>);

fn load(canonical: Option<&str>, legacy: Option<&str>) -> Decoded {
    let _g = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
    let tmp = tempfile::tempdir().expect("tempdir");
    std::env::set_var("XDG_CONFIG_HOME", tmp.path());
    std::env::set_var("HOME", tmp.path());
    for (path, body) in [(consent_path(), canonical), (legacy_consent_path(), legacy)] {
        if let Some(body) = body {
            std::fs::create_dir_all(path.parent().unwrap()).unwrap();
            std::fs::write(&path, body).unwrap();
        }
    }
    let c = load_consent();
    (c.allowed, c.prompt_version, c.decision_source, c.decided_at)
}

fn canonical(body: &str) -> Decoded {
    load(Some(body), None)
}

fn legacy(body: &str) -> Decoded {
    load(None, Some(body))
}

fn denied() -> Decoded {
    (false, 0, "config".to_string(), None)
}

fn granted(version: i64, source: &str, at: Option<&str>) -> Decoded {
    (true, version, source.to_string(), at.map(str::to_string))
}

#[test]
fn golden_full_block() {
    let body = "kit:\n  telemetry:\n    consent:\n      state: granted\n      prompt_version: 3\n      decision_source: prompt\n      decided_at: \"2026-05-19T12:00:00Z\"\n";
    assert_eq!(
        canonical(body),
        granted(3, "prompt", Some("2026-05-19T12:00:00Z"))
    );
}

#[test]
fn golden_unquoted_timestamp_stays_a_string() {
    let body = "kit:\n  telemetry:\n    consent:\n      state: granted\n      decided_at: 2026-05-19T12:00:00Z\n";
    assert_eq!(
        canonical(body),
        granted(0, "config", Some("2026-05-19T12:00:00Z"))
    );
}

#[test]
fn golden_comments_and_blank_lines() {
    let body = "# leading\n\nkit:  # trailing\n  telemetry:\n\n    consent:\n      state: granted # inline\n      prompt_version: 1\n";
    assert_eq!(canonical(body), granted(1, "config", None));
}

#[test]
fn golden_flow_style() {
    let body = "kit: {telemetry: {consent: {state: granted, prompt_version: 4}}}\n";
    assert_eq!(canonical(body), granted(4, "config", None));
}

#[test]
fn golden_quoted_scalars_and_keys() {
    let body = "\"kit\":\n  'telemetry':\n    consent:\n      \"state\": 'granted'\n      prompt_version: 2\n      decision_source: \"flag\"\n";
    assert_eq!(canonical(body), granted(2, "flag", None));
}

#[test]
fn golden_anchor_and_alias() {
    let body =
        "base: &c\n  state: granted\n  prompt_version: 5\nkit:\n  telemetry:\n    consent: *c\n";
    assert_eq!(canonical(body), granted(5, "config", None));
}

#[test]
fn golden_crlf_line_endings() {
    let body = "kit:\r\n  telemetry:\r\n    consent:\r\n      state: granted\r\n      prompt_version: 6\r\n";
    assert_eq!(canonical(body), granted(6, "config", None));
}

#[test]
fn golden_leading_document_marker() {
    let body = "---\nkit:\n  telemetry:\n    consent:\n      state: granted\n";
    assert_eq!(canonical(body), granted(0, "config", None));
}

#[test]
fn golden_bare_bool_state_is_not_a_valid_state() {
    let body = "kit:\n  telemetry:\n    consent:\n      state: true\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_yaml_1_1_booleans_are_plain_strings() {
    // `yes` is a string under the YAML 1.2 core schema, so it is not
    // "granted" and must not be read as truthy.
    let body = "kit:\n  telemetry:\n    consent:\n      state: yes\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_string_prompt_version_fails_the_parse() {
    let body =
        "kit:\n  telemetry:\n    consent:\n      state: granted\n      prompt_version: \"2\"\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_float_prompt_version_fails_the_parse() {
    let body =
        "kit:\n  telemetry:\n    consent:\n      state: granted\n      prompt_version: 2.5\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_null_and_empty_values() {
    let body = "kit:\n  telemetry:\n    consent:\n      state: granted\n      prompt_version: ~\n      decision_source:\n      decided_at: null\n";
    assert_eq!(canonical(body), granted(0, "config", None));
}

#[test]
fn golden_duplicate_keys_are_rejected() {
    let body = "kit:\n  telemetry:\n    consent:\n      state: denied\n      state: granted\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_multi_document_stream_is_rejected() {
    let body = "kit:\n  telemetry:\n    consent:\n      state: granted\n---\nother: 1\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_empty_and_whitespace_files_deny() {
    assert_eq!(canonical(""), denied());
    assert_eq!(canonical("   \n\n"), denied());
    assert_eq!(canonical("# only a comment\n"), denied());
}

#[test]
fn golden_scalar_and_sequence_roots_deny() {
    assert_eq!(canonical("granted\n"), denied());
    assert_eq!(canonical("- kit\n- telemetry\n"), denied());
}

#[test]
fn golden_unknown_keys_are_ignored() {
    let body = "extra: [1, 2]\nkit:\n  other: x\n  telemetry:\n    extra: 1\n    consent:\n      state: denied\n      note: hi\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_tab_indentation_is_rejected() {
    let body = "kit:\n\ttelemetry:\n\t\tconsent:\n\t\t\tstate: granted\n";
    assert_eq!(canonical(body), denied());
}

#[test]
fn golden_utf8_bom_prefix() {
    let body = "\u{feff}kit:\n  telemetry:\n    consent:\n      state: granted\n";
    assert_eq!(canonical(body), granted(0, "config", None));
}

#[test]
fn golden_non_ascii_value() {
    let body = "kit:\n  telemetry:\n    consent:\n      state: granted\n      decision_source: caf\u{e9}\n";
    assert_eq!(canonical(body), granted(0, "caf\u{e9}", None));
}

#[test]
fn golden_legacy_layout() {
    let body = "telemetry:\n  consent:\n    state: granted\n    prompt_version: 1\n    decision_source: prompt\n";
    assert_eq!(legacy(body), granted(1, "prompt", None));
}

// Was "falls through to legacy": that pinned a lenient read Go never
// had. Go reads both records and fails closed when either is unreadable.
#[test]
fn golden_corrupt_canonical_fails_closed_over_a_legacy_grant() {
    let corrupt = "kit: [unterminated\n";
    let good = "telemetry:\n  consent:\n    state: granted\n";
    assert_eq!(load(Some(corrupt), Some(good)), denied());
}

#[test]
fn golden_canonical_without_consent_block_falls_through_to_legacy() {
    let canon = "kit:\n  telemetry:\n    other: 1\n";
    let good = "telemetry:\n  consent:\n    state: granted\n";
    assert_eq!(load(Some(canon), Some(good)), granted(0, "config", None));
}
