//! Cross-language MCP wire conformance.
//!
//! Executes `sdk/tests/cross-lang/fixtures/mcp-wire.json` — the same
//! artifact the Go suite is gated by — against this port's surface.
//! Each case posts `request`'s bytes verbatim with `headers` applied and
//! asserts the response is **byte-identical** to `response` and the
//! status equals `status`.
//!
//! Byte-identical means exactly that: no JSON decode/re-encode before
//! comparing. Go emits objects with lexicographically sorted keys and a
//! trailing newline, and this crate's `preserve_order` serde_json would
//! otherwise emit insertion order, so the comparison is what proves the
//! explicit sort in `mcp::wire` is correct.
//!
//! The fixtures are the parity contract. Where they and the ADRs
//! disagree, the fixtures win.

#![cfg(feature = "mcp")]

use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;

use hop_top_kit::mcp::safety::{SafetyClass, Surface as SurfaceKind};
use hop_top_kit::mcp::{Bridge, CallResult, FlagSchema, HttpRequest, Leaf, MountOptions, Surface};
use serde_json::Value;

/// Locates the shared fixture file from the crate root.
fn fixture_path() -> PathBuf {
    // sdk/experimental/rs -> repo root
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../..")
        .join("sdk/tests/cross-lang/fixtures/mcp-wire.json")
}

/// One fixture case.
struct Case {
    name: String,
    era: String,
    headers: Vec<(String, String)>,
    request: String,
    status: u16,
    response: String,
}

/// One ordered sequence: steps replayed against a single long-lived
/// mount, in order.
struct Sequence {
    name: String,
    steps: Vec<Case>,
}

fn load_doc() -> Value {
    let raw = std::fs::read_to_string(fixture_path()).expect("read mcp-wire.json");
    serde_json::from_str(&raw).expect("parse mcp-wire.json")
}

/// Decodes one case/step object; both share the same shape.
fn parse_case(c: &Value) -> Case {
    Case {
        name: c["name"].as_str().unwrap().to_owned(),
        era: c["era"].as_str().unwrap_or("legacy").to_owned(),
        headers: c["headers"]
            .as_object()
            .map(|h| {
                h.iter()
                    .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_owned()))
                    .collect()
            })
            .unwrap_or_default(),
        request: c["request"].as_str().unwrap().to_owned(),
        status: c["status"].as_u64().unwrap() as u16,
        response: c["response"].as_str().unwrap().to_owned(),
    }
}

fn load_sequences() -> Vec<Sequence> {
    load_doc()["sequences"]
        .as_array()
        .map(|seqs| {
            seqs.iter()
                .map(|s| Sequence {
                    name: s["name"].as_str().unwrap().to_owned(),
                    steps: s["steps"]
                        .as_array()
                        .unwrap()
                        .iter()
                        .map(parse_case)
                        .collect(),
                })
                .collect()
        })
        .unwrap_or_default()
}

fn load_cases() -> Vec<Case> {
    let raw = std::fs::read_to_string(fixture_path()).expect("read mcp-wire.json");
    let doc: Value = serde_json::from_str(&raw).expect("parse mcp-wire.json");
    doc["cases"]
        .as_array()
        .expect("cases array")
        .iter()
        .map(|c| Case {
            name: c["name"].as_str().unwrap().to_owned(),
            era: c["era"].as_str().unwrap().to_owned(),
            headers: c["headers"]
                .as_object()
                .map(|h| {
                    h.iter()
                        .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_owned()))
                        .collect()
                })
                .unwrap_or_default(),
            request: c["request"].as_str().unwrap().to_owned(),
            status: c["status"].as_u64().unwrap() as u16,
            response: c["response"].as_str().unwrap().to_owned(),
        })
        .collect()
}

/// A leaf carrying only the MCP surface plus the local ones, matching
/// the Go bridge's default enablement.
fn leaf(path: &[&str], short: &str, stdout: &'static str) -> Leaf {
    Leaf::new(path, short, move |_| Ok(CallResult::ok(stdout)))
}

/// Enablement matching Go's `DefaultPolicy` defaults.
fn default_surfaces() -> BTreeSet<SurfaceKind> {
    [SurfaceKind::Cli, SurfaceKind::Lib, SurfaceKind::Mcp]
        .into_iter()
        .collect()
}

