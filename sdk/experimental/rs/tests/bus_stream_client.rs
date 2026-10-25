//! The dialer's link state machine, sans I/O: the handshake and
//! subscription requests. Ports the logic of the Go socket tests
//! (`network_handshake_test.go`, `network_reentry_test.go`,
//! `network_stream_test.go`) by name: the frames a Go acceptor would send
//! are fed in directly, and the decisions the Go adapter makes on them are
//! asserted. Socket timing, reconnect loops and cross-language runs belong
//! to the transport. Positions, liveness and the outbox are in
//! `bus_stream_client_link.rs`.

#![cfg(feature = "bus")]

mod bus_stream_support;

use bus_stream_support::client::*;
use hop_top_kit::bus::stream::{
    ClientConfig, Delivery, DeliveryMarker, FrameFormat, HandshakeError, Inbound, LinkResume,
    ProtocolKind, Request, RequestError, StreamCaps, CLOSE_PROTOCOL, CODE_BAD_REQUEST,
    CODE_FORBIDDEN, CODE_INVALID_PATTERN,
};
use hop_top_kit::bus::Context;
use serde_json::{json, Value};

#[test]
fn handshake_advertises_caps() {
    let log = log();
    let mut c = client(cfg(), &log);
    let (_, f) = auth_frame(&mut c);
    assert_eq!(f["auth"], "open");
    assert_eq!(
        f["caps"],
        json!({"streams": 1, "frames": ["kit", "cloudevents"]})
    );
    assert_eq!(f["origin"], "me");
    assert_eq!(f["session"], json!({}));
}

#[test]
fn handshake_no_auth_is_legacy() {
    let log = log();
    let mut c = client(
        ClientConfig {
            handshake: false,
            ..cfg()
        },
        &log,
    );
    let up = {
        let id = c.begin_legacy().unwrap();
        c.link_up_legacy(id, &log)
    }
    .unwrap();
    assert!(!up.state.streams);
    assert_eq!(up.state.resume, Some(LinkResume::Unsupported));
    assert_eq!(up.invalidated, None);
    assert_eq!(up.heartbeat_ms, None);
    let err = c
        .subscribe(&Context::background(), vec!["a.#".into()])
        .unwrap_err();
    assert_eq!(err, RequestError::StreamsUnsupported);
}

#[test]
fn handshake_no_hook_advertises_zero() {
    let log = log();
    let caps = StreamCaps {
        streams: 0,
        frames: vec![FrameFormat::Kit, FrameFormat::CloudEvents],
    };
    let mut c = client(ClientConfig { caps, ..cfg() }, &log);
    let (id, f) = auth_frame(&mut c);
    assert_eq!(f["caps"]["streams"], 0);
    // A v1 acceptor's stream info is ignored: the link is legacy.
    let up = c.on_ack(id, &ack(1, 0, None), &log).unwrap();
    assert!(!up.state.streams);
    assert_eq!(c.position(), None);
}

#[test]
fn handshake_no_hook_dialer_connects_to_ordered_hub() {
    let log = log();
    let caps = StreamCaps {
        streams: 0,
        frames: vec![FrameFormat::Kit, FrameFormat::CloudEvents],
    };
    let mut c = client(ClientConfig { caps, ..cfg() }, &log);
    let up = connect(&mut c, &log, &ack(1, 0, None));
    assert!(
        up.state.connected && !up.state.streams,
        "a connected legacy link"
    );
    // The hub sends this legacy link frames without an offset; the event
    // carries no position.
    let t = "acme.crm.deal.created";
    let frame = json!({"topic": t, "origin": "hub"}).to_string();
    assert_eq!(
        recv_event(&mut c, frame.as_bytes(), "hub", t),
        Delivery::Deliver(None)
    );
    assert_eq!(c.position(), None);
}

#[test]
fn frame_format_negotiated_per_peer() {
    let log = log();
    let mut c = client(
        ClientConfig {
            format: FrameFormat::CloudEvents,
            ..cfg()
        },
        &log,
    );
    assert_eq!(
        connect(&mut c, &log, &ack(1, 0, None)).state.format,
        FrameFormat::CloudEvents
    );
    c.link_down();
    let kit_only = br#"{"type":"auth_ok","caps":{"streams":1,"frames":["kit"]},"stream":{"id":"S","oldest":"1","head":"0","heartbeat_ms":1000}}"#;
    assert_eq!(
        connect(&mut c, &log, kit_only).state.format,
        FrameFormat::Kit
    );
}

