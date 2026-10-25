//! Runs `contracts/bus-v1/streams/handshake.json`: the auth frame, the
//! ack, negotiation and the ack encoder. Go is canonical
//! (`TestStreamConformanceHandshake`). Every failing case names the error
//! it expects (`rejected`, an auth code or a protocol kind), and the
//! runner asserts that exact error.

#![cfg(feature = "bus")]

mod bus_stream_support;

use bus_stream_support::{check, load, must_offset, non_empty, stream_file, VScope};
use hop_top_kit::bus::stream::{
    encode_ack, format_offset, negotiate, parse_ack, parse_auth_frame, Ack, AckError,
    AuthFrameError, FrameFormat, ResumeOutcome, ResumeStatus, StreamCaps, StreamInfo,
};
use serde::Deserialize;
use serde_json::Value;

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct VCaps {
    streams: u64,
    #[serde(default)]
    frames: Vec<String>,
}

fn vcaps_of(c: &Option<StreamCaps>) -> Option<VCaps> {
    c.as_ref().map(|c| VCaps {
        streams: c.streams,
        frames: c.frames.iter().map(|f| f.as_str().to_string()).collect(),
    })
}

fn caps_of(v: &VCaps) -> StreamCaps {
    StreamCaps {
        streams: v.streams,
        frames: v.frames.iter().map(|f| format(f)).collect(),
    }
}

fn format(s: &str) -> FrameFormat {
    FrameFormat::parse(s).unwrap_or_else(|| panic!("vector frame format {s:?}"))
}

#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct VSession {
    subscribe: Option<Vec<String>>,
    scope: Option<VScope>,
    resume: Option<VResume>,
}
#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct VResume {
    stream: String,
    after: String,
}
#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct VStream {
    id: String,
    oldest: String,
    head: String,
    heartbeat_ms: u64,
}
#[derive(Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct VOutcome {
    status: String,
    #[serde(default)]
    reason: String,
    after: String,
    head: String,
}