/// The legacy fixtures' command tree (Go's `legacyLockTree`).
///
/// `widget.add` carries the full visible flag set. Go's tree also
/// registers a hidden and a deprecated flag on it, both of which its
/// schema builder filters out; they are simply not declared here, which
/// is what the fixture's `inputSchema` pins.
fn legacy_tree() -> Bridge {
    Bridge::new()
        .leaf(
            leaf(&["widget", "add"], "Add a widget", "added\n")
                .with_flags(vec![
                    FlagSchema::new("name", "string", "widget name").required(),
                    FlagSchema::new("count", "integer", "widget count"),
                    FlagSchema::new("force", "boolean", "force flag"),
                    FlagSchema::new("tag", "array", "tag list"),
                ])
                .with_enabled(default_surfaces()),
        )
        .leaf(
            leaf(&["widget", "delete"], "Delete a widget", "deleted\n").with_class(SafetyClass {
                destructive: true,
                ..SafetyClass::default()
            }),
        )
        .leaf(leaf(&["secret"], "Locked", "").with_class(SafetyClass {
            auth_required: true,
            ..SafetyClass::default()
        }))
        .leaf(leaf(&["deploy"], "Deploy", "").with_class(SafetyClass {
            requires_confirmation: true,
            ..SafetyClass::default()
        }))
        .leaf(
            // cobra attaches --help to a command on its first execution,
            // so this flag is absent from a listing taken before any
            // tools/call and present afterwards on a long-lived mount.
            leaf(&["ping"], "Ping the server", "pong\n").with_flags_on_first_execution(vec![
                FlagSchema::new("help", "boolean", "help for ping"),
            ]),
        )
}

/// The modern fixtures' command tree (Go's `modernLockTree`).
///
/// Deliberately *not* the same tree as the legacy fixtures: `widget.add`
/// declares only `name` and `count`, which is why the two `tools/list`
/// fixtures legitimately differ in their schemas.
fn modern_tree() -> Bridge {
    Bridge::new()
        .leaf(leaf(&["ping"], "Ping the server", "pong\n"))
        .leaf(
            leaf(&["widget", "add"], "Add a widget", "added\n").with_flags(vec![
                FlagSchema::new("name", "string", "widget name").required(),
                FlagSchema::new("count", "integer", "widget count"),
            ]),
        )
        .leaf(
            leaf(&["widget", "delete"], "Delete a widget", "deleted\n").with_class(SafetyClass {
                destructive: true,
                ..SafetyClass::default()
            }),
        )
        .leaf(leaf(&["secret"], "Locked", "").with_class(SafetyClass {
            auth_required: true,
            ..SafetyClass::default()
        }))
        .leaf(leaf(&["deploy"], "Deploy", "").with_class(SafetyClass {
            requires_confirmation: true,
            ..SafetyClass::default()
        }))
}

/// Builds the surface a given case runs against.
fn surface_for(case: &Case) -> Surface {
    let bridge = if case.name.starts_with("legacy/") {
        legacy_tree()
    } else {
        modern_tree()
    };
    Surface::mount(bridge, MountOptions::default()).expect("mount")
}

#[test]
fn every_fixture_case_is_byte_exact() {
    let cases = load_cases();
    assert_eq!(cases.len(), 18, "fixture count changed; re-review the port");

    let mut failures = Vec::new();
    for case in &cases {
        let surface = surface_for(case);
        let mut req = HttpRequest::post("/mcp", case.request.clone());
        for (name, value) in &case.headers {
            req = req.header(name.clone(), value.clone());
        }

        let resp = surface.call(&req);
        let got = resp.body_str();

        if resp.status != case.status || got != case.response {
            failures.push(format!(
                "case {}\n  status: got {} want {}\n  body:\n    got  {:?}\n    want {:?}",
                case.name, resp.status, case.status, got, case.response
            ));
        }
    }

    assert!(
        failures.is_empty(),
        "{} of {} fixture cases diverged from the Go wire bytes:\n\n{}",
        failures.len(),
        cases.len(),
        failures.join("\n\n")
    );
}

