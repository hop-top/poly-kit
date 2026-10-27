//! The [`Event`] mapping: ports of `go/runtime/bus/cloudevents_test.go`.
//! The shared vectors run in `bus_cloudevents_conformance.rs`; the struct
//! API is in `bus_cloudevents_json.rs`, frames in `bus_cloudevents_frames.rs`.

#![cfg(feature = "bus")]

use std::collections::BTreeMap;

use hop_top_kit::bus::cloudevents::{
    from_cloud_event, marshal_cloud_event, to_cloud_event, to_cloud_event_binary,
    unmarshal_cloud_event, CloudEvent, Data, ExtensionValue,
};
use hop_top_kit::bus::{Event, Topic};
use serde_json::{json, Value};

const ID: &str = "evt_01jg000000e008000000000000";
const CE_TIME: &str = "2026-10-25T12:00:00.5Z";

fn valid_ce() -> CloudEvent {
    CloudEvent::new(ID, "kit.test", "kit.test.order.created")
}

fn event(topic: &str, source: &str, payload: Value) -> Event {
    let mut e = Event::new_at(topic, source, payload, std::time::UNIX_EPOCH);
    e.timestamp = "0001-01-01T00:00:00Z".to_string();
    e
}

fn ext(pairs: &[(&str, ExtensionValue)]) -> BTreeMap<String, ExtensionValue> {
    pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.clone()))
        .collect()
}

fn vext(pairs: &[(&str, Value)]) -> BTreeMap<String, Value> {
    pairs
        .iter()
        .map(|(k, v)| (k.to_string(), v.clone()))
        .collect()
}

