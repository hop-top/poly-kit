//! The `audit` mount hook: one record per refusal the surface decides
//! before adopter code runs, on both eras and over stdio, and never a
//! change to the wire response.
//!
//! Every case mounts the same bridge twice, once without the hook and
//! once with it, and sends the same request to both: the audited
//! response must be byte-identical to the plain one.

#![cfg(feature = "mcp")]

use std::collections::BTreeMap;
use std::io::{self, Cursor};
use std::sync::{Arc, Mutex};
use std::time::SystemTime;

use hop_top_kit::mcp::safety::SafetyClass;
use hop_top_kit::mcp::{
    AuditRecord, Bridge, CallResult, HttpRequest, Leaf, MountOptions, Response, Surface,
};
use serde_json::Value;

const MODERN_META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;

/// `_meta` declaring form-mode elicitation support.
const ELICIT_META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{"elicitation":{"form":{}}},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;

/// Go's sentinel messages, verbatim.
const ERR_AUTH_REFUSED: &str = "cmdsurface: authentication refused";
const ERR_CONFIRMATION_REQUIRED: &str = "mcpsdk: confirmation required";
const ERR_CONFIRMATION_DECLINED: &str = "mcpsdk: confirmation declined";
const ERR_CONFIRMATION_STATE_REJECTED: &str =
    "mcpsdk: confirmation requestState failed verification";
const ERR_DESTRUCTIVE_BLOCKED: &str = "cmdsurface: destructive command blocked on this surface";

type Captured = Arc<Mutex<Vec<AuditRecord>>>;

/// An auth-required `secret`, a confirmation-gated `deploy`, a
/// destructive `widget delete` and a plain `ping`.
fn bridge() -> Bridge {
    Bridge::new()
        .leaf(
            Leaf::new(&["secret"], "Locked", |_| Ok(CallResult::ok("opened\n"))).with_class(
                SafetyClass {
                    auth_required: true,
                    ..SafetyClass::default()
                },
            ),
        )
        .leaf(
            Leaf::new(&["deploy"], "Deploy", |_| Ok(CallResult::ok("deployed\n"))).with_class(
                SafetyClass {
                    requires_confirmation: true,
                    ..SafetyClass::default()
                },
            ),
        )
        .leaf(
            Leaf::new(&["widget", "delete"], "Delete", |_| {
                Ok(CallResult::ok("deleted\n"))
            })
            .with_class(SafetyClass {
                destructive: true,
                ..SafetyClass::default()
            }),
        )
        .leaf(Leaf::new(&["ping"], "Ping", |_| {
            Ok(CallResult::ok("pong\n"))
        }))
}

fn keyed() -> MountOptions {
    MountOptions {
        confirmation_key: Some(b"shared-key".to_vec()),
        ..MountOptions::default()
    }
}

fn capturing(options: MountOptions) -> (MountOptions, Captured) {
    let records: Captured = Arc::default();
    let sink = Arc::clone(&records);
    let options = MountOptions {
        audit: Some(Arc::new(move |record: &AuditRecord| {
            sink.lock().unwrap().push(record.clone());
        })),
        ..options
    };
    (options, records)
}

/// Sends `req` to a plain mount and to an audited one; returns both
/// responses and the records the hook captured.
fn observe(options: MountOptions, req: &HttpRequest) -> (Response, Response, Vec<AuditRecord>) {
    let plain = Surface::mount(bridge(), options.clone()).unwrap().call(req);
    let (audited_options, records) = capturing(options);
    let audited = Surface::mount(bridge(), audited_options).unwrap().call(req);
    let records = records.lock().unwrap().clone();
    (plain, audited, records)
}

fn legacy_call(name: &str) -> HttpRequest {
    HttpRequest::post(
        "/mcp",
        format!(r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"{name}"}}}}"#),
    )
}

fn modern_call(name: &str, params: &str) -> HttpRequest {
    HttpRequest::post(
        "/mcp",
        format!(
            r#"{{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{{"name":"{name}",{params}}}}}"#
        ),
    )
    .header("MCP-Protocol-Version", "2026-07-28")
    .header("Mcp-Method", "tools/call")
    .header("Mcp-Name", name)
}

fn body(resp: &Response) -> Value {
    serde_json::from_str(resp.body_str()).expect("response is JSON")
}

fn extra(pairs: &[(&str, &str)]) -> BTreeMap<String, String> {
    pairs
        .iter()
        .map(|(k, v)| ((*k).to_owned(), (*v).to_owned()))
        .collect()
}

