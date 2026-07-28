//! Event envelope + qualifiers. Ports `go/runtime/bus` `event_test.go`
//! (envelope cases) and `qualifiers_test.go`.

#![cfg(feature = "bus")]

use hop_top_kit::bus::{qualifiers_from, Bus, Event, Mode, Qualifiers, Topic};
use serde::{Deserialize, Serialize};
use serde_json::json;

// --- Event -----------------------------------------------------------

#[test]
fn event_json_round_trip() {
    let e = Event::new("llm.request", "test", json!({ "model": "claude-4" }));

    let data = serde_json::to_string(&e).expect("marshal");
    let decoded: Event = serde_json::from_str(&data).expect("unmarshal");

    assert_eq!(decoded.topic.as_str(), "llm.request");
    assert_eq!(decoded.source, "test");
    assert_eq!(decoded.payload["model"], "claude-4");
    assert_eq!(decoded.timestamp, e.timestamp);
}

#[test]
fn new_event_sets_timestamp() {
    let e = Event::new("test.topic", "src", json!(null));
    // YYYY-MM-DDTHH:MM:SS.sssZ
    assert_eq!(e.timestamp.len(), 24, "timestamp = {}", e.timestamp);
    assert!(e.timestamp.ends_with('Z'), "timestamp = {}", e.timestamp);
    assert!(e.timestamp.starts_with("20"), "timestamp = {}", e.timestamp);
}

/// Verifies the envelope marshals with lowercase JSON keys per the bus
/// topics spec §4. Cross-process subscribers parse lowercase;
/// capitalized keys would break them.
#[test]
fn event_json_lowercase_field_names() {
    let e = Event::new("x.y", "src", json!({ "k": "v" }));
    let js = serde_json::to_string(&e).expect("marshal");

    for key in [
        r#""topic""#,
        r#""source""#,
        r#""timestamp""#,
        r#""payload""#,
    ] {
        assert!(
            js.contains(key),
            "expected JSON to contain {key}, got: {js}"
        );
    }
    for bad in [
        r#""Topic""#,
        r#""Source""#,
        r#""Timestamp""#,
        r#""Payload""#,
    ] {
        assert!(
            !js.contains(bad),
            "expected JSON to NOT contain capitalized {bad}, got: {js}"
        );
    }
}