#[test]
fn handshake_protocol_error_is_final_and_surfaced() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    let f = c
        .on_ack(
            id,
            br#"{"type":"auth_ok","caps":{"streams":1,"frames":["kit"]}}"#,
            &log,
        )
        .unwrap_err();
    assert!(matches!(&f.error, HandshakeError::Protocol(p) if p.kind == ProtocolKind::StreamInfo));
    assert_eq!(f.close.map(|c| c.status), Some(CLOSE_PROTOCOL));
    let d = f.disconnected(ADDR).expect("published");
    assert!(d.is_final);
    assert_eq!(
        d.payload(),
        json!({"peer": ADDR, "final": true, "code": "protocol"})
    );
    assert!(c.link_state().is_final);
    assert_eq!(c.link_state().code, "protocol");
}

#[test]
fn handshake_huge_heartbeat_is_rejected_not_panicking() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    let huge = br#"{"type":"auth_ok","caps":{"streams":1,"frames":["kit"]},"stream":{"id":"s","oldest":"1","head":"0","heartbeat_ms":10000000000000}}"#;
    let f = c.on_ack(id, huge, &log).unwrap_err();
    assert!(matches!(&f.error, HandshakeError::Protocol(p) if p.kind == ProtocolKind::Heartbeat));
    let d = f.disconnected(ADDR).unwrap();
    assert!(d.is_final);
    assert_eq!(d.code, "protocol");
}

#[test]
fn handshake_heartbeat_bounds() {
    for (ms, ok) in [
        (999u64, false),
        (1000, true),
        (86_400_000, true),
        (86_400_001, false),
    ] {
        let log = log();
        let mut c = client(cfg(), &log);
        let id = hs(&mut c);
        let a = format!(
            r#"{{"type":"auth_ok","caps":{{"streams":1,"frames":["kit"]}},"stream":{{"id":"S","oldest":"1","head":"0","heartbeat_ms":{ms}}}}}"#
        );
        match c.on_ack(id, a.as_bytes(), &log) {
            Ok(up) => assert!(ok && up.heartbeat_ms == Some(ms), "{ms}"),
            Err(f) => assert!(
                !ok && matches!(f.error, HandshakeError::Protocol(ref p) if p.kind == ProtocolKind::Heartbeat),
                "{ms}"
            ),
        }
    }
}

#[test]
fn handshake_auth_reject_is_surfaced_once_and_retryable() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    let first = c.on_ack(id, br#"{"type":"auth_no"}"#, &log).unwrap_err();
    assert_eq!(first.error, HandshakeError::AuthRejected);
    let d = first
        .disconnected(ADDR)
        .expect("first failure is published");
    assert_eq!(
        d.payload(),
        json!({"peer": ADDR, "final": false, "code": "auth"})
    );
    // The acceptor's 1008 close is the same failure: not published again.
    let id = hs(&mut c);
    let again = c.handshake_failed(id, HandshakeError::AuthRejected);
    assert_eq!(again.disconnected(ADDR), None);
    assert!(!c.link_state().is_final);
    let up = connect(&mut c, &log, &ack(1, 0, None));
    assert!(up.state.connected && !up.state.is_final);
}

#[test]
fn handshake_successful_connect_clears_final() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    let f = c
        .on_ack(
            id,
            br#"{"type":"session_error","code":"forbidden","message":"no"}"#,
            &log,
        )
        .unwrap_err();
    assert!(matches!(&f.error, HandshakeError::Session(e) if e.code == CODE_FORBIDDEN));
    assert!(f.disconnected(ADDR).unwrap().is_final);
    assert!(c.link_state().is_final);
    connect(&mut c, &log, &ack(1, 0, None));
    assert!(!c.link_state().is_final);
}

#[test]
fn handshake_ack_timeout_is_not_an_auth_failure() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    let f = c.handshake_failed(id, HandshakeError::AckTimeout);
    let d = f.disconnected(ADDR).unwrap();
    assert_eq!((d.is_final, d.code.as_str()), (false, "timeout"));
    assert!(!HandshakeError::AckTimeout.is_final());
}

#[test]
fn subscribe_replaces_implicit_all_and_ack_at() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let Request::Send { id, frame } = c
        .subscribe(&Context::background(), vec!["acme.billing.#".into()])
        .unwrap()
    else {
        panic!("link is up");
    };
    let f: Value = serde_json::from_slice(&frame).unwrap();
    assert_eq!(
        f,
        json!({"type": "subscribe", "id": id, "patterns": ["acme.billing.#"]})
    );
    assert!(c.subscription().implicit, "applied only on the ack");
    let reply = format!(r#"{{"type":"ack","id":"{id}","at":"7"}}"#);
    assert_eq!(
        c.on_frame(reply.as_bytes()).unwrap(),
        Inbound::Reply { id, result: Ok(7) }
    );
    assert_eq!(
        c.subscription().effective().patterns,
        vec!["acme.billing.#"]
    );
}

