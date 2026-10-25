//! Helpers shared by the stream client tests: a log, a client, the acks a
//! Go acceptor sends, and the transport's decode step.

use std::time::UNIX_EPOCH;

use hop_top_kit::bus::stream::{
    ClientConfig, Delivery, HandshakeId, Inbound, LinkUp, LogEntry, RingLog, StreamClient,
};
use hop_top_kit::bus::Event;
use serde_json::{json, Value};

pub const ADDR: &str = "ws://hub";

pub fn log() -> RingLog {
    RingLog::new("stm_local", 8)
}

pub fn client(cfg: ClientConfig, log: &RingLog) -> StreamClient {
    StreamClient::new(cfg, log)
}

pub fn cfg() -> ClientConfig {
    ClientConfig::new(ADDR, "me")
}

/// An extended ack from a v1 acceptor with stream `S`.
pub fn ack(oldest: u64, head: u64, resume: Option<Value>) -> Vec<u8> {
    let mut a = json!({
        "type": "auth_ok",
        "caps": {"streams": 1, "frames": ["kit", "cloudevents"]},
        "origin": "hub",
        "stream": {"id": "S", "oldest": oldest.to_string(), "head": head.to_string(), "heartbeat_ms": 15000},
    });
    if let Some(r) = resume {
        a["session"] = json!({ "resume": r });
    }
    a.to_string().into_bytes()
}

pub fn connect(c: &mut StreamClient, log: &RingLog, ack: &[u8]) -> LinkUp {
    let id = hs(c);
    c.on_ack(id, ack, log).expect("link up")
}

/// Starts a handshake, returning its id.
pub fn hs(c: &mut StreamClient) -> HandshakeId {
    c.begin_handshake("open").expect("dial").0
}

/// Starts a handshake, returning its id and the decoded auth frame.
pub fn auth_frame(c: &mut StreamClient) -> (HandshakeId, Value) {
    let (id, f) = c.begin_handshake("open").expect("dial");
    (id, serde_json::from_slice(&f).unwrap())
}

pub fn event(topic: &str) -> LogEntry {
    LogEntry {
        offset: 0,
        event: Event::new_at(topic, "t", Value::Null, UNIX_EPOCH),
        origin: String::new(),
        marker: false,
    }
}

pub fn kit_frame(topic: &str, offset: &str) -> Vec<u8> {
    json!({"topic": topic, "offset": offset, "origin": "hub"})
        .to_string()
        .into_bytes()
}

/// Feeds an event frame and decides on it, as the transport would.
pub fn recv_event(c: &mut StreamClient, data: &[u8], origin: &str, topic: &str) -> Delivery {
    match c.on_frame(data).expect("frame") {
        Inbound::Event(f) => c.on_event(f.offset_member(), origin, topic).expect("event"),
        other => panic!("not an event: {other:?}"),
    }
}

pub fn topics(c: &mut StreamClient, log: &RingLog) -> Vec<String> {
    let mut out = Vec::new();
    while let Some(b) = c.next_batch(log) {
        out.extend(b.send.into_iter().map(|e| e.event.topic.0));
    }
    out
}