/// `workspace_id` survives a JSON round-trip with a snake_case key and
/// is omitted when blank, for backward compat with v0.1 publishers.
#[test]
fn event_workspace_id_round_trip() {
    let e = Event::new("tlc.task.created", "tlc", json!(null))
        .with_workspace_id("01J7ZXY8Q2K9V0M3N4P5R6S7T8");

    let data = serde_json::to_string(&e).expect("marshal");
    assert!(
        data.contains(r#""workspace_id":"01J7ZXY8Q2K9V0M3N4P5R6S7T8""#),
        "expected snake_case workspace_id in JSON, got: {data}"
    );

    let decoded: Event = serde_json::from_str(&data).expect("unmarshal");
    assert_eq!(decoded.workspace_id, "01J7ZXY8Q2K9V0M3N4P5R6S7T8");

    // Blank: the key is dropped entirely.
    let blank = Event::new("tlc.task.created", "tlc", json!(null));
    let blank_data = serde_json::to_string(&blank).expect("marshal blank");
    assert!(
        !blank_data.contains("workspace_id"),
        "blank workspace_id should be omitted, got: {blank_data}"
    );
    // ...and absence still deserialises.
    let decoded: Event = serde_json::from_str(&blank_data).expect("unmarshal blank");
    assert_eq!(decoded.workspace_id, "");
}

// --- Qualifiers ------------------------------------------------------

/// Payload with flattened qualifiers — the Go anonymous-embed analogue.
#[derive(Serialize, Deserialize)]
struct FlatPayload {
    #[serde(flatten)]
    qualifiers: Qualifiers,
    snapshot_id: String,
}

/// Payload with a nested qualifiers object — the Go named-embed analogue.
#[derive(Serialize, Deserialize)]
struct NestedPayload {
    qualifiers: Qualifiers,
    snapshot_id: String,
}

/// Payload without any qualifiers.
#[derive(Serialize, Deserialize)]
struct PlainPayload {
    snapshot_id: String,
}

#[test]
fn qualifiers_is_zero() {
    let mut q = Qualifiers::default();
    assert!(q.is_zero(), "default Qualifiers should report is_zero=true");
    q.reason = "sighup".into();
    assert!(!q.is_zero(), "Qualifiers with reason set is not zero");
}

#[test]
fn qualifiers_empty_marshals_to_empty_object() {
    let raw = serde_json::to_string(&Qualifiers::default()).expect("marshal");
    assert_eq!(raw, "{}");
}

#[test]
fn qualifiers_populated_marshals_all_fields() {
    let q = Qualifiers {
        reason: "sighup".into(),
        mechanism: "signal".into(),
        property: "snapshot_path".into(),
        circumstance: "during_reload".into(),
    };
    let raw = serde_json::to_string(&q).expect("marshal");
    let back: Qualifiers = serde_json::from_str(&raw).expect("unmarshal");
    assert_eq!(back, q);
}

#[test]
fn qualifiers_from_flattened() {
    let p = FlatPayload {
        qualifiers: Qualifiers {
            reason: "sighup".into(),
            mechanism: "signal".into(),
            ..Default::default()
        },
        snapshot_id: "abc".into(),
    };
    let v = serde_json::to_value(&p).expect("to_value");
    let q = qualifiers_from(&v).expect("qualifiers_from flattened");
    assert_eq!(q.reason, "sighup");
    assert_eq!(q.mechanism, "signal");
}

#[test]
fn qualifiers_from_nested_field() {
    let p = NestedPayload {
        qualifiers: Qualifiers {
            reason: "manual".into(),
            ..Default::default()
        },
        snapshot_id: "abc".into(),
    };
    let v = serde_json::to_value(&p).expect("to_value");
    let q = qualifiers_from(&v).expect("qualifiers_from nested");
    assert_eq!(q.reason, "manual");
}

#[test]
fn qualifiers_from_no_qualifiers_returns_none() {
    let p = PlainPayload {
        snapshot_id: "abc".into(),
    };
    let v = serde_json::to_value(&p).expect("to_value");
    assert!(qualifiers_from(&v).is_none());
}

#[test]
fn qualifiers_from_null_returns_none() {
    assert!(qualifiers_from(&json!(null)).is_none());
}

#[test]
fn qualifiers_from_non_object_returns_none() {
    for v in [json!("string"), json!(42), json!(["a"]), json!(true)] {
        assert!(qualifiers_from(&v).is_none(), "{v}");
    }
}

/// In-process round trip: the publisher passes a payload carrying
/// qualifiers; the subscriber extracts them via `qualifiers_from`.
#[test]
fn qualifiers_publish_subscribe_round_trip() {
    use std::cell::RefCell;
    use std::rc::Rc;

    let mut bus = Bus::builder().enforce(Mode::Strict).build();
    let topic = Topic::from("kit.config.snapshot.failed");

    let got: Rc<RefCell<Option<Qualifiers>>> = Rc::new(RefCell::new(None));
    let sink = Rc::clone(&got);
    bus.subscribe(topic.as_str(), move |e| {
        *sink.borrow_mut() = qualifiers_from(&e.payload);
        Ok(())
    });

    let payload = FlatPayload {
        qualifiers: Qualifiers {
            reason: "sighup".into(),
            mechanism: "signal".into(),
            ..Default::default()
        },
        snapshot_id: "snap-1".into(),
    };
    let event = Event::new(
        topic,
        "test",
        serde_json::to_value(&payload).expect("to_value"),
    );
    bus.publish(&event).expect("publish");

    let q = got
        .borrow()
        .clone()
        .expect("subscriber extracted qualifiers");
    assert_eq!(q.reason, "sighup");
    assert_eq!(q.mechanism, "signal");
}
