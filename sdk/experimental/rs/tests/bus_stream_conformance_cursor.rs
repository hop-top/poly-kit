//! Runs the stream vectors for the dialer cursor, the bounded inbound
//! queue and the outbox. Go is canonical
//! (`stream_conformance_cursor_test.go`). Every failing cursor case names
//! its protocol kind, and the runner asserts that exact kind.

#![cfg(feature = "bus")]

mod bus_stream_support;

use std::time::UNIX_EPOCH;

use bus_stream_support::{check, load, must_offset, non_empty, present, stream_file};
use hop_top_kit::bus::stream::{
    classify_frame, dialer_control, format_offset, parse_ack, Ack, DialerCtl, DialerCursor,
    GapInfo, InboundQueue, LogEntry, RingLog, SendCursor, StreamLog, STREAMS_VERSION,
};
use hop_top_kit::bus::Event;
use serde::Deserialize;
use serde_json::{json, Value};

#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct ConfPos {
    stream: String,
    offset: String,
}

stream_file!(CursorFile { cases: Vec<CursorCase>, });
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CursorCase {
    name: String,
    seed: Option<ConfPos>,
    reconnect: bool,
    link: String,
    #[serde(default)]
    ack: Option<Value>,
    frames: Vec<CursorFrame>,
    expect: CursorExpect,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CursorFrame {
    #[serde(default, deserialize_with = "present")]
    event: Option<Value>,
    #[serde(default, deserialize_with = "present")]
    reset: Option<Value>,
    #[serde(default, deserialize_with = "present")]
    position: Option<Value>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CursorExpect {
    link_up: Value,
    frames: Vec<Value>,
    position: Option<ConfPos>,
}

fn conf_gap(g: &Option<GapInfo>) -> Value {
    match g {
        None => Value::Null,
        Some(g) => {
            let p = g.payload("");
            json!({"reason": p["reason"], "stream": p["stream"], "after": p["after"], "head": p["head"]})
        }
    }
}

fn protocol(kind: &str) -> Value {
    json!({ "protocol_error": kind })
}

/// Turns a control-frame body from a vector into its members, typed.
fn control(body: &Value, typ: &str) -> serde_json::Map<String, Value> {
    let mut m = body.as_object().cloned().unwrap_or_default();
    m.insert("type".into(), Value::String(typ.into()));
    let raw = serde_json::to_vec(&m).unwrap();
    let (_, members) = classify_frame(&raw, true);
    members.expect("control frame")
}

#[test]
fn stream_conformance_cursor() {
    let f: CursorFile = load("cursor.json");
    non_empty(&f.cases, "cursor cases");
    let mut fails = Vec::new();
    for tc in &f.cases {
        let name = &tc.name;
        let mut c = DialerCursor::default();
        if let Some(s) = &tc.seed {
            c = DialerCursor {
                stream: s.stream.clone(),
                offset: must_offset(&s.offset),
                held: true,
            };
        }
        let ack = match tc.link.as_str() {
            "stream" => {
                // The vector's ack is the reply body minus type/caps: wrap
                // it into a real extended ack and decode it.
                let body = tc.ack.as_ref().expect("vector: stream link needs an ack");
                let mut si = body["stream"].clone();
                si["heartbeat_ms"] = json!(1000);
                let mut frame = json!({"type": "auth_ok", "caps": {"streams": 1, "frames": ["kit"]}, "stream": si});
                if let Some(r) = body.get("resume") {
                    frame["session"] = json!({ "resume": r });
                }
                match parse_ack(frame.to_string().as_bytes(), STREAMS_VERSION) {
                    Ok(Ack::AuthOk(a)) => Some(a),
                    other => panic!("{name}: ack {other:?}"),
                }
            }
            "legacy" => None,
            other => panic!("{name}: vector link {other:?}"),
        };
        let sent = c.request();
        let link_up = match c.link_up(ack.as_ref(), sent.as_ref(), tc.reconnect) {
            Ok((resume, gap)) => json!({"resume": resume.as_str(), "invalidated": conf_gap(&gap)}),
            Err(e) => protocol(e.kind.as_str()),
        };
        if link_up != tc.expect.link_up {
            fails.push(format!(
                "{name}: link_up {link_up}, want {}",
                tc.expect.link_up
            ));
        }
        assert_eq!(
            tc.frames.len(),
            tc.expect.frames.len(),
            "{name}: vector frames and expectations differ in length"
        );
        for (i, (fr, want)) in tc.frames.iter().zip(&tc.expect.frames).enumerate() {
            let got = if let Some(raw) = &fr.event {
                match c.on_event_offset(Some(raw)) {
                    Err(e) => protocol(e.kind.as_str()),
                    Ok(true) => json!("deliver"),
                    Ok(false) => json!("drop"),
                }
            } else if let Some(body) = &fr.reset {
                let r = dialer_control(&control(body, "reset")).and_then(|dc| match dc {
                    DialerCtl::Reset(r) => c.on_reset(&r),
                    other => panic!("{name}: reset decoded as {other:?}"),
                });
                match r {
                    Ok(g) => json!({ "invalidated": conf_gap(&Some(g)) }),
                    Err(e) => protocol(e.kind.as_str()),
                }
            } else if let Some(body) = &fr.position {
                let r = dialer_control(&control(body, "position")).and_then(|dc| match dc {
                    DialerCtl::Position(p) => c.on_position(&p),
                    other => panic!("{name}: position decoded as {other:?}"),
                });
                match r {
                    Ok(()) => json!("ok"),
                    Err(e) => protocol(e.kind.as_str()),
                }
            } else {
                panic!("{name}: vector frame {i} is empty");
            };
            if &got != want {
                fails.push(format!("{name}: frame {i}: {got}, want {want}"));
            }
        }
        let pos = c.held.then(|| ConfPos {
            stream: c.stream.clone(),
            offset: format_offset(c.offset),
        });
        if pos != tc.expect.position {
            fails.push(format!(
                "{name}: position {pos:?}, want {:?}",
                tc.expect.position
            ));
        }
    }
    check(fails);
}

stream_file!(InboundFile { cases: Vec<InboundCase>, });
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct InboundCase {
    name: String,
    after: String,
    capacity: usize,
    ops: Vec<InboundOp>,
    delivered: Vec<String>,
    overflow: bool,
    resume_after: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct InboundOp {
    #[serde(default)]
    recv: Option<String>,
    #[serde(default)]
    deliver: Option<usize>,
}

#[test]
fn stream_conformance_inbound() {
    let f: InboundFile = load("inbound.json");
    non_empty(&f.cases, "inbound cases");
    let mut fails = Vec::new();
    for tc in &f.cases {
        let mut q = InboundQueue::new(tc.capacity, must_offset(&tc.after));
        let mut delivered = Vec::new();
        for op in &tc.ops {
            match (&op.recv, op.deliver) {
                (Some(r), None) => q.recv(must_offset(r)),
                (None, Some(n)) => delivered.extend(q.deliver(n).into_iter().map(format_offset)),
                _ => panic!("{}: vector op needs exactly one of recv, deliver", tc.name),
            }
        }
        if delivered != tc.delivered {
            fails.push(format!("{}: delivered {delivered:?}", tc.name));
        }
        if q.overflowed() != tc.overflow {
            fails.push(format!("{}: overflow {}", tc.name, q.overflowed()));
        }
        if format_offset(q.resume_after()) != tc.resume_after {
            fails.push(format!("{}: resume after {}", tc.name, q.resume_after()));
        }
    }
    check(fails);
}

stream_file!(OutboxFile { cases: Vec<OutboxCase>, });
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct OutboxCase {
    name: String,
    capacity: usize,
    outbox: bool,
    ops: Vec<OutboxOp>,
    sent: Vec<String>,
    dropped: u64,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct OutboxOp {
    #[serde(default)]
    publish: Option<String>,
    #[serde(default)]
    connect: bool,
    #[serde(default)]
    disconnect: bool,
}

#[test]
fn stream_conformance_outbox() {
    let f: OutboxFile = load("outbox.json");
    non_empty(&f.cases, "outbox cases");
    let mut fails = Vec::new();
    for tc in &f.cases {
        let mut log = RingLog::new("S", tc.capacity);
        let mut ob = SendCursor::new(&log, tc.outbox);
        let mut sent = Vec::new();
        let mut dropped = 0u64;
        let mut connected = false;
        let drain =
            |ob: &mut SendCursor, log: &RingLog, sent: &mut Vec<String>, dropped: &mut u64| {
                let (entries, gap) = ob.next(log, 1 << 20);
                *dropped += gap;
                sent.extend(entries.into_iter().map(|e| e.event.topic.0));
            };
        for op in &tc.ops {
            match (&op.publish, op.connect, op.disconnect) {
                (Some(t), false, false) => {
                    log.append(LogEntry {
                        offset: 0,
                        event: Event::new_at(t.as_str(), "", Value::Null, UNIX_EPOCH),
                        origin: String::new(),
                        marker: false,
                    });
                    if connected {
                        drain(&mut ob, &log, &mut sent, &mut dropped);
                    }
                }
                (None, true, false) => {
                    connected = true;
                    ob.link_up(&log);
                    drain(&mut ob, &log, &mut sent, &mut dropped);
                }
                (None, false, true) => connected = false,
                _ => panic!("{}: vector op needs exactly one action", tc.name),
            }
        }
        if sent != tc.sent {
            fails.push(format!("{}: sent {sent:?}, want {:?}", tc.name, tc.sent));
        }
        if dropped != tc.dropped {
            fails.push(format!(
                "{}: dropped {dropped}, want {}",
                tc.name, tc.dropped
            ));
        }
    }
    check(fails);
}