/// Asserts `records` holds exactly one record, with every field as
/// expected and a timestamp taken while the call ran.
fn assert_one(
    records: &[AuditRecord],
    before: SystemTime,
    path: &[&str],
    spec_version: &str,
    error: &str,
    want_extra: &[(&str, &str)],
) {
    assert_eq!(records.len(), 1, "{records:?}");
    let record = &records[0];
    assert_eq!(record.path, path);
    assert_eq!(record.surface, "mcp");
    assert_eq!(record.spec_version, spec_version);
    assert_eq!(record.error, error);
    assert_eq!(record.extra, extra(want_extra));
    assert!(
        record.requested_at >= before && record.requested_at <= SystemTime::now(),
        "requested_at is stamped when the refusal is decided"
    );
}

// --- 2024-11-05 --------------------------------------------------------

#[test]
fn legacy_auth_refused_is_audited() {
    let before = SystemTime::now();
    let (plain, audited, records) = observe(MountOptions::default(), &legacy_call("secret"));
    assert_eq!(plain.status, 401);
    assert_eq!(audited, plain, "the hook never touches the response");
    assert_one(
        &records,
        before,
        &["secret"],
        "2024-11-05",
        ERR_AUTH_REFUSED,
        &[("mcp_spec_version", "2024-11-05")],
    );
}

#[test]
fn legacy_confirmation_required_is_audited() {
    let before = SystemTime::now();
    let (plain, audited, records) = observe(MountOptions::default(), &legacy_call("deploy"));
    assert_eq!(plain.status, 428);
    assert_eq!(audited, plain);
    assert_one(
        &records,
        before,
        &["deploy"],
        "2024-11-05",
        ERR_CONFIRMATION_REQUIRED,
        &[("mcp_spec_version", "2024-11-05")],
    );

    // The header satisfies the gate: nothing to audit.
    let (_, audited, records) = observe(
        MountOptions::default(),
        &legacy_call("deploy").header("X-Confirm-Token", "t"),
    );
    assert!(audited.body_str().contains("deployed"));
    assert!(records.is_empty(), "{records:?}");
}

#[test]
fn legacy_destructive_ceiling_is_audited() {
    let before = SystemTime::now();
    let (plain, audited, records) = observe(MountOptions::default(), &legacy_call("widget.delete"));
    assert_eq!(plain.status, 200);
    assert!(plain.body_str().contains("destructive command blocked"));
    assert_eq!(audited, plain);
    assert_one(
        &records,
        before,
        &["widget", "delete"],
        "2024-11-05",
        ERR_DESTRUCTIVE_BLOCKED,
        &[("mcp_spec_version", "2024-11-05")],
    );
}

// --- 2026-07-28 --------------------------------------------------------

#[test]
fn modern_auth_refused_is_audited() {
    let before = SystemTime::now();
    let (plain, audited, records) =
        observe(MountOptions::default(), &modern_call("secret", MODERN_META));
    assert_eq!(plain.status, 401);
    assert_eq!(audited, plain);
    assert_one(
        &records,
        before,
        &["secret"],
        "2026-07-28",
        ERR_AUTH_REFUSED,
        &[("mcp_spec_version", "2026-07-28")],
    );
}

#[test]
fn modern_header_confirmation_required_is_audited() {
    let before = SystemTime::now();
    let (plain, audited, records) =
        observe(MountOptions::default(), &modern_call("deploy", MODERN_META));
    assert_eq!(plain.status, 428);
    assert_eq!(audited, plain);
    assert_one(
        &records,
        before,
        &["deploy"],
        "2026-07-28",
        ERR_CONFIRMATION_REQUIRED,
        &[("mcp_spec_version", "2026-07-28")],
    );
}

#[test]
fn modern_no_elicitation_fallback_is_audited() {
    // A keyed mount, a client that declared no elicitation: the header
    // gate answers, and the refusal is the same event.
    let before = SystemTime::now();
    let (plain, audited, records) = observe(keyed(), &modern_call("deploy", MODERN_META));
    assert_eq!(plain.status, 428);
    assert!(plain.body_str().contains("confirmation required"));
    assert_eq!(audited, plain);
    assert_one(
        &records,
        before,
        &["deploy"],
        "2026-07-28",
        ERR_CONFIRMATION_REQUIRED,
        &[("mcp_spec_version", "2026-07-28")],
    );
}

