//! Acknowledged publishes through the dialer's link state machine:
//! negotiation, pubacks (cumulative), rewind on reconnect, and every way
//! a wait settles. Ports the dialer half of Go's `network_ack_test.go`;
//! the frames a Go acceptor sends are fed in directly.

#![cfg(feature = "bus")]

mod bus_stream_support;

use bus_stream_support::client::*;
use hop_top_kit::bus::stream::{
    AckResolution, ClientConfig, Inbound, ProtocolKind, RingLog, StreamClient, StreamLog,
    ACK_EVICTED, ACK_REJECTED, ACK_UNAVAILABLE, ACK_UNSUPPORTED, CLOSE_PROTOCOL,
};
use serde_json::json;

fn acks_cfg(log: &RingLog) -> ClientConfig {
    cfg().with_acks(log.id())
}

/// An extended ack from an acceptor that echoes `caps.acks`.
fn acks_ack(head: u64) -> Vec<u8> {
    json!({
        "type": "auth_ok",
        "caps": {"streams": 1, "frames": ["kit", "cloudevents"], "acks": 1},
        "origin": "hub",
        "stream": {"id": "S", "oldest": "1", "head": head.to_string(), "heartbeat_ms": 15000},
    })
    .to_string()
    .into_bytes()
}

fn publish(log: &mut RingLog, c: &mut StreamClient, topic: &str, acked: bool) -> u64 {
    let off = log.append(event(topic));
    if acked {
        c.expect_ack(off, topic);
    }
    off
}

/// Drains the writer: topics sent and waits the batches settled.
fn drain(c: &mut StreamClient, log: &RingLog) -> (Vec<String>, Vec<AckResolution>) {
    let (mut sent, mut acks) = (Vec::new(), Vec::new());
    while let Some(b) = c.next_batch(log) {
        sent.extend(b.send.into_iter().map(|e| e.event.topic.0));
        acks.extend(b.acks);
    }
    (sent, acks)
}

fn puback(off: u64, status: &str, code: Option<&str>) -> Vec<u8> {
    let mut f = json!({"type": "puback", "offset": off.to_string(), "status": status});
    if let Some(code) = code {
        f["code"] = json!(code);
    }
    f.to_string().into_bytes()
}

fn acked(c: &mut StreamClient, frame: &[u8]) -> Vec<(u64, String)> {
    match c.on_frame(frame).expect("puback") {
        Inbound::Acked(rs) => rs.into_iter().map(|r| (r.offset, r.code)).collect(),
        other => panic!("not acked: {other:?}"),
    }
}

#[test]
fn with_acks_advertises_acks_and_the_stream_and_keeps_an_outbox() {
    let log = log();
    let cfg = acks_cfg(&log);
    assert!(cfg.outbox);
    let mut c = client(cfg, &log);
    let (_, f) = auth_frame(&mut c);
    assert_eq!(f["caps"]["acks"], 1);
    assert_eq!(f["stream"], json!({"id": "stm_local"}));
}

#[test]
fn without_acks_the_auth_frame_is_unchanged() {
    let log = log();
    let mut c = client(cfg(), &log);
    let (_, f) = auth_frame(&mut c);
    assert_eq!(f["caps"].get("acks"), None);
    assert_eq!(f.get("stream"), None);
}

#[test]
fn puback_settles_its_frame() {
    let mut log = log();
    let mut c = client(acks_cfg(&log), &log);
    let up = connect(&mut c, &log, &acks_ack(0));
    assert!(up.acks && c.is_acks());
    let off = publish(&mut log, &mut c, "a.b.c.d", true);
    assert_eq!(drain(&mut c, &log).0, ["a.b.c.d"]);
    assert_eq!(
        acked(&mut c, &puback(off, "ok", None)),
        [(off, String::new())]
    );
    assert_eq!(c.acked(), off);
}

#[test]
fn cumulative_puback_settles_every_earlier_frame() {
    let mut log = log();
    let mut c = client(acks_cfg(&log), &log);
    connect(&mut c, &log, &acks_ack(0));
    let first = publish(&mut log, &mut c, "a.first", true);
    publish(&mut log, &mut c, "a.plain", false);
    let last = publish(&mut log, &mut c, "a.last", true);
    drain(&mut c, &log);
    assert_eq!(
        acked(&mut c, &puback(last, "err", Some(ACK_REJECTED))),
        [(first, String::new()), (last, ACK_REJECTED.to_string())]
    );
}

