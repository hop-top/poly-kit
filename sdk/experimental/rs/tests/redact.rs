// Integration tests for telemetry::redact. Cargo compiles every file
// under tests/ unconditionally, so the entire body is gated on the
// `telemetry` feature to keep default-feature builds happy.
#![cfg(feature = "telemetry")]

use hop_top_kit::telemetry::redact::{redact, redact_string};
use serde_json::{json, Value};

#[test]
fn redacts_email_in_string() {
    let out = redact_string("user alice@example.com signed up");
    assert_eq!(out, "user <redacted:email> signed up");
}

#[test]
fn redacts_ipv4_in_string() {
    let out = redact_string("client 10.0.0.42 connected");
    assert_eq!(out, "client <redacted:ipv4> connected");
}

#[test]
fn redacts_ipv6_full_form() {
    let out = redact_string("client 2001:0db8:0000:0000:0000:0000:0000:0001 connected");
    assert_eq!(out, "client <redacted:ipv6> connected");
}

#[test]
fn redacts_ipv6_compressed() {
    // Cross-SDK pattern (matches PHP) covers `::` compressed form.
    let out = redact_string("addr 2001:db8::1 here");
    assert_eq!(out, "addr <redacted:ipv6> here");
}

#[test]
fn redacts_ipv6_link_local() {
    let out = redact_string("peer fe80::1234:5678:abcd:ef01 up");
    assert_eq!(out, "peer <redacted:ipv6> up");
}

#[test]
fn ipv6_bare_loopback_under_match_documented() {
    // PHP-parity pattern requires at least one hex group BEFORE `::`,
    // so the bare `::1` loopback is intentionally NOT redacted.
    // Documented trade-off; the alternative (matching pure leading `::`)
    // would diverge from the cross-lang regex.
    assert_eq!(redact_string("ping ::1 ok"), "ping ::1 ok");
}

#[test]
fn ipv6_ipv4_mapped_documented() {
    // `::ffff:192.168.1.1` has no leading hex group before `::`, so the
    // IPv6 pass skips the `::ffff` prefix; the IPv4 pass redacts the
    // dotted-quad tail. Result: `::ffff:<redacted:ipv4>` (partial).
    let out = redact_string("hybrid ::ffff:192.168.1.1 here");
    assert!(!out.contains("192.168.1.1"), "got: {out}");
    assert!(out.contains("<redacted:ipv4>"), "got: {out}");
}

#[test]
fn redacts_sk_token() {
    let out = redact_string("Authorization: Bearer sk-AbCdEf1234567890 trailing");
    assert_eq!(out, "Authorization: Bearer <redacted:token> trailing");
}

#[test]
fn redacts_github_token() {
    let out = redact_string("token ghp_abcdefghijklmnopqrstuv next");
    assert!(out.contains("<redacted:token>"));
    assert!(!out.contains("ghp_abcdefghijklmnop"));
}

#[test]
fn redacts_slack_bot_token() {
    let out = redact_string("xoxb-12345-67890-abcdefghijklmnopqrstuvwx tail");
    assert!(out.contains("<redacted:token>"));
}

#[test]
fn nested_json_recurses_into_arrays_and_objects() {
    let v = json!({
        "user": "alice@example.com",
        "ips": ["10.0.0.1", "10.0.0.2"],
        "deep": {
            "token": "sk-AbCdEf1234567890"
        }
    });
    let r = redact(v);
    let s = serde_json::to_string(&r).unwrap();
    assert!(s.contains("<redacted:email>"));
    assert!(s.contains("<redacted:ipv4>"));
    assert!(s.contains("<redacted:token>"));
    assert!(!s.contains("alice@example.com"));
}

#[test]
fn non_string_value_types_pass_through() {
    let v = json!({"n": 42, "b": true, "f": 7.5, "nil": null});
    let r = redact(v.clone());
    assert_eq!(r, v);
}

#[test]
fn non_string_root_value_passes_through() {
    assert_eq!(redact(Value::Null), Value::Null);
    assert_eq!(redact(json!(42)), json!(42));
    assert_eq!(redact(json!(true)), json!(true));
}

#[test]
fn object_keys_preserved_verbatim() {
    // ADR-0038 §7: flag KEYS are not redacted; only VALUES route
    // through redact. Use a key that LOOKS like an email; it must
    // survive.
    let v = json!({"sender@example.com": "ok"});
    let r = redact(v);
    let obj = r.as_object().unwrap();
    assert!(obj.contains_key("sender@example.com"));
}
