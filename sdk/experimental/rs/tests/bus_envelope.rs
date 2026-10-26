//! The envelope members beyond topic/source/timestamp/payload: `id`,
//! `subject`, `datacontenttype`, `dataschema`, `extensions`, and the
//! `workspace_id` rules. Ports the envelope parts of
//! `go/runtime/bus/event_test.go` and `cloudevents_test.go`
//! (`TestNewEvent_MintsTypeID`, the extension cases of
//! `TestToCloudEvent_Errors`); the shared vectors live in
//! `contracts/bus-v1` and run from `bus_conformance.rs`.

#![cfg(feature = "bus")]

use std::time::{Duration, UNIX_EPOCH};

use hop_top_kit::bus::event::{
    canonical_timestamp, mint_event_id, EventError, EVENT_ID_PREFIX, ORIGIN_EXTENSION,
    WORKSPACE_EXTENSION,
};
use hop_top_kit::bus::Event;
use serde_json::{json, Value};

fn base() -> Event {
    Event::new_at("kit.test.order.created", "src", json!(null), UNIX_EPOCH)
}

// --- id ---------------------------------------------------------------

#[test]
fn mint_event_id_matches_the_typeid_vectors() {
    // 01940000-0000-7000-8000-000000000000 is the typeid-v1 `task`
    // vector; only the prefix differs.
    let at = UNIX_EPOCH + Duration::from_millis(0x0194_0000_0000);
    assert_eq!(mint_event_id(at, [0; 10]), "evt_01jg000000e008000000000000");
    // Random bits fill rand_a, the variant's low 6 bits and the tail.
    assert_eq!(
        mint_event_id(at, [0xff; 10]),
        "evt_01jg000000fzzvzzzzzzzzzzzz"
    );
    let at = UNIX_EPOCH + Duration::from_millis(1_700_000_000_123);
    assert_eq!(
        mint_event_id(at, [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]),
        "evt_01hf7yat3ve41861050r3gg28a"
    );
    assert_eq!(
        mint_event_id(UNIX_EPOCH, [0; 10]),
        "evt_0000000000e008000000000000"
    );
}

#[test]
fn mint_event_id_clamps_pre_epoch_instants() {
    let before = UNIX_EPOCH - Duration::from_secs(5);
    assert_eq!(
        mint_event_id(before, [0; 10]),
        mint_event_id(UNIX_EPOCH, [0; 10])
    );
}

#[test]
fn event_id_prefix_is_evt() {
    assert_eq!(EVENT_ID_PREFIX, "evt");
}

/// Port of `TestNewEvent_MintsTypeID`.
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[test]
fn new_event_mints_a_typeid() {
    let e = Event::new("kit.test.order.created", "src", json!(null));
    let suffix = e.id.strip_prefix("evt_").expect("evt_ prefix");
    assert_eq!(suffix.len(), 26, "id = {}", e.id);
    let e2 = Event::new("kit.test.order.created", "src", json!(null));
    assert_ne!(e.id, e2.id, "ids must be unique per event");
}

#[test]
fn new_at_leaves_the_id_empty() {
    // The clock seam is pure: no randomness, same on every target.
    assert!(base().id.is_empty());
}

#[test]
fn with_id_sets_the_id() {
    let e = base().with_id(mint_event_id(UNIX_EPOCH, [0; 10]));
    assert_eq!(e.id, "evt_0000000000e008000000000000");
}

// --- wire shape ---------------------------------------------------------

#[test]
fn minimal_event_emits_only_the_four_base_keys() {
    let js = serde_json::to_string(&base()).unwrap();
    assert_eq!(
        js,
        r#"{"topic":"kit.test.order.created","source":"src","timestamp":"1970-01-01T00:00:00Z","payload":null}"#
    );
}

#[test]
fn full_event_emits_members_in_go_order() {
    let e = base()
        .with_workspace_id("ws1")
        .with_id("evt_x")
        .with_subject("deal/42")
        .with_data_content_type("application/json")
        .with_data_schema("https://example.com/s.json")
        .with_extension("zz", "z")
        .with_extension("aa", 7)
        .with_extension("m1", true);
    let js = serde_json::to_string(&e).unwrap();
    assert_eq!(
        js,
        concat!(
            r#"{"topic":"kit.test.order.created","source":"src","#,
            r#""timestamp":"1970-01-01T00:00:00Z","payload":null,"#,
            r#""workspace_id":"ws1","id":"evt_x","subject":"deal/42","#,
            r#""datacontenttype":"application/json","#,
            r#""dataschema":"https://example.com/s.json","#,
            r#""extensions":{"aa":7,"m1":true,"zz":"z"}}"#
        )
    );
}

