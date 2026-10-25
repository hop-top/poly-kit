//! The dialer's lifecycle, sans I/O: disconnect, dial ids and late or
//! stale replies, write failures and the delivery marker of async
//! handlers. Ports `network_reentry_test.go` logic by name; see
//! `bus_stream_client.rs`.

#![cfg(feature = "bus")]

mod bus_stream_support;

use bus_stream_support::client::*;
use hop_top_kit::bus::stream::{
    ClientConfig, Delivery, HandshakeError, Inbound, LinkResume, Request, RequestError, RingLog,
    StreamClient, StreamLog, StreamPosition, CLOSE_NORMAL,
};
use hop_top_kit::bus::Context;
use serde_json::json;

#[test]
fn subscribe_after_disconnect_does_not_recreate_the_peer() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let close = c.disconnect().expect("a link is up");
    assert_eq!(close.status, CLOSE_NORMAL);
    let down = c.link_down().expect("link was up");
    assert!(down.disconnected.is_final && !down.reconnect);
    let err = c
        .subscribe(&Context::background(), vec!["acme.#".into()])
        .unwrap_err();
    assert_eq!(err, RequestError::Disconnected(ADDR.into()));
    assert_eq!(
        err.to_string(),
        format!("bus: address disconnected during connect: {ADDR}")
    );
    assert!(c.subscription().implicit, "the set is not touched");
    // A reconnect timer that was already armed cannot bring it back.
    assert_eq!(
        c.begin_handshake("open").unwrap_err(),
        HandshakeError::Disconnected(ADDR.into())
    );
    assert!(c.begin_legacy().is_err());
    assert!(c.is_disconnected() && !c.link_state().connected);
}

#[test]
fn disconnect_during_handshake_refuses_the_ack() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    assert!(c.disconnect().is_some(), "the dial's socket is closed");
    let f = c.on_ack(id, &ack(1, 5, None), &log).unwrap_err();
    assert_eq!(f.error, HandshakeError::Disconnected(ADDR.into()));
    assert!(!f.publish);
    assert_eq!(f.close.map(|c| c.reason), Some("disconnect"));
    assert!(!c.link_state().connected && c.link_state().is_final);
    assert_eq!(c.position(), None);
}

#[test]
fn late_ack_after_timeout_is_refused_not_panicking() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    assert!(c.handshake_failed(id, HandshakeError::AckTimeout).publish);
    let f = c.on_ack(id, &ack(1, 0, None), &log).unwrap_err();
    assert_eq!(f.error, HandshakeError::Stale);
    assert!(!f.publish);
    assert!(!c.link_state().connected && !c.is_up());
    assert_eq!(c.link_state().code, "timeout");
}

#[test]
fn stale_ack_is_not_applied_to_a_later_handshake() {
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
    let first = hs(&mut c);
    c.handshake_failed(first, HandshakeError::AckTimeout);
    let second = hs(&mut c);
    // The first socket's reply arrives late: a reset that would move the
    // position to 57.
    let stale = ack(
        17,
        57,
        Some(json!({"status": "reset", "reason": "evicted", "after": "41", "head": "57"})),
    );
    assert_eq!(
        c.on_ack(first, &stale, &log).unwrap_err().error,
        HandshakeError::Stale
    );
    assert_eq!(c.position().unwrap().offset, 41, "nothing applied");
    let up = c
        .on_ack(
            second,
            &ack(
                17,
                57,
                Some(json!({"status": "replaying", "after": "41", "head": "57"})),
            ),
            &log,
        )
        .unwrap();
    assert_eq!(up.state.resume, Some(LinkResume::Replaying));
    // A stale timeout report changes nothing either.
    assert!(
        !c.handshake_failed(first, HandshakeError::AckTimeout)
            .publish
    );
    assert!(c.is_up());
}

#[test]
fn dial_while_in_flight_is_busy() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    assert_eq!(c.begin_handshake("open").unwrap_err(), HandshakeError::Busy);
    assert_eq!(c.begin_legacy().unwrap_err(), HandshakeError::Busy);
    c.on_ack(id, &ack(1, 0, None), &log).unwrap();
    assert_eq!(c.begin_handshake("open").unwrap_err(), HandshakeError::Busy);
}