#[test]
fn every_case_routes_to_the_era_the_fixture_names() {
    // `era` is documentation rather than an input, but a port that
    // routes a case to the wrong handler and still matches the bytes
    // would be matching them by accident.
    use hop_top_kit::mcp::{detect_era, Era, Request};

    for case in load_cases() {
        // The parse-error case has no parseable body to classify.
        if case.name == "legacy/error/parse" {
            continue;
        }
        let parsed = Request::from_slice(case.request.as_bytes()).expect("parse fixture request");
        let want = match case.era.as_str() {
            "legacy" => Era::Legacy,
            _ => Era::Modern,
        };

        // `modern/initialize-is-legacy` is labelled modern (it is a
        // modern-era *case*) but D2 routes it to the legacy handler,
        // which is the very behavior it pins.
        let want = if case.name == "modern/initialize-is-legacy" {
            Era::Legacy
        } else {
            want
        };

        assert_eq!(
            detect_era(&case.headers, &parsed),
            want,
            "case {} routed to the wrong era",
            case.name
        );
    }
}

#[test]
fn every_sequence_replays_byte_exact_on_one_mount() {
    // Unlike `cases`, these steps share a single mount: the whole point
    // is state that legitimately carries across requests, which a
    // fresh-mount-per-step model cannot express.
    let sequences = load_sequences();
    assert_eq!(
        sequences.len(),
        1,
        "sequence count changed; re-review the port"
    );

    let mut failures = Vec::new();
    for sequence in &sequences {
        let bridge = if sequence.name.starts_with("legacy/") {
            legacy_tree()
        } else {
            modern_tree()
        };
        let surface = Surface::mount(bridge, MountOptions::default()).expect("mount");

        for (i, step) in sequence.steps.iter().enumerate() {
            let mut req = HttpRequest::post("/mcp", step.request.clone());
            for (name, value) in &step.headers {
                req = req.header(name.clone(), value.clone());
            }

            let resp = surface.call(&req);
            if resp.status != step.status || resp.body_str() != step.response {
                failures.push(format!(
                    "sequence {} step {i} ({})\n  status: got {} want {}\n                       body:\n    got  {:?}\n    want {:?}",
                    sequence.name,
                    step.name,
                    resp.status,
                    step.status,
                    resp.body_str(),
                    step.response
                ));
            }
        }
    }

    assert!(
        failures.is_empty(),
        "{} sequence step(s) diverged from the Go wire bytes:\n\n{}",
        failures.len(),
        failures.join("\n\n")
    );
}

#[test]
fn a_fresh_mount_shows_the_pre_execution_flag_set() {
    // Guards the isolation `cases` depends on: the lazy flag must be a
    // property of an executed leaf, not of the tree definition. If it
    // leaked into construction, the `cases` listings would carry `help`
    // and 18/18 would break.
    let surface = Surface::mount(legacy_tree(), MountOptions::default()).expect("mount");
    let resp = surface.call(&HttpRequest::post(
        "/mcp",
        r#"{"jsonrpc":"2.0","id":1,"method":"tools/list"}"#,
    ));
    assert!(
        !resp.body_str().contains("help"),
        "a mount that has never invoked ping must not report its help flag"
    );
}

/// The fixture's `mrtr` section: the confirmation round trip.
///
/// Unlike `cases` and `sequences` this is not byte-exact end to end.
/// Round 1 mints a fresh, time-bound `requestState` whose MAC differs
/// every run, so only its SHAPE is assertable — the fixture names the
/// members that must be present and the ones that must never appear.
/// Round 2 echoes that state back into a template and IS byte-exact,
/// which is what makes the exchange verifiable rather than merely
/// plausible: a port that fabricated a plausible-looking round 1 could
/// not produce a state its own round 2 accepts AND land on Go's bytes.
struct Mrtr {
    confirmation_key: String,
    round1_headers: Vec<(String, String)>,
    round1_request: String,
    round1_status: u16,
    round1_must_have: Vec<(String, String)>,
    round1_must_not_have: Vec<String>,
    round2_headers: Vec<(String, String)>,
    round2_request_template: String,
    round2_status: u16,
    round2_response: String,
}

fn header_pairs(v: &Value) -> Vec<(String, String)> {
    v.as_object()
        .map(|h| {
            h.iter()
                .map(|(k, val)| (k.clone(), val.as_str().unwrap().to_owned()))
                .collect()
        })
        .unwrap_or_default()
}

