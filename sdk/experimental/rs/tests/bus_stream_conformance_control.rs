//! Runs `contracts/bus-v1/streams/control.json`: frame classification,
//! acceptor and dialer handling of every control type, error codes, and
//! applying subscribe and unsubscribe. Go is canonical
//! (`TestStreamConformanceControl`). Every dialer `protocol_error` asserts
//! its kind, and every acceptor reply its code.

#![cfg(feature = "bus")]

mod bus_stream_support;

use bus_stream_support::{
    check, load, non_empty, scope_of, stream_file, vscope_of, ConfSub, VScope,
};
use hop_top_kit::bus::stream::{
    acceptor_control, classify_frame, dialer_control, format_offset, AcceptorControl, CtlRequest,
    DialerCtl, RequestType,
};
use serde::Deserialize;

#[derive(Clone, Debug, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
struct VReq {
    #[serde(rename = "type")]
    typ: String,
    #[serde(default)]
    id: String,
    #[serde(default)]
    patterns: Option<Vec<String>>,
    #[serde(default)]
    scope: Option<VScope>,
}

fn req_type(s: &str) -> RequestType {
    match s {
        "subscribe" => RequestType::Subscribe,
        "unsubscribe" => RequestType::Unsubscribe,
        other => panic!("vector request type {other:?}"),
    }
}

