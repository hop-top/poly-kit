//! Runs `contracts/bus-v1/streams/event_frames.json`: `offset` and
//! `kitoffset` on event frames, encoded and decoded through the
//! CloudEvents core. Go is canonical (`TestStreamConformanceEventFrames`).
//! Outcomes are exact: an `event` checks origin, offset and the event; a
//! `protocol_error` asserts its kind; a `drop` must not decode.

#![cfg(feature = "bus")]

mod bus_stream_support;

// The CloudEvents runner's vector event form (`../cloudevents`).
#[allow(dead_code)]
#[path = "bus_cloudevents_support/mod.rs"]
mod bus_cloudevents_support;

use bus_cloudevents_support::{json_eq, parse_json, VEvent};
use bus_stream_support::{check, load, must_offset, non_empty, stream_file};
use hop_top_kit::bus::cloudevents::DecodedEvent;
use hop_top_kit::bus::stream::{
    decode_event_frame, encode_event_frame, format_offset, EventFrameError, FrameFormat,
};
use serde::Deserialize;
use serde_json::Value;

stream_file!(FramesFile {
    encode: Vec<EncodeCase>,
    decode: Vec<DecodeCase>,
});
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EncodeCase {
    name: String,
    format: String,
    origin: String,
    offset: Option<String>,
    event: VEvent,
    frame: Value,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DecodeCase {
    name: String,
    frame: String,
    stream_link: bool,
    outcome: String,
    #[serde(default)]
    kind: Option<String>,
    origin: Option<String>,
    offset: Option<String>,
    #[serde(default)]
    event: Option<VEvent>,
}

fn format(s: &str) -> FrameFormat {
    match s {
        "kit" => FrameFormat::Kit,
        "cloudevents" => FrameFormat::CloudEvents,
        other => panic!("vector format {other:?}"),
    }
}

#[test]
fn stream_conformance_event_frames() {
    let f: FramesFile = load("event_frames.json");
    non_empty(&f.encode, "encode");
    non_empty(&f.decode, "decode");
    let mut fails = Vec::new();

    for tc in &f.encode {
        let n = format!("encode/{}", tc.name);
        let off = tc.offset.as_deref().map(must_offset);
        match encode_event_frame(format(&tc.format), &tc.event.event(), &tc.origin, off) {
            Ok(out) if parse_json(&out).is_some_and(|g| json_eq(&g, &tc.frame)) => {}
            Ok(out) => fails.push(format!("{n}:\n   got  {out}\n   want {}", tc.frame)),
            Err(e) => fails.push(format!("{n}: {e}")),
        }
    }

    for tc in &f.decode {
        let n = format!("decode/{}", tc.name);
        let got = decode_event_frame(tc.frame.as_bytes(), tc.stream_link);
        match (tc.outcome.as_str(), got) {
            ("event", Ok(of)) => {
                let want_origin = tc.origin.as_deref().expect("vector: event needs an origin");
                if of.frame.origin != want_origin {
                    fails.push(format!(
                        "{n}: origin {:?}, want {want_origin:?}",
                        of.frame.origin
                    ));
                }
                let off = of.offset.map(format_offset);
                if off != tc.offset {
                    fails.push(format!("{n}: offset {off:?}, want {:?}", tc.offset));
                }
                if let Some(v) = &tc.event {
                    let d = DecodedEvent {
                        event: of.frame.event,
                        binary: of.frame.binary,
                    };
                    fails.extend(v.diff(&d).into_iter().map(|d| format!("{n}: {d}")));
                }
            }
            ("protocol_error", Err(EventFrameError::Protocol(p))) => {
                let want = tc
                    .kind
                    .as_deref()
                    .expect("vector: protocol_error needs a kind");
                if p.kind.as_str() != want {
                    fails.push(format!("{n}: kind {}, want {want}", p.kind.as_str()));
                }
            }
            ("drop", Err(EventFrameError::Drop(_))) => {}
            (want, got) => fails.push(format!("{n}: got {got:?}, want {want}")),
        }
        if tc.outcome != "event" && (tc.origin.is_some() || tc.offset.is_some()) {
            fails.push(format!("{n}: vector: {} with origin or offset", tc.outcome));
        }
    }
    check(fails);
}

/// Go's `decodeEventFrame`: a frame unusable for any other reason is
/// dropped, which wins over a bad offset. No vector pairs the two.
#[test]
fn drop_wins_over_a_bad_offset() {
    let frames: [&[u8]; 2] = [
        br#"{"specversion":"1.0","id":"x","source":"crm","kitoffset":7}"#,
        br#"{"topic":7,"source":"crm"}"#,
    ];
    for f in frames {
        let got = decode_event_frame(f, true);
        assert!(
            matches!(got, Err(EventFrameError::Drop(_))),
            "{}: {got:?}",
            String::from_utf8_lossy(f)
        );
    }
}
