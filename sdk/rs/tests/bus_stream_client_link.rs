//! The dialer's link state machine, sans I/O: resume, liveness, inbound
//! events and the outbox. Ports the logic of the Go socket tests
//! (`network_resume_test.go`, `network_reentry_test.go`) by name; see
//! `bus_stream_client.rs`.

#![cfg(feature = "bus")]

mod bus_stream_support;

use bus_stream_support::client::*;
use hop_top_kit::bus::stream::{
    ClientConfig, Delivery, HandshakeError, Inbound, LinkResume, LogEntry, ProtocolKind, RingLog,
    StreamClient, StreamLog, StreamPosition, CLOSE_PROTOCOL,
};
use serde_json::json;

#[test]
fn resume_replays_missed_in_order() {
    let log = log();
    let seed = StreamPosition {
        peer: ADDR.into(),
        stream: "S".into(),
        offset: 41,
    };
    let mut c = client(
        ClientConfig {
            resume_from: Some(seed),
            ..cfg()
        },
        &log,
    );
    let (id, f) = auth_frame(&mut c);
    assert_eq!(
        f["session"]["resume"],
        json!({"stream": "S", "after": "41"})
    );
    let up = c
        .on_ack(
            id,
            &ack(
                17,
                57,
                Some(json!({"status": "replaying", "after": "41", "head": "57"})),
            ),
            &log,
        )
        .unwrap();
    assert_eq!(
        (up.state.resume, up.invalidated),
        (Some(LinkResume::Replaying), None)
    );
    for (off, want) in [("42", true), ("42", false), ("43", true)] {
        let d = recv_event(&mut c, &kit_frame("a.b.c.d", off), "hub", "a.b.c.d");
        assert_eq!(
            matches!(d, Delivery::Deliver(Some(_))),
            want,
            "offset {off}"
        );
    }
    assert_eq!(c.position().unwrap().offset, 43);
}

#[test]
fn resume_out_of_range_sends_reset() {
    let log = log();
    let seed = StreamPosition {
        peer: ADDR.into(),
        stream: "S".into(),
        offset: 5,
    };
    let mut c = client(
        ClientConfig {
            resume_from: Some(seed),
            ..cfg()
        },
        &log,
    );
    let up = connect(
        &mut c,
        &log,
        &ack(
            17,
            57,
            Some(json!({"status": "reset", "reason": "evicted", "after": "5", "head": "57"})),
        ),
    );
    let g = up.invalidated.unwrap();
    assert_eq!(
        g.payload(ADDR),
        json!({"peer": ADDR, "reason": "evicted", "stream": "S", "after": "5", "head": "57"})
    );
    assert_eq!(c.position().unwrap().offset, 57);
}

fn seeded(stream: &str, offset: u64, log: &RingLog, handshake: bool) -> StreamClient {
    let seed = StreamPosition {
        peer: ADDR.into(),
        stream: stream.into(),
        offset,
    };
    let cfg = ClientConfig {
        resume_from: Some(seed),
        handshake,
        ..cfg()
    };
    client(cfg, log)
}

#[test]
fn resume_ahead_reset_moves_position_to_head() {
    let log = log();
    let mut c = seeded("S", 99, &log, true);
    let reset = json!({"status": "reset", "reason": "ahead", "after": "99", "head": "3"});
    let up = connect(&mut c, &log, &ack(1, 3, Some(reset)));
    assert_eq!(up.invalidated.unwrap().reason, "ahead");
    assert_eq!(c.position().unwrap().offset, 3);
    let t = "acme.crm.deal.updated";
    match recv_event(&mut c, &kit_frame(t, "4"), "hub", t) {
        Delivery::Deliver(Some(p)) => assert_eq!(p.offset, 4),
        other => panic!("{other:?}"),
    }
}

#[test]
fn resume_stream_restart_sends_reset() {
    let log = log();
    let mut c = seeded("stm_gone", 4, &log, true);
    let reset = json!({"status": "reset", "reason": "stream", "after": "4", "head": "0"});
    let up = connect(&mut c, &log, &ack(1, 0, Some(reset)));
    assert_eq!(up.state.resume, Some(LinkResume::Reset));
    assert_eq!(up.connected_payload(ADDR)["resume"], "reset");
    let g = up.invalidated.unwrap();
    assert_eq!((g.reason.as_str(), g.stream.as_str()), ("stream", "S"));
}