stream_file!(HandshakeFile {
    auth: Vec<AuthCase>,
    ack: Vec<AckCase>,
    negotiate: Vec<NegotiateCase>,
    ack_encode: Vec<AckEncodeCase>,
});
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AuthCase {
    name: String,
    frame: String,
    ok: bool,
    #[serde(default)]
    token: String,
    #[serde(default)]
    caps: Option<VCaps>,
    #[serde(default)]
    origin: String,
    #[serde(default)]
    session: Option<VSession>,
    #[serde(default)]
    error: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AckCase {
    name: String,
    mine: Option<u64>,
    frame: String,
    ok: bool,
    #[serde(default)]
    kind: String,
    #[serde(default)]
    caps: Option<VCaps>,
    #[serde(default)]
    origin: String,
    #[serde(default)]
    stream: Option<VStream>,
    #[serde(default)]
    resume: Option<VOutcome>,
    #[serde(default)]
    error: Option<Value>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct NegotiateCase {
    name: String,
    mine: VCaps,
    peer: Option<VCaps>,
    handshake: bool,
    configured: String,
    streams: u64,
    format: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AckEncodeCase {
    name: String,
    dialer_caps: bool,
    caps: VCaps,
    origin: String,
    stream: Option<VStream>,
    resume: Option<VOutcome>,
    frame: Value,
}

fn outcome_of(v: &VOutcome) -> ResumeOutcome {
    ResumeOutcome {
        status: ResumeStatus::parse(&v.status).expect("vector status"),
        reason: v.reason.clone(),
        after: must_offset(&v.after),
        head: must_offset(&v.head),
    }
}

fn voutcome_of(o: &ResumeOutcome) -> VOutcome {
    VOutcome {
        status: o.status.as_str().to_string(),
        reason: o.reason.clone(),
        after: format_offset(o.after),
        head: format_offset(o.head),
    }
}

#[test]
fn stream_conformance_handshake() {
    let f: HandshakeFile = load("handshake.json");
    non_empty(&f.auth, "auth");
    non_empty(&f.ack, "ack");
    non_empty(&f.negotiate, "negotiate");
    non_empty(&f.ack_encode, "ack_encode");
    let mut fails = Vec::new();

    for tc in &f.auth {
        let name = format!("auth/{}", tc.name);
        let got = parse_auth_frame(tc.frame.as_bytes());
        if !tc.ok {
            assert!(!tc.error.is_empty(), "{name}: failing case needs a code");
            match &got {
                Err(e) if e.error().code == tc.error => {
                    // invalid_auth carries no frame; a session error does.
                    let has_frame = matches!(e, AuthFrameError::Session { .. });
                    if has_frame == (tc.error == "invalid_auth") {
                        fails.push(format!("{name}: wrong error variant {e:?}"));
                    }
                }
                _ => fails.push(format!("{name}: got {got:?}, want {}", tc.error)),
            }
            continue;
        }
        let a = match got {
            Ok(a) => a,
            Err(e) => {
                fails.push(format!("{name}: {e:?}"));
                continue;
            }
        };
        if a.token != tc.token || a.origin != tc.origin {
            fails.push(format!("{name}: token/origin {:?}/{:?}", a.token, a.origin));
        }
        if vcaps_of(&a.caps) != tc.caps {
            fails.push(format!("{name}: caps {:?}, want {:?}", a.caps, tc.caps));
        }
        let sess = a.session.as_ref().map(|s| VSession {
            subscribe: s.subscribe.clone(),
            scope: s.scope.as_ref().map(|s| VScope {
                workspaces: s.workspaces.clone(),
            }),
            resume: s.resume.as_ref().map(|r| VResume {
                stream: r.stream.clone(),
                after: format_offset(r.after),
            }),
        });
        if sess != tc.session {
            fails.push(format!(
                "{name}: session\n   got  {sess:?}\n   want {:?}",
                tc.session
            ));
        }
    }

    for tc in &f.ack {
        let name = format!("ack/{}", tc.name);
        let mine = tc
            .mine
            .unwrap_or_else(|| panic!("{name}: vector without mine"));
        let got = parse_ack(tc.frame.as_bytes(), mine);
        if !tc.ok {
            let want = tc
                .error
                .as_ref()
                .and_then(Value::as_str)
                .filter(|s| !s.is_empty())
                .unwrap_or_else(|| panic!("{name}: failing case needs an error kind"));
            match (&got, want) {
                (Err(AckError::Rejected), "rejected") => {}
                (Err(AckError::Protocol(p)), w) if p.kind.as_str() == w => {}
                _ => fails.push(format!("{name}: got {got:?}, want {want}")),
            }
            continue;
        }
        let a = match got {
            Ok(a) => a,
            Err(e) => {
                fails.push(format!("{name}: {e:?}"));
                continue;
            }
        };
        match (a, tc.kind.as_str()) {
            (Ack::SessionError(e), "session_error") => {
                let want = tc.error.as_ref().and_then(|v| v["code"].as_str());
                if Some(e.code.as_str()) != want {
                    fails.push(format!("{name}: session error {e:?}"));
                }
            }
            (Ack::AuthOk(a), "auth_ok") => {
                if vcaps_of(&a.caps) != tc.caps {
                    fails.push(format!("{name}: caps {:?}, want {:?}", a.caps, tc.caps));
                }
                if a.origin != tc.origin {
                    fails.push(format!("{name}: origin {:?}", a.origin));
                }
                let st = a.stream.as_ref().map(|s| VStream {
                    id: s.id.clone(),
                    oldest: format_offset(s.oldest),
                    head: format_offset(s.head),
                    heartbeat_ms: s.heartbeat_ms,
                });
                if st != tc.stream {
                    fails.push(format!("{name}: stream {st:?}, want {:?}", tc.stream));
                }
                let ro = a.resume.as_ref().map(voutcome_of);
                if ro != tc.resume {
                    fails.push(format!("{name}: resume {ro:?}, want {:?}", tc.resume));
                }
            }
            (a, want) => fails.push(format!("{name}: got {a:?}, want kind {want}")),
        }
    }

    for tc in &f.negotiate {
        let peer = tc.peer.as_ref().map(caps_of);
        let (v, ff) = negotiate(
            &caps_of(&tc.mine),
            peer.as_ref(),
            tc.handshake,
            format(&tc.configured),
        );
        if v != tc.streams || ff.as_str() != tc.format {
            fails.push(format!(
                "negotiate/{}: {v}/{}, want {}/{}",
                tc.name,
                ff.as_str(),
                tc.streams,
                tc.format
            ));
        }
    }

    for tc in &f.ack_encode {
        let name = format!("ack_encode/{}", tc.name);
        let si = tc.stream.as_ref().map(|s| StreamInfo {
            id: s.id.clone(),
            oldest: must_offset(&s.oldest),
            head: must_offset(&s.head),
            heartbeat_ms: s.heartbeat_ms,
        });
        let ro = tc.resume.as_ref().map(outcome_of);
        let out = encode_ack(
            tc.dialer_caps,
            &caps_of(&tc.caps),
            &tc.origin,
            si.as_ref(),
            ro.as_ref(),
        );
        let got: Value = serde_json::from_slice(&out).expect("ack is JSON");
        if got != tc.frame {
            fails.push(format!("{name}:\n   got  {got}\n   want {}", tc.frame));
        }
        if !tc.dialer_caps && out != br#"{"type":"auth_ok"}"# {
            fails.push(format!(
                "{name}: plain ack bytes {}",
                String::from_utf8_lossy(&out)
            ));
        }
    }
    check(fails);
}