#[test]
fn subscribe_error_codes() {
    let log = log();
    let mut c = client(cfg(), &log);
    let bg = Context::background();
    let code = |r: Result<Request, RequestError>| match r {
        Err(RequestError::Stream(e)) => e.code,
        other => panic!("{other:?}"),
    };
    assert_eq!(code(c.subscribe(&bg, vec![])), CODE_BAD_REQUEST);
    assert_eq!(code(c.unsubscribe(&bg, vec![])), CODE_BAD_REQUEST);
    assert_eq!(
        code(c.subscribe(&bg, vec!["a.#.b".into()])),
        CODE_INVALID_PATTERN
    );
    connect(&mut c, &log, &ack(1, 0, None));
    let Request::Send { id, .. } = c.subscribe(&bg, vec!["a.#".into()]).unwrap() else {
        panic!("link is up");
    };
    let reply = format!(r#"{{"type":"error","id":"{id}","code":"forbidden","message":"no"}}"#);
    match c.on_frame(reply.as_bytes()).unwrap() {
        Inbound::Reply {
            result: Err(RequestError::Stream(e)),
            ..
        } => assert_eq!(e.code, CODE_FORBIDDEN),
        other => panic!("{other:?}"),
    }
    assert!(
        c.subscription().implicit,
        "a refused request changes nothing"
    );
}

#[test]
fn subscribe_while_down_updates_the_next_handshake() {
    let log = log();
    let mut c = client(
        ClientConfig {
            max_subscriptions: 2,
            ..cfg()
        },
        &log,
    );
    let bg = Context::background();
    assert_eq!(
        c.subscribe(&bg, vec!["a.#".into(), "b.#".into()]),
        Ok(Request::Applied)
    );
    match c.subscribe(&bg, vec!["c.#".into()]) {
        Err(RequestError::Stream(e)) => assert_eq!(e.code, "limit"),
        other => panic!("{other:?}"),
    }
    let (_, f) = auth_frame(&mut c);
    assert_eq!(f["session"]["subscribe"], json!(["a.#", "b.#"]));
}

#[test]
fn subscribe_resent_on_reconnect() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let bg = Context::background();
    let Request::Send { id, .. } = c.subscribe(&bg, vec!["acme.billing.#".into()]).unwrap() else {
        panic!("link is up");
    };
    c.on_frame(format!(r#"{{"type":"ack","id":"{id}","at":"0"}}"#).as_bytes())
        .unwrap();
    let down = c.link_down().expect("link was up");
    assert!(down.reconnect);
    let (_, f) = auth_frame(&mut c);
    assert_eq!(f["session"]["subscribe"], json!(["acme.billing.#"]));
    assert_eq!(f["session"]["resume"], json!({"stream": "S", "after": "0"}));
}

#[test]
fn subscribe_reentrant_from_link_connected() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let ctx = c.delivery_context(&Context::background());
    let err = c.subscribe(&ctx, vec!["acme.other.#".into()]).unwrap_err();
    assert_eq!(err, RequestError::Reentrant(ADDR.into()));
    assert_eq!(
        err.to_string(),
        format!("bus: request from a delivery of the same link: {ADDR}")
    );
}

#[test]
fn subscribe_reentrant_from_invalidated() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 10, None));
    let reset = br#"{"type":"reset","stream":"S","reason":"lagged","after":"10","head":"20"}"#;
    assert!(matches!(
        c.on_frame(reset).unwrap(),
        Inbound::Invalidated(_)
    ));
    let ctx = c.delivery_context(&Context::background());
    assert!(matches!(
        c.set_scope(&ctx, Default::default()),
        Err(RequestError::Reentrant(_))
    ));
}

#[test]
fn subscribe_reentrant_from_delivered_event() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let d = recv_event(
        &mut c,
        &kit_frame("acme.crm.deal.created", "1"),
        "hub",
        "acme.crm.deal.created",
    );
    assert!(matches!(d, Delivery::Deliver(Some(_))));
    let ctx = c.delivery_context(&Context::background());
    assert!(matches!(
        c.unsubscribe(&ctx, vec!["#".into()]),
        Err(RequestError::Reentrant(_))
    ));
    // A delivery from another address is not reentrant.
    let other = Context::background().with_value(DeliveryMarker("ws://other".into()));
    assert!(matches!(
        c.subscribe(&other, vec!["a.#".into()]),
        Ok(Request::Send { .. })
    ));
}

#[test]
fn subscribe_reentrant_with_background_unblocks_on_close() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let Request::Send { id, .. } = c
        .subscribe(&Context::background(), vec!["a.#".into()])
        .unwrap()
    else {
        panic!("link is up");
    };
    let down = c.link_down().expect("link was up");
    assert_eq!(down.failed, vec![id.clone()]);
    // A late reply for the failed request is ignored.
    assert!(c.subscription().implicit);
    assert_eq!(
        RequestError::LinkClosed.to_string(),
        "bus: link closed before the reply"
    );
}