#[test]
fn reconnect_sends_again_after_the_last_acknowledged_offset() {
    let mut log = log();
    let mut c = client(acks_cfg(&log), &log);
    connect(&mut c, &log, &acks_ack(0));
    let one = publish(&mut log, &mut c, "a.one", true);
    let two = publish(&mut log, &mut c, "a.two", true);
    drain(&mut c, &log);
    acked(&mut c, &puback(one, "ok", None));
    c.link_down();
    connect(&mut c, &log, &acks_ack(0));
    assert_eq!(
        drain(&mut c, &log).0,
        ["a.two"],
        "lost frame goes out again"
    );
    assert_eq!(
        acked(&mut c, &puback(two, "ok", None)),
        [(two, String::new())]
    );
}

#[test]
fn a_link_without_acks_fails_waits_unsupported() {
    let mut log = log();
    let mut c = client(acks_cfg(&log), &log);
    let off = publish(&mut log, &mut c, "a.b.c.d", true);
    // The acceptor did not echo acks: an older hub.
    let up = connect(&mut c, &log, &ack(1, 0, None));
    assert!(!up.acks);
    assert_eq!(up.settled.len(), 1);
    assert_eq!(up.settled[0].offset, off);
    assert_eq!(up.settled[0].code, ACK_UNSUPPORTED);
    let later = publish(&mut log, &mut c, "a.later", true);
    let (_, acks) = drain(&mut c, &log);
    assert_eq!(acks.len(), 1);
    assert_eq!(
        (acks[0].offset, acks[0].code.as_str()),
        (later, ACK_UNSUPPORTED)
    );
    // A puback on such a link is not for us.
    assert_eq!(
        c.on_frame(&puback(later, "ok", None)).unwrap(),
        Inbound::Ignored
    );
}

#[test]
fn evicted_before_sent() {
    let mut log = RingLog::new("stm_local", 2);
    let mut c = client(acks_cfg(&log), &log);
    let off = publish(&mut log, &mut c, "a.one", true);
    publish(&mut log, &mut c, "a.two", false);
    publish(&mut log, &mut c, "a.three", false);
    connect(&mut c, &log, &acks_ack(0));
    let (sent, acks) = drain(&mut c, &log);
    assert_eq!(sent, ["a.two", "a.three"]);
    assert_eq!(acks.len(), 1);
    assert_eq!((acks[0].offset, acks[0].code.as_str()), (off, ACK_EVICTED));
}

#[test]
fn fail_acks_settles_every_wait() {
    let mut log = log();
    let mut c = client(acks_cfg(&log), &log);
    connect(&mut c, &log, &acks_ack(0));
    let one = publish(&mut log, &mut c, "a.one", true);
    drain(&mut c, &log);
    let two = publish(&mut log, &mut c, "a.two", true);
    c.disconnect();
    let rs: Vec<_> = c
        .fail_acks(ACK_UNAVAILABLE)
        .into_iter()
        .map(|r| (r.offset, r.code))
        .collect();
    assert_eq!(
        rs,
        [
            (one, ACK_UNAVAILABLE.to_string()),
            (two, ACK_UNAVAILABLE.to_string())
        ]
    );
}

#[test]
fn bad_pubacks_break_the_link() {
    for (frame, kind) in [
        (
            br#"{"type":"puback","offset":"1","status":"err"}"#.to_vec(),
            ProtocolKind::PubackMalformed,
        ),
        (puback(9, "ok", None), ProtocolKind::PubackAhead),
        (
            br#"{"type":"puback","offset":"1","id":"other","status":"ok"}"#.to_vec(),
            ProtocolKind::PubackId,
        ),
    ] {
        let mut log = log();
        let mut c = client(acks_cfg(&log), &log);
        connect(&mut c, &log, &acks_ack(0));
        publish(&mut log, &mut c, "a.one", true);
        drain(&mut c, &log);
        let err = c.on_frame(&frame).unwrap_err();
        assert_eq!(err.error.kind, kind);
        assert_eq!(err.close.status, CLOSE_PROTOCOL);
    }
}