#[test]
fn empty_optional_members_are_omitted() {
    let mut e = base();
    e.id = String::new();
    e.extensions.clear();
    let v: Value = serde_json::to_value(&e).unwrap();
    let keys: Vec<&str> = v.as_object().unwrap().keys().map(String::as_str).collect();
    for k in [
        "workspace_id",
        "id",
        "subject",
        "datacontenttype",
        "dataschema",
        "extensions",
    ] {
        assert!(!keys.contains(&k), "{k} should be omitted: {keys:?}");
    }
}

#[test]
fn decode_without_optional_members() {
    let e: Event = serde_json::from_str(
        r#"{"topic":"a.b.c.d","source":"s","timestamp":"2024-05-01T12:00:00Z","payload":1}"#,
    )
    .unwrap();
    assert!(e.id.is_empty() && e.subject.is_empty() && e.extensions.is_empty());
}

/// Go decodes an object missing any member, and a `null` as unset.
#[test]
fn decode_is_lenient_about_absent_and_null_members() {
    let e: Event = serde_json::from_str("{}").unwrap();
    assert_eq!(e.topic.as_str(), "");
    assert_eq!(e.source, "");
    assert_eq!(e.timestamp, "0001-01-01T00:00:00Z");
    assert_eq!(e.payload, Value::Null);

    let e: Event = serde_json::from_str(
        r#"{"topic":null,"source":null,"timestamp":null,"payload":null,"workspace_id":null,"extensions":null}"#,
    )
    .unwrap();
    assert_eq!(e.timestamp, "0001-01-01T00:00:00Z");
    assert!(e.extensions.is_empty());
}

#[test]
fn decode_ignores_unknown_members() {
    let e: Event = serde_json::from_str(
        r#"{"topic":"a.b.c.d","source":"s","timestamp":"2024-05-01T12:00:00Z","payload":null,"origin":"abc"}"#,
    )
    .unwrap();
    assert_eq!(e.source, "s");
}

