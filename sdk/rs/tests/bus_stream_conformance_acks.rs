//! Runs `contracts/bus-v1/streams/acks.json`: `caps.acks` and the dialer
//! stream id in the auth frame, acks negotiation, the `puback` frame and
//! the dialer's ack tracking. Go is canonical
//! (`go/runtime/bus/stream_conformance_acks_test.go`).
//!
//! The `acceptor` section pins the acceptor's dedupe memory. This port
//! has the dialer side only, so the section is decoded strictly (its
//! shape must not drift) and skipped; Go runs it.

#![cfg(feature = "bus")]

mod bus_stream_support;

use std::collections::{BTreeMap, BTreeSet};
use std::time::UNIX_EPOCH;

use bus_stream_support::{check, load, must_offset, stream_file};
use hop_top_kit::bus::stream::{
    classify_frame, encode_puback, negotiate_acks, parse_auth_frame, parse_puback, AckResolution,
    AckTracker, FrameKind, LogEntry, Puback, RingLog, SendCursor, StreamCaps, StreamLog,
    WRITER_BATCH,
};
use hop_top_kit::bus::Event;
use serde::Deserialize;
use serde_json::Value;

stream_file!(AcksFile {
    auth: Vec<AuthCase>,
    negotiate: Vec<NegotiateCase>,
    puback: Vec<PubackCase>,
    puback_encode: Vec<EncodeCase>,
    dialer: Vec<DialerCase>,
    acceptor: Vec<AcceptorCase>,
});

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AuthCase {
    name: String,
    frame: String,
    ok: bool,
    #[serde(default)]
    acks: u64,
    #[serde(default)]
    stream: String,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct NegotiateCase {
    name: String,
    mine: u64,
    peer: Option<PeerCaps>,
    stream_link: bool,
    acks: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PeerCaps {
    acks: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PubackCase {
    name: String,
    frame: String,
    ok: bool,
    #[serde(default)]
    offset: String,
    #[serde(default)]
    id: String,
    #[serde(default)]
    status: String,
    #[serde(default)]
    code: String,
    #[serde(default)]
    message: String,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct EncodeCase {
    name: String,
    offset: String,
    id: String,
    status: String,
    code: String,
    message: String,
    frame: Value,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DialerCase {
    name: String,
    capacity: usize,
    ops: Vec<DialerOp>,
    sent: Vec<String>,
    resolved: Vec<Resolved>,
    #[serde(default)]
    error: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DialerOp {
    #[serde(default)]
    publish: Option<String>,
    #[serde(default)]
    acked: bool,
    #[serde(default)]
    connect: Option<Connect>,
    #[serde(default)]
    disconnect: bool,
    #[serde(default)]
    puback: Option<String>,
    #[serde(default)]
    fail: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Connect {
    acks: bool,
}

#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct Resolved {
    event: String,
    code: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct AcceptorCase {
    name: String,
    limits: Limits,
    steps: Vec<AcceptorStep>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct Limits {
    streams: u64,
    streams_per_peer: u64,
    errors: u64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct AcceptorStep {
    #[serde(default)]
    recv: Option<SeenRef>,
    #[serde(default)]
    result: Option<String>,
    #[serde(default)]
    settle: Option<SeenRef>,
    #[serde(default)]
    code: Option<String>,
    #[serde(default)]
    expect: Option<Expect>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct SeenRef {
    peer: String,
    stream: String,
    offset: String,
    #[serde(default)]
    id: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
#[allow(dead_code)]
struct Expect {
    publish: bool,
    status: String,
    code: String,
}

fn members(frame: &str) -> serde_json::Map<String, Value> {
    let (kind, m) = classify_frame(frame.as_bytes(), true);
    assert_eq!(kind, FrameKind::Control, "{frame} is not a control frame");
    m.expect("members")
}

#[test]
fn stream_conformance_acks() {
    let f: AcksFile = load("acks.json");
    for (what, n) in [
        ("auth", f.auth.len()),
        ("negotiate", f.negotiate.len()),
        ("puback", f.puback.len()),
        ("puback_encode", f.puback_encode.len()),
        ("dialer", f.dialer.len()),
        ("acceptor", f.acceptor.len()),
    ] {
        assert!(n > 0, "acks.json: {what} needs cases");
    }
    let mut fails = Vec::new();

    for tc in &f.auth {
        let got = parse_auth_frame(tc.frame.as_bytes());
        match (tc.ok, got) {
            (true, Ok(a)) => {
                let acks = a.caps.as_ref().map_or(0, |c| c.acks);
                if acks != tc.acks || a.stream != tc.stream {
                    fails.push(format!(
                        "auth/{}: acks {acks} stream {:?}, want {} {:?}",
                        tc.name, a.stream, tc.acks, tc.stream
                    ));
                }
            }
            (false, Err(e)) if e.error().code == tc.error && !tc.error.is_empty() => {}
            (_, got) => fails.push(format!("auth/{}: got {got:?}", tc.name)),
        }
    }

    for tc in &f.negotiate {
        let peer = tc.peer.as_ref().map(|p| StreamCaps {
            streams: 1,
            frames: Vec::new(),
            acks: p.acks,
        });
        if negotiate_acks(tc.mine, peer.as_ref(), tc.stream_link) != tc.acks {
            fails.push(format!("negotiate/{}: want {}", tc.name, tc.acks));
        }
    }

    for tc in &f.puback {
        let got = parse_puback(&members(&tc.frame));
        match (tc.ok, got) {
            (true, Ok(p)) => {
                let want = Puback {
                    offset: must_offset(&tc.offset),
                    id: tc.id.clone(),
                    status: tc.status.clone(),
                    code: tc.code.clone(),
                    message: tc.message.clone(),
                };
                if p != want {
                    fails.push(format!("puback/{}: {p:?}, want {want:?}", tc.name));
                }
            }
            (false, Err(e)) if e.kind.as_str() == tc.error && !tc.error.is_empty() => {}
            (_, got) => fails.push(format!("puback/{}: got {got:?}", tc.name)),
        }
    }

    for tc in &f.puback_encode {
        let got: Value = serde_json::from_slice(&encode_puback(&Puback {
            offset: must_offset(&tc.offset),
            id: tc.id.clone(),
            status: tc.status.clone(),
            code: tc.code.clone(),
            message: tc.message.clone(),
        }))
        .unwrap();
        if got != tc.frame {
            fails.push(format!("puback_encode/{}: {got}", tc.name));
        }
    }

    for tc in &f.dialer {
        if let Err(e) = run_dialer(tc) {
            fails.push(format!("dialer/{}: {e}", tc.name));
        }
    }

    check(fails);
}

fn run_dialer(tc: &DialerCase) -> Result<(), String> {
    let mut log = RingLog::new("stm_dialer", tc.capacity);
    let mut cur = SendCursor::new(&log, true);
    let mut tr = AckTracker::new(cur.at);
    let mut names: BTreeMap<u64, String> = BTreeMap::new();
    let mut settled: BTreeSet<u64> = BTreeSet::new();
    let (mut connected, mut acks) = (false, false);
    let mut sent = Vec::new();
    let mut resolved = Vec::new();
    let mut error = String::new();

    let mut record = |rs: Vec<AckResolution>, names: &BTreeMap<u64, String>| {
        for r in rs {
            if settled.insert(r.offset) {
                resolved.push(Resolved {
                    event: names[&r.offset].clone(),
                    code: r.code,
                });
            }
        }
    };
    let send = |cur: &mut SendCursor,
                tr: &mut AckTracker,
                log: &RingLog,
                acks: bool,
                sent: &mut Vec<String>|
     -> Vec<AckResolution> {
        let mut out = Vec::new();
        loop {
            let before = cur.at;
            let (entries, gap) = cur.next(log, WRITER_BATCH);
            if entries.is_empty() {
                return out;
            }
            if gap > 0 {
                out.extend(tr.on_gap(before, entries[0].offset));
            }
            for e in entries {
                out.extend(tr.on_send(e.offset, acks));
                sent.push(format!("{}@{}", e.event.topic.0, e.offset));
            }
        }
    };

    for (i, op) in tc.ops.iter().enumerate() {
        if let Some(topic) = &op.publish {
            let off = log.append(LogEntry {
                offset: 0,
                event: Event::new_at(topic.as_str(), "", Value::Null, UNIX_EPOCH),
                origin: String::new(),
                marker: false,
            });
            names.insert(off, topic.clone());
            if op.acked {
                tr.register(off, topic);
            }
            if connected {
                let rs = send(&mut cur, &mut tr, &log, acks, &mut sent);
                record(rs, &names);
            }
        } else if let Some(c) = &op.connect {
            connected = true;
            acks = c.acks;
            let (at, rs) = tr.link_up(acks, cur.at);
            cur.at = at;
            record(rs, &names);
            let rs = send(&mut cur, &mut tr, &log, acks, &mut sent);
            record(rs, &names);
        } else if op.disconnect {
            connected = false;
        } else if let Some(frame) = &op.puback {
            match parse_puback(&members(frame)).and_then(|p| tr.on_puback(&p)) {
                Ok(rs) => record(rs, &names),
                Err(e) => {
                    error = e.kind.as_str().to_string();
                    break;
                }
            }
        } else if let Some(code) = &op.fail {
            record(tr.fail_all(code), &names);
        } else {
            return Err(format!("op {i} is empty"));
        }
    }
    if sent != tc.sent || resolved != tc.resolved || error != tc.error {
        return Err(format!(
            "sent {sent:?} resolved {resolved:?} error {error:?}, want {:?} {:?} {:?}",
            tc.sent, tc.resolved, tc.error
        ));
    }
    Ok(())
}