fn vreq_of(r: &CtlRequest) -> VReq {
    VReq {
        typ: r.typ.as_str().to_string(),
        id: r.id.clone(),
        patterns: r.patterns.clone(),
        scope: vscope_of(&r.scope),
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct IdCode {
    #[serde(default)]
    id: String,
    code: String,
}

stream_file!(ControlFile {
    classify: Vec<ClassifyCase>,
    acceptor: Vec<AcceptorCase>,
    dialer: Vec<DialerCase>,
    apply: Vec<ApplyCase>,
});
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ClassifyCase {
    name: String,
    frame: String,
    stream_link: bool,
    kind: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct AcceptorCase {
    name: String,
    frame: String,
    outcome: String,
    #[serde(default)]
    request: Option<VReq>,
    #[serde(default)]
    reply: Option<IdCode>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DialerCase {
    name: String,
    frame: String,
    outcome: String,
    #[serde(default)]
    kind: Option<String>,
    #[serde(default)]
    ack: Option<VAck>,
    #[serde(default)]
    error: Option<IdCode>,
    #[serde(default)]
    reset: Option<VReset>,
    #[serde(default)]
    position: Option<VPosition>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct VAck {
    id: String,
    at: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct VReset {
    stream: String,
    reason: String,
    after: String,
    head: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct VPosition {
    stream: String,
    offset: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ApplyCase {
    name: String,
    state: ConfSub,
    request: VReq,
    max: usize,
    ok: bool,
    #[serde(default)]
    code: Option<String>,
    #[serde(default)]
    after: Option<ConfSub>,
    #[serde(default)]
    effective: Option<Effective>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Effective {
    patterns: Vec<String>,
    #[serde(default)]
    scope: Option<VScope>,
}

#[test]
fn stream_conformance_control() {
    let f: ControlFile = load("control.json");
    non_empty(&f.classify, "classify");
    non_empty(&f.acceptor, "acceptor");
    non_empty(&f.dialer, "dialer");
    non_empty(&f.apply, "apply");
    let mut fails = Vec::new();

    for tc in &f.classify {
        let (kind, _) = classify_frame(tc.frame.as_bytes(), tc.stream_link);
        if kind.as_str() != tc.kind {
            fails.push(format!(
                "classify/{}: kind {kind:?}, want {}",
                tc.name, tc.kind
            ));
        }
    }

    for tc in &f.acceptor {
        let name = format!("acceptor/{}", tc.name);
        let (kind, members) = classify_frame(tc.frame.as_bytes(), true);
        let Some(members) = members.filter(|_| kind.as_str() == "control") else {
            fails.push(format!(
                "{name}: vector frame is not a control frame ({kind:?})"
            ));
            continue;
        };
        match (acceptor_control(&members), tc.outcome.as_str()) {
            (AcceptorControl::Request(r), "request") => {
                let want = tc.request.as_ref().expect("vector: request outcome");
                if &vreq_of(&r) != want {
                    fails.push(format!("{name}:\n   got  {r:?}\n   want {want:?}"));
                }
            }
            (AcceptorControl::Reply(e), "reply") => {
                let want = tc.reply.as_ref().expect("vector: reply outcome");
                if e.id != want.id || e.error.code != want.code {
                    fails.push(format!(
                        "{name}: reply {}/{}, want {}/{}",
                        e.id, e.error.code, want.id, want.code
                    ));
                }
            }
            (AcceptorControl::Ignore, "ignore") => {}
            (got, want) => fails.push(format!("{name}: got {got:?}, want {want}")),
        }
    }

    for tc in &f.dialer {
        let name = format!("dialer/{}", tc.name);
        let (kind, members) = classify_frame(tc.frame.as_bytes(), true);
        let Some(members) = members.filter(|_| kind.as_str() == "control") else {
            fails.push(format!(
                "{name}: vector frame is not a control frame ({kind:?})"
            ));
            continue;
        };
        let got = dialer_control(&members);
        if tc.outcome == "protocol_error" {
            let want = tc
                .kind
                .as_deref()
                .expect("vector: protocol_error needs a kind");
            match &got {
                Err(e) if e.kind.as_str() == want => {}
                _ => fails.push(format!("{name}: got {got:?}, want protocol error {want}")),
            }
            continue;
        }
        let ok = match (got, tc.outcome.as_str()) {
            (Ok(DialerCtl::Ack { id, at }), "ack") => {
                let w = tc.ack.as_ref().expect("vector: ack");
                id == w.id && format_offset(at) == w.at
            }
            (Ok(DialerCtl::Error { id, code, .. }), "error") => {
                let w = tc.error.as_ref().expect("vector: error");
                id == w.id && code == w.code
            }
            (Ok(DialerCtl::Reset(r)), "reset") => {
                let w = tc.reset.as_ref().expect("vector: reset");
                r.stream == w.stream
                    && r.reason == w.reason
                    && format_offset(r.after) == w.after
                    && format_offset(r.head) == w.head
            }
            (Ok(DialerCtl::Position(p)), "position") => {
                let w = tc.position.as_ref().expect("vector: position");
                p.stream == w.stream && format_offset(p.offset) == w.offset
            }
            (Ok(DialerCtl::Ignore), "ignore") => true,
            (got, want) => {
                fails.push(format!("{name}: got {got:?}, want {want}"));
                continue;
            }
        };
        if !ok {
            fails.push(format!("{name}: fields differ from the vector"));
        }
    }

    for tc in &f.apply {
        let name = format!("apply/{}", tc.name);
        let r = CtlRequest {
            typ: req_type(&tc.request.typ),
            id: tc.request.id.clone(),
            patterns: tc.request.patterns.clone(),
            scope: scope_of(&tc.request.scope),
        };
        let got = tc.state.state().apply(&r, tc.max);
        if !tc.ok {
            let want = tc
                .code
                .as_deref()
                .expect("vector: failing apply needs a code");
            match got {
                Err(e) if e.code == want => {}
                other => fails.push(format!("{name}: got {other:?}, want code {want}")),
            }
            continue;
        }
        let got = match got {
            Ok(s) => s,
            Err(e) => {
                fails.push(format!("{name}: {e}"));
                continue;
            }
        };
        let want = tc.after.as_ref().expect("vector: ok apply needs after");
        if &ConfSub::of(&got) != want {
            fails.push(format!(
                "{name}: state\n   got  {:?}\n   want {want:?}",
                ConfSub::of(&got)
            ));
        }
        let eff = got.effective();
        let w = tc
            .effective
            .as_ref()
            .expect("vector: ok apply needs effective");
        if eff.patterns != w.patterns || vscope_of(&eff.scope) != w.scope {
            fails.push(format!("{name}: effective {eff:?}"));
        }
    }
    check(fails);
}