#[test]
fn resume_first_connect_to_legacy_with_position_invalidates() {
    let log = log();
    let mut c = seeded("stm_x", 7, &log, false);
    let id = c.begin_legacy().unwrap();
    let up = c.link_up_legacy(id, &log).unwrap();
    assert_eq!(up.state.resume, Some(LinkResume::Unsupported));
    let p = up.invalidated.unwrap().payload(ADDR);
    assert_eq!(
        (p["reason"].as_str(), p["stream"].as_str()),
        (Some("unsupported"), Some(""))
    );
    assert_eq!(c.position(), None, "a legacy link clears the position");
}

#[test]
fn outbox_offline_start_keeps_events() {
    let mut log = log();
    let mut c = client(
        ClientConfig {
            outbox: true,
            ..cfg()
        },
        &log,
    );
    log.append(event("acme.crm.deal.created"));
    log.append(event("acme.crm.deal.updated"));
    // The hub refuses the first dials; events wait in the outbox.
    let id = hs(&mut c);
    c.on_ack(id, br#"{"type":"auth_no"}"#, &log).unwrap_err();
    assert_eq!(c.next_batch(&log), None);
    connect(&mut c, &log, &ack(1, 0, None));
    assert_eq!(
        topics(&mut c, &log),
        ["acme.crm.deal.created", "acme.crm.deal.updated"]
    );
}

#[test]
fn resume_reconnect_to_legacy_invalidates() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 3, None));
    c.link_down();
    let up = connect(&mut c, &log, br#"{"type":"auth_ok"}"#);
    assert_eq!(up.state.resume, Some(LinkResume::Unsupported));
    assert_eq!(up.invalidated.unwrap().reason, "unsupported");
    assert_eq!(c.position(), None);
}

#[test]
fn resume_unrequested_outcome_is_final() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    let f = c
        .on_ack(
            id,
            &ack(
                1,
                57,
                Some(json!({"status": "replaying", "after": "0", "head": "57"})),
            ),
            &log,
        )
        .unwrap_err();
    assert!(matches!(&f.error, HandshakeError::Protocol(p) if p.kind == ProtocolKind::Unrequested));
    assert!(c.link_state().is_final);
    assert_eq!(c.position(), None);
}

#[test]
fn stream_dead_link_detected_after_three_silent_ticks() {
    let log = log();
    let mut c = client(cfg(), &log);
    let up = connect(&mut c, &log, &ack(1, 0, None));
    assert_eq!(up.heartbeat_ms, Some(15000));
    assert_eq!(c.heartbeat_tick(), None);
    assert_eq!(c.heartbeat_tick(), None);
    let close = c.heartbeat_tick().expect("dead after three silent ticks");
    assert_eq!(close.reason, "heartbeat timeout");
    let down = c.link_down().expect("link was up");
    assert_eq!(
        down.disconnected.payload(),
        json!({"peer": ADDR, "final": false, "code": "timeout"})
    );
    assert!(down.reconnect);
}

#[test]
fn stream_heartbeat_keeps_quiet_link_alive() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    for _ in 0..10 {
        assert_eq!(c.heartbeat_tick(), None);
        c.on_frame(br#"{"type":"position","stream":"S","offset":"0"}"#)
            .unwrap();
    }
}

#[test]
fn stream_missing_offset_is_a_protocol_error() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let Inbound::Event(f) = c.on_frame(br#"{"topic":"a.b.c.d"}"#).unwrap() else {
        panic!("event");
    };
    let fail = c.on_event(f.offset_member(), "hub", "a.b.c.d").unwrap_err();
    assert_eq!(fail.error.kind, ProtocolKind::Offset);
    assert_eq!(fail.close.status, CLOSE_PROTOCOL);
    let down = c.link_down().expect("link was up");
    assert_eq!(down.disconnected.code, "protocol");
    assert!(
        down.reconnect,
        "a protocol error on a live link is not final"
    );
}

