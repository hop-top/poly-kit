//! Frame codec: the pure half of `go/runtime/bus/network_frame_test.go`.
//! The shared frame vectors run in `bus_cloudevents_conformance.rs`.

#![cfg(feature = "bus")]

use hop_top_kit::bus::cloudevents::{decode_frame, encode_frame, FrameFormat};
use hop_top_kit::bus::Event;
use serde_json::{json, Value};

const ID: &str = "evt_01jg000000e008000000000000";

fn event(topic: &str, source: &str, payload: Value) -> Event {
    let mut e = Event::new_at(topic, source, payload, std::time::UNIX_EPOCH);
    e.timestamp = "0001-01-01T00:00:00Z".to_string();
    e
}

fn frame_event() -> Event {
    let mut e = event("kit.test.frame.sent", "A", json!({"k": "v"}))
        .with_id(ID)
        .with_workspace_id("ws1")
        // Set explicitly: CloudEvents frames stamp it on JSON data.
        .with_data_content_type("application/json")
        .with_extension("tenantid", "t1");
    e.timestamp = "2026-10-25T12:00:00Z".into();
    e
}

#[test]
fn frame_format_display() {
    assert_eq!(FrameFormat::Kit.to_string(), "kit");
    assert_eq!(FrameFormat::CloudEvents.to_string(), "cloudevents");
    assert_eq!(FrameFormat::default(), FrameFormat::Kit);
}

#[test]
fn encode_frame_shapes() {
    let cases = [
        (
            FrameFormat::Kit,
            &[
                "id",
                "topic",
                "source",
                "timestamp",
                "payload",
                "origin",
                "workspace_id",
            ][..],
            &["specversion", "extensions", "tenantid"][..],
        ),
        (
            FrameFormat::CloudEvents,
            &[
                "specversion",
                "id",
                "source",
                "type",
                "time",
                "datacontenttype",
                "data",
                "workspaceid",
                "tenantid",
                "kitorigin",
            ][..],
            &["topic", "payload", "origin", "workspace_id"][..],
        ),
    ];
    for (format, present, absent) in cases {
        let raw = encode_frame(format, &frame_event(), "A").unwrap();
        let m: serde_json::Map<String, Value> = serde_json::from_str(&raw).unwrap();
        for k in present {
            assert!(m.contains_key(*k), "{format}: missing {k} in {raw}");
        }
        for k in absent {
            assert!(!m.contains_key(*k), "{format}: unexpected {k} in {raw}");
        }
    }
}

#[test]
fn encode_frame_rejects_user_origin() {
    let e = frame_event().with_extension("kitorigin", "spoof");
    assert!(encode_frame(FrameFormat::CloudEvents, &e, "A").is_err());
}

#[test]
fn decode_frame_both_formats() {
    let ce_frame = encode_frame(FrameFormat::CloudEvents, &frame_event(), "A").unwrap();
    let f = decode_frame(ce_frame.as_bytes()).unwrap();
    assert_eq!(f.origin, "A");
    assert_eq!(f.format, FrameFormat::CloudEvents);
    assert_eq!(f.event, frame_event());

    let kit_frame = encode_frame(FrameFormat::Kit, &frame_event(), "A").unwrap();
    let f = decode_frame(kit_frame.as_bytes()).unwrap();
    assert_eq!(f.format, FrameFormat::Kit);
    assert_eq!(f.origin, "A");
    assert_eq!(f.event.workspace_id, "ws1");
    assert_eq!(f.event.id, ID);

    // The origin extension is stripped even when it is the only one.
    let mut bare = event("kit.test.frame.sent", "A", Value::Null).with_id("x");
    bare.timestamp = "0001-01-01T00:00:00Z".into();
    let raw = encode_frame(FrameFormat::CloudEvents, &bare, "A").unwrap();
    assert!(decode_frame(raw.as_bytes())
        .unwrap()
        .event
        .extensions
        .is_empty());

    // A stream offset on a legacy link is stripped, not checked.
    let raw = r#"{"specversion":"1.0","id":"x","source":"s","type":"t","kitoffset":"junk"}"#;
    assert!(decode_frame(raw.as_bytes())
        .unwrap()
        .event
        .extensions
        .is_empty());

    // Detection is by the exact key: a case-folded key is a kit frame.
    let f = decode_frame(br#"{"SPECVERSION":"1.0","topic":"kit.test.frame.sent","source":"s"}"#)
        .unwrap();
    assert_eq!(f.event.topic.as_str(), "kit.test.frame.sent");

    for bad in [
        "not json",
        r#"{"specversion":"0.3","id":"x","source":"s","type":"t"}"#,
        r#"{"specversion":null}"#,
        r#"{"specversion":"1.0","id":"x","source":"s","type":"t","kitorigin":7}"#,
        r#"{"topic":"t","timestamp":"yesterday"}"#,
    ] {
        assert!(decode_frame(bad.as_bytes()).is_err(), "{bad}: want error");
    }
}

#[test]
fn kit_frame_ignores_other_members() {
    // Only the kit frame keys are read: CloudEvents-only fields stay unset.
    let f =
        decode_frame(br#"{"topic":"t","subject":"x","extensions":{"a":1},"offset":"3"}"#).unwrap();
    assert_eq!(f.event.subject, "");
    assert!(f.event.extensions.is_empty());
}