#[test]
fn to_cloud_event_mapping() {
    let mut e = event(
        "kit.test.order.created",
        "kit.checkout",
        json!({"total": 3}),
    );
    e.id = ID.into();
    e.timestamp = CE_TIME.into();
    e.subject = "order-42".into();
    e.data_schema = "https://example.com/order.json".into();
    e.workspace_id = "ws1".into();
    e.extensions = vext(&[
        ("tenantid", json!("t1")),
        ("seq", json!(7)),
        ("replay", json!(true)),
    ]);
    let ce = to_cloud_event(&e).unwrap();
    let want = CloudEvent {
        spec_version: "1.0".into(),
        id: ID.into(),
        source: "kit.checkout".into(),
        event_type: "kit.test.order.created".into(),
        subject: "order-42".into(),
        time: CE_TIME.into(),
        data_content_type: "application/json".into(),
        data_schema: "https://example.com/order.json".into(),
        data: Some(Data::Json(r#"{"total":3}"#.into())),
        extensions: ext(&[
            ("tenantid", "t1".into()),
            ("seq", 7i64.into()),
            ("replay", true.into()),
            ("workspaceid", "ws1".into()),
        ]),
    };
    assert_eq!(ce, want);
}

#[test]
fn to_cloud_event_defaults() {
    // null payload omits data and datacontenttype
    let ce =
        to_cloud_event(&event("kit.test.order.created", "src", Value::Null).with_id("x")).unwrap();
    assert_eq!(ce.data, None);
    assert_eq!(ce.data_content_type, "");

    // bytes payload uses data_base64
    let e = event("kit.test.order.created", "src", Value::Null)
        .with_id("x")
        .with_data_content_type("application/octet-stream");
    let ce = to_cloud_event_binary(&e, vec![0xde, 0xad]).unwrap();
    assert_eq!(ce.data, Some(Data::Binary(vec![0xde, 0xad])));
    assert_eq!(ce.data_content_type, "application/octet-stream");

    // explicit datacontenttype kept
    let e = event("kit.test.order.created", "src", json!("hi"))
        .with_id("x")
        .with_data_content_type("text/plain");
    let ce = to_cloud_event(&e).unwrap();
    assert_eq!(ce.data_content_type, "text/plain");
    assert_eq!(ce.data, Some(Data::Json(r#""hi""#.into())));

    // structured JSON passes through with the default content type
    let ce = to_cloud_event(&event("kit.test.order.created", "src", json!([1, 2])).with_id("x"))
        .unwrap();
    assert_eq!(ce.data, Some(Data::Json("[1,2]".into())));
    assert_eq!(ce.data_content_type, "application/json");
}

#[test]
fn to_cloud_event_binary_needs_a_null_payload() {
    let e = event("kit.test.order.created", "src", json!({"k": 1})).with_id("x");
    let err = to_cloud_event_binary(&e, vec![1]).unwrap_err();
    assert_eq!(
        err.to_string(),
        "cloudevents: data: data and data_base64 are mutually exclusive"
    );
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
#[test]
fn to_cloud_event_mints_missing_id() {
    let ce = to_cloud_event(&event("kit.test.order.created", "src", Value::Null)).unwrap();
    assert!(
        ce.id.starts_with("evt_") && ce.id.len() == 30,
        "minted id {:?}",
        ce.id
    );
    assert_eq!(ce.time, "", "zero timestamp must leave time unset");
    let again = to_cloud_event(&event("kit.test.order.created", "src", Value::Null)).unwrap();
    assert_ne!(ce.id, again.id, "ids must be unique per event");
}

#[test]
fn to_cloud_event_errors() {
    let base = || event("kit.test.order.created", "src", Value::Null).with_id(ID);
    let cases: Vec<(&str, Event, &str)> = vec![
        (
            "empty source",
            {
                let mut e = base();
                e.source.clear();
                e
            },
            "cloudevents: source: required",
        ),
        (
            "empty topic",
            {
                let mut e = base();
                e.topic = Topic::from("");
                e
            },
            "cloudevents: type: required",
        ),
        (
            "workspaceid in extensions",
            base().with_extension("workspaceid", "x"),
            "cloudevents: workspaceid: reserved for the workspace id",
        ),
        (
            "bad extension name",
            base().with_extension("tenant_id", "x"),
            "cloudevents: tenant_id: invalid extension name",
        ),
        (
            "kitorigin in extensions",
            base().with_extension("kitorigin", "x"),
            "cloudevents: kitorigin: reserved for the network origin",
        ),
        (
            "kitoffset in extensions",
            base().with_extension("kitoffset", "1"),
            "cloudevents: kitoffset: reserved for the stream offset",
        ),
        (
            "structured payload, text content type",
            {
                let mut e = base().with_data_content_type("text/plain");
                e.payload = json!({"k": 1});
                e
            },
            "cloudevents: data: non-JSON datacontenttype needs a string or binary payload",
        ),
        (
            "year past 9999 in UTC",
            {
                let mut e = base();
                e.timestamp = "9999-12-31T23:00:00-02:00".into();
                e
            },
            "cloudevents: time: invalid RFC 3339 timestamp",
        ),
        (
            "unparsable timestamp",
            {
                let mut e = base();
                e.timestamp = "yesterday".into();
                e
            },
            "cloudevents: time: invalid RFC 3339 timestamp",
        ),
    ];
    for (name, e, want) in cases {
        let err = to_cloud_event(&e).expect_err(name);
        assert_eq!(err.to_string(), want, "{name}");
    }
}

#[test]
fn cloud_event_round_trip() {
    let mut e = event(
        "kit.test.order.created",
        "kit.checkout",
        json!({"total": 3.0, "items": ["a"]}),
    );
    e.id = ID.into();
    e.timestamp = CE_TIME.into();
    e.subject = "order-42".into();
    e.data_content_type = "application/json".into();
    e.workspace_id = "ws1".into();
    e.extensions = vext(&[("tenantid", json!("t1")), ("seqoffset", json!(9))]);
    let raw = marshal_cloud_event(&e).unwrap();
    let got = unmarshal_cloud_event(raw.as_bytes()).unwrap();
    assert_eq!(got.binary, None);
    assert_eq!(got.event, e);

    let bin = {
        let mut e = event("kit.test.blob.stored", "s", Value::Null).with_id("x");
        e.timestamp = CE_TIME.into();
        e
    };
    let ce = to_cloud_event_binary(&bin, b"hi".to_vec()).unwrap();
    let got = unmarshal_cloud_event(ce.to_json().unwrap().as_bytes()).unwrap();
    assert_eq!(got.binary.as_deref(), Some(&b"hi"[..]));
    assert_eq!(got.event, bin);
}

#[test]
fn decoded_time_keeps_its_offset_on_the_event() {
    let input = r#"{"specversion":"1.0","id":"x","source":"s","type":"t","time":"2026-10-25t08:00:00.50-04:00"}"#;
    let got = unmarshal_cloud_event(input.as_bytes()).unwrap();
    assert_eq!(got.event.timestamp, "2026-10-25T08:00:00.5-04:00");
}

#[test]
fn to_cloud_event_content_type_rules() {
    let cases: Vec<(&str, Value, bool)> = vec![
        ("application/json", json!({"k": 1}), true),
        (
            "application/cloudevents+json; charset=utf-8",
            json!([1]),
            true,
        ),
        ("TEXT/JSON", json!(1), true),
        ("text/plain", json!("hello"), true),
        ("text/plain", json!(1), false),
        ("application/xml", json!(true), false),
        ("jsonish", json!({}), false),
        ("/json", json!({}), false),
    ];
    for (dct, payload, ok) in cases {
        let e = event("kit.test.order.created", "src", payload.clone())
            .with_id("x")
            .with_data_content_type(dct);
        assert_eq!(to_cloud_event(&e).is_ok(), ok, "{dct} / {payload}");
    }
}

#[test]
fn to_cloud_event_empty_bytes_payload() {
    let e = event("kit.test.blob.stored", "s", Value::Null).with_id("x");
    let raw = to_cloud_event_binary(&e, vec![])
        .unwrap()
        .to_json()
        .unwrap();
    assert!(raw.ends_with(r#""data_base64":""}"#), "{raw}");
    let got = unmarshal_cloud_event(raw.as_bytes()).unwrap();
    assert_eq!(got.binary, Some(vec![]));
}

#[test]
fn from_cloud_event_reserved_extensions() {
    let mut c = valid_ce();
    c.extensions = ext(&[("kitorigin", "A".into())]);
    let err = from_cloud_event(c).unwrap_err();
    assert_eq!(
        err.to_string(),
        "cloudevents: kitorigin: reserved for the network origin"
    );

    let mut c = valid_ce();
    c.extensions = ext(&[("kitoffset", "1".into())]);
    let err = from_cloud_event(c).unwrap_err();
    assert_eq!(
        err.to_string(),
        "cloudevents: kitoffset: reserved for the stream offset"
    );
}

#[test]
fn from_cloud_event_workspace_extension_must_be_string() {
    let mut c = valid_ce();
    c.extensions = ext(&[("workspaceid", 1i64.into())]);
    let err = from_cloud_event(c).unwrap_err();
    assert_eq!(
        err.to_string(),
        "cloudevents: workspaceid: must be a string"
    );
}

#[test]
fn event_validate_extensions_reserves_kitoffset() {
    let e = event("kit.test.order.created", "src", Value::Null).with_extension("kitoffset", "1");
    let err = e.validate_extensions().unwrap_err();
    assert_eq!(
        err.to_string(),
        "cloudevents: kitoffset: reserved for the stream offset"
    );
    // Same text as shaping the event.
    assert_eq!(
        to_cloud_event(&e.with_id("x")).unwrap_err().to_string(),
        err.to_string()
    );
}