#[test]
fn stream_reserved_topics_dropped_inbound() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let t = "kit.runtime.link.connected";
    assert_eq!(
        recv_event(&mut c, &kit_frame(t, "1"), "hub", t),
        Delivery::Drop
    );
    assert_eq!(
        recv_event(&mut c, &kit_frame("a.b.c.d", "2"), "me", "a.b.c.d"),
        Delivery::Drop,
        "own echo"
    );
    assert_eq!(
        c.position().unwrap().offset,
        0,
        "dropped events do not move the position"
    );
}

#[test]
fn stream_disconnect_is_final() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    c.disconnect();
    let down = c.link_down().expect("link was up");
    assert!(down.disconnected.is_final && !down.reconnect);
}

#[test]
fn outbox_flushes_in_order_after_reconnect() {
    let mut log = log();
    let mut c = client(
        ClientConfig {
            outbox: true,
            ..cfg()
        },
        &log,
    );
    log.append(event("e1"));
    connect(&mut c, &log, &ack(1, 0, None));
    assert_eq!(topics(&mut c, &log), ["e1"]);
    c.link_down();
    log.append(event("e2"));
    log.append(event("e3"));
    assert_eq!(c.next_batch(&log), None, "nothing is sent while down");
    connect(&mut c, &log, &ack(1, 0, None));
    assert_eq!(topics(&mut c, &log), ["e2", "e3"]);
}

#[test]
fn outbox_overflow_drops_oldest_and_counts() {
    let mut log = RingLog::new("L", 3);
    let mut c = client(
        ClientConfig {
            outbox: true,
            ..cfg()
        },
        &log,
    );
    for t in ["e1", "e2", "e3", "e4", "e5"] {
        log.append(event(t));
    }
    connect(&mut c, &log, &ack(1, 0, None));
    assert_eq!(topics(&mut c, &log), ["e3", "e4", "e5"]);
    assert_eq!((c.outbox_dropped(), c.skipped()), (2, 0));
}

#[test]
fn outbox_off_keeps_drop_behaviour() {
    let mut log = log();
    let mut c = client(cfg(), &log);
    log.append(event("e1"));
    connect(&mut c, &log, &ack(1, 0, None));
    log.append(event("e2"));
    assert_eq!(topics(&mut c, &log), ["e2"]);
    c.link_down();
    log.append(event("e3"));
    connect(&mut c, &log, &ack(1, 0, None));
    log.append(event("e4"));
    assert_eq!(topics(&mut c, &log), ["e4"]);
}

#[test]
fn outbox_write_failure_keeps_the_frame() {
    let mut log = log();
    let mut c = client(
        ClientConfig {
            outbox: true,
            ..cfg()
        },
        &log,
    );
    connect(&mut c, &log, &ack(1, 0, None));
    log.append(event("acme.crm.deal.created"));
    log.append(event("acme.crm.deal.updated"));
    let b = c.next_batch(&log).unwrap();
    c.write_failed(b.send[0].offset); // the first write fails, the link closes
    c.link_down();
    connect(&mut c, &log, &ack(1, 0, None));
    assert_eq!(
        topics(&mut c, &log),
        ["acme.crm.deal.created", "acme.crm.deal.updated"]
    );
}

#[test]
fn outbox_off_loses_a_failed_write() {
    let mut log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    log.append(event("e1"));
    let b = c.next_batch(&log).unwrap();
    c.write_failed(b.send[0].offset);
    c.link_down();
    connect(&mut c, &log, &ack(1, 0, None));
    assert!(topics(&mut c, &log).is_empty());
}

#[test]
fn legacy_skips_are_counted() {
    let mut log = RingLog::new("L", 2);
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    for t in ["e1", "e2", "e3", "e4"] {
        log.append(event(t));
    }
    assert_eq!(topics(&mut c, &log), ["e3", "e4"]);
    assert_eq!((c.skipped(), c.outbox_dropped()), (2, 0));
}

#[test]
fn writer_never_echoes_the_peer_origin() {
    let mut log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None)); // the hub's origin is "hub"
    log.append(LogEntry {
        origin: "hub".into(),
        ..event("from.hub.x.created")
    });
    log.append(event("local.a.b.created"));
    log.append(LogEntry {
        marker: true,
        ..event("")
    });
    assert_eq!(topics(&mut c, &log), ["local.a.b.created"]);
}