fn load_mrtr() -> Mrtr {
    let doc = load_doc();
    let m = &doc["mrtr"];
    assert!(m.is_object(), "fixture has no mrtr section");
    Mrtr {
        confirmation_key: m["confirmation_key"].as_str().unwrap().to_owned(),
        round1_headers: header_pairs(&m["round1_headers"]),
        round1_request: m["round1_request"].as_str().unwrap().to_owned(),
        round1_status: m["round1_status"].as_u64().unwrap() as u16,
        round1_must_have: m["round1_must_have"]
            .as_object()
            .expect("round1_must_have object")
            .iter()
            .map(|(k, v)| (k.clone(), v.as_str().unwrap().to_owned()))
            .collect(),
        round1_must_not_have: m["round1_must_not_have"]
            .as_array()
            .expect("round1_must_not_have array")
            .iter()
            .map(|v| v.as_str().unwrap().to_owned())
            .collect(),
        round2_headers: header_pairs(&m["round2_headers"]),
        round2_request_template: m["round2_request_template"].as_str().unwrap().to_owned(),
        round2_status: m["round2_status"].as_u64().unwrap() as u16,
        round2_response: m["round2_response"].as_str().unwrap().to_owned(),
    }
}

/// The Go MRTR lock tree, plus the execution counter the exchange turns
/// on: round 1's whole point is that the leaf does NOT run, which only
/// a count can prove.
///
/// `vault.burn` is Go's destructive-AND-requires-confirmation leaf: a
/// fully accepted exchange still meets the policy gate behind it. A leaf
/// that is merely destructive never enters the confirmation gate at all.
fn mrtr_tree() -> (Bridge, Arc<AtomicUsize>) {
    let executions = Arc::new(AtomicUsize::new(0));

    let purge_count = Arc::clone(&executions);
    let burn_count = Arc::clone(&executions);

    let bridge = Bridge::new()
        .leaf(
            Leaf::new(&["purge"], "Purge a target", move |args| {
                purge_count.fetch_add(1, Ordering::SeqCst);
                let target = args.get("target").and_then(Value::as_str).unwrap_or("");
                Ok(CallResult::ok(format!("purged {target}\n")))
            })
            .with_flags(vec![FlagSchema::new("target", "string", "what to purge")])
            .with_class(SafetyClass {
                requires_confirmation: true,
                ..SafetyClass::default()
            }),
        )
        .leaf(
            Leaf::new(&["vault", "burn"], "Burn the vault", move |_| {
                burn_count.fetch_add(1, Ordering::SeqCst);
                Ok(CallResult::ok("burned\n"))
            })
            .with_class(SafetyClass {
                requires_confirmation: true,
                destructive: true,
                ..SafetyClass::default()
            }),
        );

    (bridge, executions)
}

/// Reads a dotted path out of a decoded result, for the fixture's
/// shape assertions.
fn dig<'a>(root: &'a Value, path: &str) -> Option<&'a Value> {
    let mut cur = root;
    for seg in path.split('.') {
        cur = cur.as_object()?.get(seg)?;
    }
    Some(cur)
}