/// Runs the first MRTR call on `surface` and returns its `requestState`.
fn prompt(surface: &Surface) -> String {
    let first = surface.call(&modern_call("deploy", ELICIT_META));
    body(&first)["result"]["requestState"]
        .as_str()
        .expect("requestState")
        .to_owned()
}

#[test]
fn modern_confirmation_declined_is_audited() {
    for action in ["decline", "cancel"] {
        let (audited_options, records) = capturing(keyed());
        let plain = Surface::mount(bridge(), keyed()).unwrap();
        let audited = Surface::mount(bridge(), audited_options).unwrap();
        // One state serves both mounts: they share the key and the
        // binding, and the prompt itself is not a refusal.
        let state = prompt(&audited);
        assert!(
            records.lock().unwrap().is_empty(),
            "a prompt is not audited"
        );

        let before = SystemTime::now();
        let retry = modern_call(
            "deploy",
            &format!(
                r#""requestState":"{state}","inputResponses":{{"confirm":{{"action":"{action}"}}}},{ELICIT_META}"#
            ),
        );
        let plain_resp = plain.call(&retry);
        let audited_resp = audited.call(&retry);
        assert_eq!(plain_resp.status, 200);
        assert!(plain_resp.body_str().contains("confirmation declined"));
        assert_eq!(audited_resp, plain_resp, "{action}");
        assert_one(
            &records.lock().unwrap(),
            before,
            &["deploy"],
            "2026-07-28",
            ERR_CONFIRMATION_DECLINED,
            &[("mcp_spec_version", "2026-07-28")],
        );
    }
}

#[test]
fn modern_state_rejected_is_audited() {
    let before = SystemTime::now();
    let tampered = modern_call(
        "deploy",
        &format!(
            r#""requestState":"v1.9999999999.AAAA","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}"#
        ),
    );
    let (plain, audited, records) = observe(keyed(), &tampered);
    assert_eq!(plain.status, 200);
    assert_eq!(audited.status, 200);
    // The re-prompt carries freshly minted state, so compare the shape
    // rather than the bytes.
    for resp in [&plain, &audited] {
        let result = body(resp)["result"].clone();
        assert_eq!(result["resultType"], "input_required");
        assert!(result["requestState"].is_string());
        assert!(!resp.body_str().contains("deployed"));
    }
    assert_one(
        &records,
        before,
        &["deploy"],
        "2026-07-28",
        ERR_CONFIRMATION_STATE_REJECTED,
        &[
            ("mcp_confirm_rejection", "request_state_verification_failed"),
            ("mcp_spec_version", "2026-07-28"),
        ],
    );
}

#[test]
fn state_bound_to_other_arguments_is_rejected_and_audited() {
    // Authentic state, presented for a different binding: fails the MAC,
    // so it is a rejection, not an expiry.
    let (audited_options, records) = capturing(keyed());
    let surface = Surface::mount(bridge(), audited_options).unwrap();
    let state = prompt(&surface);
    let resp = surface.call(&modern_call(
        "deploy",
        &format!(
            r#""arguments":{{"env":"production"}},"requestState":"{state}","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}"#
        ),
    ));
    assert_eq!(body(&resp)["result"]["resultType"], "input_required");
    let records = records.lock().unwrap();
    assert_eq!(records.len(), 1, "{records:?}");
    assert_eq!(records[0].error, ERR_CONFIRMATION_STATE_REJECTED);
    assert_eq!(
        records[0]
            .extra
            .get("mcp_confirm_rejection")
            .map(String::as_str),
        Some("request_state_verification_failed")
    );
}

#[test]
fn modern_destructive_ceiling_is_audited() {
    let before = SystemTime::now();
    let (plain, audited, records) = observe(
        MountOptions::default(),
        &modern_call("widget.delete", MODERN_META),
    );
    assert_eq!(plain.status, 200);
    assert!(plain.body_str().contains("destructive command blocked"));
    assert_eq!(audited, plain);
    assert_one(
        &records,
        before,
        &["widget", "delete"],
        "2026-07-28",
        ERR_DESTRUCTIVE_BLOCKED,
        &[("mcp_spec_version", "2026-07-28")],
    );
}

// --- best effort -------------------------------------------------------

