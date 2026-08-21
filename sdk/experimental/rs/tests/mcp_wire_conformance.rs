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
                    steps: s["steps"].as_array().unwrap().iter().map(parse_case).collect(),
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
        .leaf(
            leaf(&["secret"], "Locked", "").with_class(SafetyClass {
                auth_required: true,
                ..SafetyClass::default()
            }),
        )
        .leaf(
            leaf(&["deploy"], "Deploy", "").with_class(SafetyClass {
                requires_confirmation: true,
                ..SafetyClass::default()
            }),
        )
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
        .leaf(
            leaf(&["secret"], "Locked", "").with_class(SafetyClass {
                auth_required: true,
                ..SafetyClass::default()
            }),
        )
        .leaf(
            leaf(&["deploy"], "Deploy", "").with_class(SafetyClass {
                requires_confirmation: true,
                ..SafetyClass::default()
            }),
        )
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