#[test]
fn decode_keeps_extensions_unvalidated() {
    let e: Event =
        serde_json::from_str(r#"{"extensions":{"Bad Name":1.5,"list":[1],"workspaceid":"x"}}"#)
            .unwrap();
    assert_eq!(e.extensions.len(), 3);
    assert!(e.validate_extensions().is_err());
}

// --- timestamp ------------------------------------------------------------

#[test]
fn rejects_non_rfc3339_timestamp() {
    for bad in [
        json!("not-a-time"),
        json!("2024-05-01T12:00:00"),
        json!("2024-05-01 12:00:00Z"),
        json!(""),
        json!(12345),
    ] {
        let wire = json!({"topic": "a.b.c.d", "source": "s", "timestamp": bad, "payload": null});
        assert!(
            serde_json::from_value::<Event>(wire).is_err(),
            "{bad} should be refused"
        );
    }
}

#[test]
fn timestamp_error_names_the_field() {
    let err = serde_json::from_str::<Event>(r#"{"timestamp":"nope"}"#).unwrap_err();
    assert!(
        err.to_string().contains("invalid RFC 3339 timestamp"),
        "{err}"
    );
}

#[test]
fn canonical_timestamp_normalises_like_go() {
    let cases = [
        ("2024-05-01T12:00:00Z", "2024-05-01T12:00:00Z"),
        ("2024-05-01T12:00:00.500Z", "2024-05-01T12:00:00.5Z"),
        ("2024-05-01T12:00:00.000Z", "2024-05-01T12:00:00Z"),
        ("2024-05-01T12:00:00,5Z", "2024-05-01T12:00:00.5Z"),
        (
            "2024-05-01T12:00:00.1234567891Z",
            "2024-05-01T12:00:00.123456789Z",
        ),
        ("2024-05-01T12:00:00-00:00", "2024-05-01T12:00:00Z"),
        ("2024-05-01T12:00:00+00:00", "2024-05-01T12:00:00Z"),
        ("2024-05-01T12:00:00.5+02:00", "2024-05-01T12:00:00.5+02:00"),
    ];
    for (given, want) in cases {
        assert_eq!(canonical_timestamp(given).as_deref(), Ok(want), "{given}");
    }
    for bad in ["2024-02-30T00:00:00Z", "2024-05-01T12:00:60Z", "2024-05-01"] {
        assert!(canonical_timestamp(bad).is_err(), "{bad}");
    }
}

#[test]
fn leap_day_follows_the_calendar() {
    assert!(canonical_timestamp("2024-02-29T00:00:00Z").is_ok());
    assert!(canonical_timestamp("2023-02-29T00:00:00Z").is_err());
    assert!(canonical_timestamp("1900-02-29T00:00:00Z").is_err());
    assert!(canonical_timestamp("2000-02-29T00:00:00Z").is_ok());
}

// --- extensions -------------------------------------------------------------

fn ext_err(e: &Event) -> String {
    e.validate_extensions().unwrap_err().to_string()
}

#[test]
fn extension_value_types_pass() {
    let e = base()
        .with_extension("tenantid", "t1")
        .with_extension("replay", true)
        .with_extension("seq", 7)
        .with_extension("min", i64::from(i32::MIN))
        .with_extension("max", i64::from(i32::MAX));
    assert_eq!(e.validate_extensions(), Ok(()));
}

#[test]
fn extension_errors_match_go_texts() {
    let cases: Vec<(&str, Value, &str)> = vec![
        (
            "tenant_id",
            json!("x"),
            "cloudevents: tenant_id: invalid extension name",
        ),
        (
            "Tenant",
            json!("x"),
            "cloudevents: Tenant: invalid extension name",
        ),
        ("", json!("x"), "cloudevents: \"\": invalid extension name"),
        (
            "data_base64",
            json!("x"),
            "cloudevents: data_base64: invalid extension name",
        ),
        (
            "subject",
            json!("x"),
            "cloudevents: subject: reserved attribute name",
        ),
        (
            "workspaceid",
            json!("x"),
            "cloudevents: workspaceid: reserved for the workspace id",
        ),
        (
            "kitorigin",
            json!("x"),
            "cloudevents: kitorigin: reserved for the network origin",
        ),
        (
            "a",
            json!(1.5),
            "cloudevents: a: unsupported extension value type",
        ),
        (
            "a",
            json!(null),
            "cloudevents: a: unsupported extension value type",
        ),
        (
            "a",
            json!([1]),
            "cloudevents: a: unsupported extension value type",
        ),
        (
            "a",
            json!(2_147_483_648_i64),
            "cloudevents: a: integer out of range",
        ),
        (
            "a",
            json!(-2_147_483_649_i64),
            "cloudevents: a: integer out of range",
        ),
        ("a", json!(u64::MAX), "cloudevents: a: integer out of range"),
    ];
    for (name, value, want) in cases {
        let mut e = base();
        e.extensions.insert(name.to_string(), value.clone());
        assert_eq!(ext_err(&e), want, "{name}={value}");
    }
}

#[test]
fn extension_names_are_checked_in_ascending_order() {
    let mut e = base();
    e.extensions.insert("b".into(), json!(1.5));
    e.extensions.insert("a".into(), json!([1]));
    assert_eq!(
        ext_err(&e),
        "cloudevents: a: unsupported extension value type"
    );
}

#[test]
fn extension_error_exposes_attribute_and_reason() {
    let mut e = base();
    e.extensions.insert("kitorigin".into(), json!("x"));
    let err: EventError = e.validate_extensions().unwrap_err();
    assert_eq!(err.attribute(), "kitorigin");
    assert_eq!(err.reason(), "reserved for the network origin");
    assert_eq!(ORIGIN_EXTENSION, "kitorigin");
    assert_eq!(WORKSPACE_EXTENSION, "workspaceid");
}

#[test]
fn workspace_id_field_is_not_an_extension() {
    let e = base().with_workspace_id("ws1");
    assert_eq!(e.validate_extensions(), Ok(()));
}