#[test]
fn failed_handshakes_publish_once_and_link_down_adds_nothing() {
    let log = log();
    let mut c = client(cfg(), &log);
    let mut published = 0;
    for _ in 0..3 {
        let id = hs(&mut c);
        let f = c.on_ack(id, br#"{"type":"nope"}"#, &log).unwrap_err();
        published += usize::from(f.disconnected(ADDR).is_some());
        // The socket closes after the failed handshake.
        assert_eq!(c.link_down(), None);
    }
    assert_eq!(published, 1);
}

#[test]
fn repeat_suppression_never_hides_a_failure_after_a_final_one() {
    let log = log();
    let mut c = client(cfg(), &log);
    let id = hs(&mut c);
    // A final session error whose code happens to be "auth".
    let f = c
        .on_ack(
            id,
            br#"{"type":"session_error","code":"auth","message":"no"}"#,
            &log,
        )
        .unwrap_err();
    assert!(f.publish && c.link_state().is_final);
    // The next explicit connect's token rejection must be surfaced.
    let id = hs(&mut c);
    let f = c.on_ack(id, br#"{"type":"auth_no"}"#, &log).unwrap_err();
    assert_eq!(f.error, HandshakeError::AuthRejected);
    assert!(f.publish, "a retrying failure after a final one is news");
    assert!(!c.link_state().is_final);
}

#[test]
fn subscribe_from_async_handler_on_link_connected() {
    let log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    let delivery = c.delivery_context(&Context::background());
    let async_ctx = StreamClient::clear_delivery(&delivery);
    assert!(matches!(
        c.subscribe(&async_ctx, vec!["acme.billing.#".into()]),
        Ok(Request::Send { .. })
    ));
    assert!(matches!(
        c.subscribe(&delivery, vec!["acme.billing.#".into()]),
        Err(RequestError::Reentrant(_))
    ));
}

#[test]
fn write_failure_stops_the_writer_until_link_down() {
    let mut log = log();
    let mut c = client(
        ClientConfig {
            outbox: true,
            ..cfg()
        },
        &log,
    );
    connect(&mut c, &log, &ack(1, 0, None));
    log.append(event("e1"));
    let b = c.next_batch(&log).unwrap();
    assert!(c.write_failed(b.send[0].offset).is_some());
    assert!(c.is_up());
    assert_eq!(c.next_batch(&log), None, "no rewrite on the failing link");
    c.link_down().expect("link was up");
    connect(&mut c, &log, &ack(1, 0, None));
    assert_eq!(topics(&mut c, &log), ["e1"]);
}

#[test]
fn legacy_link_closes_on_the_third_write_failure() {
    let mut log: RingLog = log();
    let mut c = client(
        ClientConfig {
            handshake: false,
            ..cfg()
        },
        &log,
    );
    let id = c.begin_legacy().unwrap();
    c.link_up_legacy(id, &log).unwrap();
    log.append(event("e1"));
    assert_eq!(c.write_failed(1), None);
    c.write_ok();
    assert_eq!(c.write_failed(1), None);
    assert_eq!(c.write_failed(1), None);
    assert!(c.write_failed(1).is_some(), "third failure in a row");
    assert_eq!(c.next_batch(&log), None);
}

#[test]
fn late_ack_from_a_replaced_client_is_stale() {
    let log = log();
    let mut a = client(cfg(), &log);
    let id_a = hs(&mut a);
    a.disconnect();
    // A later explicit connect is a new client for the same address.
    let mut b = client(
        ClientConfig {
            subscribe: Some(vec!["only.b".into()]),
            ..cfg()
        },
        &log,
    );
    let id_b = hs(&mut b);
    assert_ne!(id_a, id_b, "handshake ids are unique across clients");
    // A transport routing by address hands the old socket's ack to b.
    let f = b.on_ack(id_a, &ack(1, 0, None), &log).unwrap_err();
    assert_eq!(f.error, HandshakeError::Stale);
    assert!(!f.publish && !b.is_up());
    let up = b.on_ack(id_b, &ack(1, 0, None), &log).unwrap();
    assert!(up.state.connected);
}

#[test]
fn disconnect_stops_the_live_link() {
    let mut log = log();
    let mut c = client(cfg(), &log);
    connect(&mut c, &log, &ack(1, 0, None));
    c.disconnect().expect("a link is up");
    log.append(event("e1"));
    assert_eq!(c.next_batch(&log), None, "nothing more is written");
    let frame = br#"{"topic":"a.b.c.d","offset":"1"}"#;
    assert_eq!(c.on_frame(frame).unwrap(), Inbound::Ignored);
    assert_eq!(c.on_event(None, "hub", "a.b.c.d").unwrap(), Delivery::Drop);
    assert_eq!(c.position().unwrap().offset, 0, "nothing more is delivered");
}