#[test]
fn a_panicking_hook_leaves_the_response_unchanged() {
    let panicking = |options: MountOptions| MountOptions {
        audit: Some(Arc::new(|_: &AuditRecord| panic!("sink down"))),
        ..options
    };
    for (options, req) in [
        (MountOptions::default(), legacy_call("secret")),
        (MountOptions::default(), legacy_call("widget.delete")),
        (keyed(), modern_call("deploy", MODERN_META)),
        (MountOptions::default(), modern_call("secret", MODERN_META)),
    ] {
        let plain = Surface::mount(bridge(), options.clone())
            .unwrap()
            .call(&req);
        let audited = Surface::mount(bridge(), panicking(options))
            .unwrap()
            .call(&req);
        assert_eq!(audited, plain, "{}", req.body.escape_ascii());
    }

    // The re-prompt after a rejected state mints fresh state: compare
    // the shape.
    let tampered = modern_call(
        "deploy",
        &format!(
            r#""requestState":"v1.9999999999.AAAA","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}"#
        ),
    );
    let audited = Surface::mount(bridge(), panicking(keyed()))
        .unwrap()
        .call(&tampered);
    assert_eq!(audited.status, 200);
    assert_eq!(body(&audited)["result"]["resultType"], "input_required");
}

#[test]
fn nothing_is_audited_for_calls_that_are_not_refused() {
    let (audited_options, records) = capturing(keyed());
    let surface = Surface::mount(bridge(), audited_options).unwrap();

    // A successful call on each era.
    assert!(surface
        .call(&legacy_call("ping"))
        .body_str()
        .contains("pong"));
    assert!(surface
        .call(&modern_call("ping", MODERN_META))
        .body_str()
        .contains("pong"));
    // The whole MRTR round trip: prompt, then accept.
    let state = prompt(&surface);
    let accepted = surface.call(&modern_call(
        "deploy",
        &format!(
            r#""requestState":"{state}","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}"#
        ),
    ));
    assert!(accepted.body_str().contains("deployed"));
    // Refusals outside this pass: an unknown tool, a missing name.
    assert!(surface
        .call(&legacy_call("nope"))
        .body_str()
        .contains("unknown tool"));
    assert!(surface
        .call(&HttpRequest::post(
            "/mcp",
            r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}"#
        ))
        .body_str()
        .contains("missing tool name"));

    assert!(records.lock().unwrap().is_empty(), "{records:?}");
}

// --- stdio -------------------------------------------------------------

#[test]
fn stdio_state_rejected_is_audited() {
    let (audited_options, records) = capturing(keyed());
    let surface = Surface::mount(bridge(), audited_options).unwrap();

    let before = SystemTime::now();
    let input = format!(
        r#"{{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{{"name":"deploy","requestState":"v1.9999999999.AAAA","inputResponses":{{"confirm":{{"action":"accept"}}}},{ELICIT_META}}}}}"#
    ) + "\n";
    let mut out = Vec::new();
    surface
        .serve_stdio(Cursor::new(input.into_bytes()), &mut out, io::sink())
        .expect("session");

    let reply: Value = serde_json::from_slice(&out).expect("one JSON reply");
    assert_eq!(reply["id"], 7);
    assert_eq!(reply["result"]["resultType"], "input_required");
    assert!(!String::from_utf8_lossy(&out).contains("deployed"));
    assert_one(
        &records.lock().unwrap(),
        before,
        &["deploy"],
        "2026-07-28",
        ERR_CONFIRMATION_STATE_REJECTED,
        &[
            ("mcp_confirm_rejection", "request_state_verification_failed"),
            ("mcp_spec_version", "2026-07-28"),
        ],
    );
}

#[test]
fn stdio_header_gates_are_audited_on_both_eras() {
    // No header layer: the X-Confirm-Token gate cannot be satisfied and
    // no verifier admits anyone, so each era refuses, and each refusal
    // is audited with its own spec version.
    let (audited_options, records) = capturing(MountOptions::default());
    let surface = Surface::mount(bridge(), audited_options).unwrap();
    let input = format!(
        "{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{{\"name\":\"deploy\"}}}}\n\
         {{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{{\"name\":\"secret\",{MODERN_META}}}}}\n"
    );
    surface
        .serve_stdio(Cursor::new(input.into_bytes()), io::sink(), io::sink())
        .expect("session");

    let mut records = records.lock().unwrap().clone();
    records.sort_by(|a, b| a.spec_version.cmp(&b.spec_version));
    assert_eq!(records.len(), 2, "{records:?}");
    assert_eq!(records[0].path, ["deploy"]);
    assert_eq!(records[0].spec_version, "2024-11-05");
    assert_eq!(records[0].error, ERR_CONFIRMATION_REQUIRED);
    assert_eq!(records[1].path, ["secret"]);
    assert_eq!(records[1].spec_version, "2026-07-28");
    assert_eq!(records[1].error, ERR_AUTH_REFUSED);
}