#[test]
fn the_mrtr_exchange_prompts_then_lands_on_the_go_bytes() {
    let mrtr = load_mrtr();
    let (bridge, executions) = mrtr_tree();

    // ONE mount for both rounds, keyed with the fixture's key: the state
    // is a MAC over it, so a mount with a different key cannot replay
    // round 2.
    let surface = Surface::mount(
        bridge,
        MountOptions {
            confirmation_key: Some(mrtr.confirmation_key.clone().into_bytes()),
            ..MountOptions::default()
        },
    )
    .expect("mount");

    // --- Round 1: the prompt -------------------------------------------
    let mut req = HttpRequest::post("/mcp", mrtr.round1_request.clone());
    for (name, value) in &mrtr.round1_headers {
        req = req.header(name.clone(), value.clone());
    }
    let r1 = surface.call(&req);
    assert_eq!(r1.status, mrtr.round1_status, "round1 status");

    let body1: Value = serde_json::from_str(r1.body_str()).expect("round1 body is JSON");
    let result1 = body1
        .get("result")
        .filter(|v| v.is_object())
        .expect("round1 carries a result");

    // The leaf must NOT have run: that is the entire defect this section
    // exists to catch. A port gating on X-Confirm-Token alone would have
    // refused at 428 above; a port with no gate at all executes here.
    assert_eq!(
        executions.load(Ordering::SeqCst),
        0,
        "leaf executed before confirmation"
    );

    for (path, want) in &mrtr.round1_must_have {
        assert_eq!(
            dig(result1, path).and_then(Value::as_str),
            Some(want.as_str()),
            "round1 {path}"
        );
    }
    for absent in &mrtr.round1_must_not_have {
        assert!(
            result1.get(absent).is_none(),
            "round1 must not carry {absent}"
        );
    }

    // Exactly one entry, under the reserved "confirm" key.
    let keys: Vec<&str> = result1["inputRequests"]
        .as_object()
        .expect("inputRequests object")
        .keys()
        .map(String::as_str)
        .collect();
    assert_eq!(keys, ["confirm"], "inputRequests keys");

    // `v1.<expiry-base10>.<mac>` — three dot-separated parts. The MAC is
    // production-derived and never compared.
    let state = result1["requestState"]
        .as_str()
        .expect("requestState is a string");
    let parts: Vec<&str> = state.split('.').collect();
    assert_eq!(parts.len(), 3, "requestState part count: {state}");
    assert_eq!(parts[0], "v1", "requestState version");
    assert!(
        !parts[1].is_empty() && parts[1].bytes().all(|b| b.is_ascii_digit()),
        "requestState expiry is base-10: {}",
        parts[1]
    );
    assert!(!parts[2].is_empty(), "requestState mac is non-empty");

    // --- Round 2: the accepted retry, byte-exact ------------------------
    let body2 = mrtr
        .round2_request_template
        .replace("{{requestState}}", state);
    let mut req = HttpRequest::post("/mcp", body2);
    for (name, value) in &mrtr.round2_headers {
        req = req.header(name.clone(), value.clone());
    }
    let r2 = surface.call(&req);
    assert_eq!(r2.status, mrtr.round2_status, "round2 status");
    assert_eq!(
        r2.body_str(),
        mrtr.round2_response,
        "round2 body must be byte-exact against the Go wire bytes"
    );
    assert_eq!(
        executions.load(Ordering::SeqCst),
        1,
        "executions after accept"
    );
}

/// A state minted under a different key cannot replay round 2.
///
/// The happy-path exchange above only ever presents a genuine state, so
/// on its own it cannot tell a mount that verifies the MAC from one that
/// waves any well-formed token through. Here the token is correctly
/// framed and correctly bound to the call — only the key behind the MAC
/// differs — and it must still be refused.
///
/// Re-prompting rather than erroring is the deliberate choice: the user
/// can still approve, and a token error is not actionable by them. What
/// must never happen is the leaf running.
#[test]
fn the_mrtr_retry_is_refused_when_the_state_was_minted_under_another_key() {
    let mrtr = load_mrtr();

    // The forger's mount: same tree, same call, different secret.
    let (forger_bridge, _forger_executions) = mrtr_tree();
    let forger = Surface::mount(
        forger_bridge,
        MountOptions {
            confirmation_key: Some(b"a-different-suite-shared-secret!!".to_vec()),
            ..MountOptions::default()
        },
    )
    .expect("forger mount");

    let mut req = HttpRequest::post("/mcp", mrtr.round1_request.clone());
    for (name, value) in &mrtr.round1_headers {
        req = req.header(name.clone(), value.clone());
    }
    let minted_body: Value =
        serde_json::from_str(forger.call(&req).body_str()).expect("foreign round1 body is JSON");
    let minted = minted_body["result"]["requestState"]
        .as_str()
        .expect("foreign round1 minted a state string")
        .to_owned();

    // The real mount, keyed by the fixture.
    let (bridge, executions) = mrtr_tree();
    let surface = Surface::mount(
        bridge,
        MountOptions {
            confirmation_key: Some(mrtr.confirmation_key.clone().into_bytes()),
            ..MountOptions::default()
        },
    )
    .expect("mount");

    let body = mrtr
        .round2_request_template
        .replace("{{requestState}}", &minted);
    let mut req = HttpRequest::post("/mcp", body);
    for (name, value) in &mrtr.round2_headers {
        req = req.header(name.clone(), value.clone());
    }
    let res = surface.call(&req);

    let decoded: Value = serde_json::from_str(res.body_str()).expect("foreign round2 body is JSON");
    let result = decoded
        .get("result")
        .filter(|v| v.is_object())
        .expect("foreign round2 carries a result");

    assert_eq!(
        result["resultType"].as_str(),
        Some("input_required"),
        "foreign round2 must re-prompt"
    );
    assert_eq!(
        executions.load(Ordering::SeqCst),
        0,
        "leaf ran on a state minted under another key"
    );
}
