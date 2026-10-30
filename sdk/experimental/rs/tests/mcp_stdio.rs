//! The stdio binding: newline framing, notifications never answered,
//! ping, parse / batch refusals, concurrent requests, both eras with no
//! header layer, fail-closed gates, and a clean end of session.

#![cfg(feature = "mcp")]

use std::io::{self, Cursor, Write};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hop_top_kit::mcp::safety::SafetyClass;
use hop_top_kit::mcp::{
    Bridge, CallResult, HttpRequest, Identity, Leaf, MountOptions, SpecVersion, Surface, Verifier,
};
use serde_json::Value;

const MODERN_META: &str = r#""_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2026-07-28"}"#;

fn ping_bridge() -> Bridge {
    Bridge::new().leaf(Leaf::new(&["ping"], "Ping the server", |_| {
        Ok(CallResult::ok("pong\n"))
    }))
}

fn mount(bridge: Bridge, options: MountOptions) -> Surface {
    Surface::mount(bridge, options).expect("mount")
}

/// Serves `input` to completion; returns the output lines and the log.
fn run(surface: &Surface, input: &str) -> (Vec<String>, String) {
    let mut out = Vec::new();
    let mut log = Vec::new();
    surface
        .serve_stdio(Cursor::new(input.as_bytes().to_vec()), &mut out, &mut log)
        .expect("session");
    (lines(&out), String::from_utf8(log).expect("utf-8 log"))
}

fn lines(out: &[u8]) -> Vec<String> {
    let text = std::str::from_utf8(out).expect("utf-8 output");
    assert!(
        text.is_empty() || text.ends_with('\n'),
        "every output line is newline-terminated: {text:?}"
    );
    text.lines().map(ToOwned::to_owned).collect()
}

fn decode(line: &str) -> Value {
    serde_json::from_str(line).expect("each output line is one JSON message")
}

/// The decoded reply whose id equals `id`.
fn reply(lines: &[String], id: Value) -> Value {
    lines
        .iter()
        .map(|l| decode(l))
        .find(|m| m["id"] == id)
        .unwrap_or_else(|| panic!("no reply with id {id}: {lines:?}"))
}

#[test]
fn runs_the_legacy_handshake_and_never_answers_a_notification() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(
        &surface,
        concat!(
            r#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"pipe","version":"0"}}}"#,
            "\n",
            r#"{"jsonrpc":"2.0","method":"notifications/initialized"}"#,
            "\n",
            r#"{"jsonrpc":"2.0","id":2,"method":"tools/list"}"#,
            "\n",
            r#"{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping"}}"#,
            "\n",
        ),
    );
    assert_eq!(lines.len(), 3, "{lines:?}");
    assert_eq!(
        reply(&lines, 1.into())["result"]["protocolVersion"],
        "2024-11-05"
    );
    assert!(reply(&lines, 2.into())["result"]["tools"].is_array());
    assert!(reply(&lines, 3.into())["result"]
        .to_string()
        .contains("pong"));
}

#[test]
fn answers_ping_with_an_empty_result() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(
        &surface,
        "{\"jsonrpc\":\"2.0\",\"id\":\"p1\",\"method\":\"ping\"}\n",
    );
    assert_eq!(lines, [r#"{"jsonrpc":"2.0","id":"p1","result":{}}"#]);
}

#[test]
fn drops_every_notification_even_a_malformed_or_failing_one() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(
        &surface,
        &[
            r#"{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}"#,
            r#"{"jsonrpc":"1.0","method":"notifications/initialized"}"#,
            r#"{"jsonrpc":"2.0","method":"tools/call","params":{"name":"ping"}}"#,
            r#"{"jsonrpc":"2.0","method":"nope"}"#,
            &format!(r#"{{"jsonrpc":"2.0","method":"tools/list","params":{{{MODERN_META}}}}}"#),
            r#"{"jsonrpc":"2.0"}"#,
            "",
        ]
        .join("\n"),
    );
    assert!(lines.is_empty(), "{lines:?}");
}

#[test]
fn answers_unparseable_input_with_32700_and_id_null() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(&surface, "{not json\n");
    assert_eq!(
        lines,
        [
            r#"{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"parse error: invalid character 'n' looking for beginning of object key string"}}"#
        ]
    );
}

#[test]
fn refuses_a_batch_and_a_non_object_with_32600_and_id_null() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(
        &surface,
        "[{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}]\n42\n",
    );
    assert_eq!(
        lines,
        [
            r#"{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request: batch requests are not supported"}}"#,
            r#"{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"invalid request: not a JSON-RPC object"}}"#,
        ]
    );
}

#[test]
fn answers_a_request_without_a_method_with_32600_and_its_id() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(&surface, "{\"jsonrpc\":\"2.0\",\"id\":9}\n");
    assert_eq!(
        lines,
        [
            r#"{"jsonrpc":"2.0","id":9,"error":{"code":-32600,"message":"invalid request: missing method"}}"#
        ]
    );
}

#[test]
fn ignores_a_client_response_and_logs_it() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, log) = run(
        &surface,
        "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"error\":{\"code\":1,\"message\":\"x\"}}\n",
    );
    assert!(lines.is_empty(), "{lines:?}");
    assert_eq!(
        log.matches("ignored a JSON-RPC response").count(),
        2,
        "{log}"
    );
}

#[test]
fn skips_blank_lines_accepts_crlf_and_a_missing_final_newline() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, log) = run(
        &surface,
        "\n   \n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\r\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}",
    );
    assert_eq!(
        lines,
        [
            r#"{"jsonrpc":"2.0","id":1,"result":{}}"#,
            r#"{"jsonrpc":"2.0","id":2,"result":{}}"#,
        ]
    );
    assert!(log.is_empty(), "{log}");
}

#[test]
fn serves_2026_07_28_from_the_body_alone() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(
        &surface,
        &[
            format!(r#"{{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{{{MODERN_META}}}}}"#),
            format!(r#"{{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{{"name":"ping",{MODERN_META}}}}}"#),
            format!(r#"{{"jsonrpc":"2.0","id":3,"method":"ping","params":{{{MODERN_META}}}}}"#),
            format!(r#"{{"jsonrpc":"2.0","id":4,"method":"tools/list","params":{{{MODERN_META}}}}}"#),
            String::new(),
        ]
        .join("\n"),
    );
    assert_eq!(lines.len(), 4, "{lines:?}");
    let discover = reply(&lines, 1.into());
    assert_eq!(discover["result"]["resultType"], "complete");
    assert_eq!(
        discover["result"]["supportedVersions"],
        serde_json::json!(["2026-07-28"])
    );
    let call = reply(&lines, 2.into());
    assert_eq!(call["result"]["resultType"], "complete", "{call}");
    assert_eq!(call["result"]["isError"], false, "{call}");
    assert!(call["result"].to_string().contains("pong"));
    // 2026-07-28 removed ping: a modern ping is an unknown method.
    assert_eq!(reply(&lines, 3.into())["error"]["code"], -32601);
    assert_eq!(reply(&lines, 4.into())["result"]["resultType"], "complete");
}

#[test]
fn keeps_the_remaining_modern_validation() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(
        &surface,
        concat!(
            // V5: unsupported version.
            r#"{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/protocolVersion":"2099-01-01"}}}"#,
            "\n",
            // V3: capabilities missing.
            r#"{"jsonrpc":"2.0","id":2,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}"#,
            "\n",
            // V2: a fractional id.
            r#"{"jsonrpc":"2.0","id":1.5,"method":"server/discover"}"#,
            "\n",
        ),
    );
    assert_eq!(lines.len(), 3, "{lines:?}");
    assert_eq!(reply(&lines, 1.into())["error"]["code"], -32022);
    assert_eq!(reply(&lines, 2.into())["error"]["code"], -32602);
    assert_eq!(reply(&lines, 1.5.into())["error"]["code"], -32600);
}

#[test]
fn a_modern_tools_call_without_a_name_fails_v9_not_a_header_check() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, _) = run(
        &surface,
        &format!("{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{{{MODERN_META}}}}}\n"),
    );
    assert_eq!(
        lines,
        [r#"{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing tool name"}}"#]
    );
}

#[test]
fn applies_mount_options() {
    let surface = mount(
        ping_bridge(),
        MountOptions {
            server_name: "stdio-tool".into(),
            server_version: "9.9.9".into(),
            ..MountOptions::default()
        },
    );
    let (lines, _) = run(
        &surface,
        "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n",
    );
    assert_eq!(
        reply(&lines, 1.into())["result"]["serverInfo"],
        serde_json::json!({"name": "stdio-tool", "version": "9.9.9"})
    );

    let modern_only = mount(
        ping_bridge(),
        MountOptions {
            spec_versions: Some(vec![SpecVersion::V2026_07_28]),
            ..MountOptions::default()
        },
    );
    let (lines, _) = run(
        &modern_only,
        "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n",
    );
    assert!(
        lines[0].contains("supported protocol versions: 2026-07-28"),
        "{lines:?}"
    );
}

/// A bridge with an auth-required `secret` and a confirmation-gated
/// `drop` leaf, counting runs of either.
fn gated_bridge(runs: &Arc<AtomicUsize>) -> Bridge {
    let secret_runs = Arc::clone(runs);
    let drop_runs = Arc::clone(runs);
    Bridge::new()
        .leaf(
            Leaf::new(&["secret"], "Locked", move |_| {
                secret_runs.fetch_add(1, Ordering::SeqCst);
                Ok(CallResult::ok("opened\n"))
            })
            .with_class(SafetyClass {
                auth_required: true,
                ..SafetyClass::default()
            }),
        )
        .leaf(
            Leaf::new(&["drop"], "Needs a yes", move |_| {
                drop_runs.fetch_add(1, Ordering::SeqCst);
                Ok(CallResult::ok("dropped\n"))
            })
            .with_class(SafetyClass {
                requires_confirmation: true,
                ..SafetyClass::default()
            }),
        )
}

fn calls_on_both_eras(name: &str) -> String {
    format!(
        "{{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{{\"name\":\"{name}\"}}}}\n\
         {{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{{\"name\":\"{name}\",{MODERN_META}}}}}\n"
    )
}

#[test]
fn auth_required_fails_closed_and_the_verifier_sees_no_headers() {
    let runs = Arc::new(AtomicUsize::new(0));
    let seen = Arc::new(Mutex::new(Vec::new()));
    let seen_by_verifier = Arc::clone(&seen);
    let surface = mount(
        gated_bridge(&runs),
        MountOptions {
            verifier: Some(Verifier::new(move |req: &HttpRequest| {
                seen_by_verifier
                    .lock()
                    .unwrap()
                    .push((req.method.clone(), req.headers.len()));
                // Admits only a bearer token stdio cannot carry.
                req.headers
                    .iter()
                    .any(|(k, v)| k.eq_ignore_ascii_case("authorization") && v == "Bearer good")
                    .then(Identity::default)
            })),
            ..MountOptions::default()
        },
    );
    let (lines, _) = run(&surface, &calls_on_both_eras("secret"));
    assert_eq!(lines.len(), 2, "{lines:?}");
    for id in [1, 2] {
        let got = reply(&lines, id.into());
        assert_eq!(got["result"]["isError"], true, "{got}");
        assert!(got.to_string().contains("authentication required"), "{got}");
    }
    assert_eq!(runs.load(Ordering::SeqCst), 0);
    assert_eq!(
        *seen.lock().unwrap(),
        [("POST".to_owned(), 0), ("POST".to_owned(), 0)],
        "the verifier runs once per tools/call, with a header-less request"
    );
}

#[test]
fn auth_required_runs_when_the_verifier_admits_the_caller() {
    let runs = Arc::new(AtomicUsize::new(0));
    let surface = mount(
        gated_bridge(&runs),
        MountOptions {
            verifier: Some(Verifier::new(|_| {
                Some(Identity {
                    caller: "local".into(),
                    ..Identity::default()
                })
            })),
            ..MountOptions::default()
        },
    );
    let (lines, _) = run(&surface, &calls_on_both_eras("secret"));
    for id in [1, 2] {
        assert!(reply(&lines, id.into()).to_string().contains("opened"));
    }
    assert_eq!(runs.load(Ordering::SeqCst), 2);
}

#[test]
fn auth_required_without_a_verifier_is_refused() {
    let runs = Arc::new(AtomicUsize::new(0));
    let surface = mount(gated_bridge(&runs), MountOptions::default());
    let (lines, _) = run(&surface, &calls_on_both_eras("secret"));
    for id in [1, 2] {
        assert!(reply(&lines, id.into())
            .to_string()
            .contains("authentication required"));
    }
    assert_eq!(runs.load(Ordering::SeqCst), 0);
}

#[test]
fn the_confirmation_header_gate_cannot_be_satisfied() {
    let runs = Arc::new(AtomicUsize::new(0));
    let surface = mount(gated_bridge(&runs), MountOptions::default());
    let (lines, _) = run(&surface, &calls_on_both_eras("drop"));
    for id in [1, 2] {
        let got = reply(&lines, id.into());
        assert_eq!(got["result"]["isError"], true, "{got}");
        assert!(got.to_string().contains("confirmation required"), "{got}");
    }
    assert_eq!(runs.load(Ordering::SeqCst), 0);
}

/// A modern `tools/call` on `drop` declaring `caps` as client capabilities.
fn drop_call(id: u64, caps: Value, extra: Value) -> String {
    let mut params = serde_json::json!({
        "name": "drop",
        "_meta": {
            "io.modelcontextprotocol/clientCapabilities": caps,
            "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        },
    });
    if let (Some(p), Some(e)) = (params.as_object_mut(), extra.as_object()) {
        p.extend(e.clone());
    }
    let msg =
        serde_json::json!({"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": params});
    format!("{msg}\n")
}

#[test]
fn confirms_in_band_over_mrtr_and_refuses_a_client_without_elicitation() {
    let runs = Arc::new(AtomicUsize::new(0));
    let surface = mount(
        gated_bridge(&runs),
        MountOptions {
            confirmation_key: Some(b"stdio-mrtr-suite-shared-secret-32".to_vec()),
            ..MountOptions::default()
        },
    );
    let form = serde_json::json!({"elicitation": {"form": {}}});

    // The state is stateless and self-contained, so each round trip may
    // run in its own session.
    let (lines, _) = run(&surface, &drop_call(1, form.clone(), Value::Null));
    let first = reply(&lines, 1.into());
    assert_eq!(first["result"]["resultType"], "input_required", "{first}");
    let keys: Vec<&String> = first["result"]["inputRequests"]
        .as_object()
        .expect("inputRequests object")
        .keys()
        .collect();
    assert_eq!(keys, ["confirm"]);
    assert_eq!(runs.load(Ordering::SeqCst), 0);

    let retry = serde_json::json!({
        "requestState": first["result"]["requestState"],
        "inputResponses": {"confirm": {"action": "accept"}},
    });
    let (lines, _) = run(&surface, &drop_call(2, form, retry));
    let second = reply(&lines, 2.into());
    assert_eq!(second["result"]["resultType"], "complete", "{second}");
    assert_eq!(second["result"]["isError"], false, "{second}");
    assert_eq!(runs.load(Ordering::SeqCst), 1);

    // No elicitation declared: the header gate applies, and stdio has no headers.
    let (lines, _) = run(&surface, &drop_call(3, serde_json::json!({}), Value::Null));
    let third = reply(&lines, 3.into());
    assert_eq!(third["result"]["resultType"], "complete", "{third}");
    assert_eq!(third["result"]["isError"], true, "{third}");
    assert!(
        third.to_string().contains("confirmation required"),
        "{third}"
    );
    assert_eq!(runs.load(Ordering::SeqCst), 1);
}

/// Output that reports each completed line to `lines_tx`.
struct Signalling {
    buf: Arc<Mutex<Vec<u8>>>,
    lines_tx: Sender<()>,
}

impl Write for Signalling {
    fn write(&mut self, data: &[u8]) -> io::Result<usize> {
        self.buf.lock().unwrap().extend_from_slice(data);
        for _ in data.iter().filter(|b| **b == b'\n') {
            let _ = self.lines_tx.send(());
        }
        Ok(data.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[test]
fn runs_requests_concurrently_replies_out_of_order_each_with_its_id() {
    // `slow` answers only once a reply line has been written, so it can
    // finish only if `fast`, read after it, runs alongside it.
    let (lines_tx, lines_rx) = mpsc::channel();
    let lines_rx: Arc<Mutex<Receiver<()>>> = Arc::new(Mutex::new(lines_rx));
    let bridge = Bridge::new()
        .leaf(Leaf::new(&["slow"], "Slow", move |_| {
            let waited = lines_rx
                .lock()
                .unwrap()
                .recv_timeout(Duration::from_secs(10));
            Ok(CallResult::ok(if waited.is_ok() {
                "slow\n"
            } else {
                "slow timed out\n"
            }))
        }))
        .leaf(Leaf::new(&["fast"], "Fast", |_| {
            Ok(CallResult::ok("fast\n"))
        }));
    let surface = mount(bridge, MountOptions::default());

    let buf = Arc::new(Mutex::new(Vec::new()));
    let output = Signalling {
        buf: Arc::clone(&buf),
        lines_tx,
    };
    let input = concat!(
        r#"{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"slow"}}"#,
        "\n",
        r#"{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fast"}}"#,
        "\n",
    );
    surface
        .serve_stdio(Cursor::new(input), output, io::sink())
        .expect("session");

    // Returning at all means the in-flight `slow` was waited for.
    let lines = lines(&buf.lock().unwrap());
    let ids: Vec<Value> = lines.iter().map(|l| decode(l)["id"].clone()).collect();
    assert_eq!(ids, [Value::from(2), Value::from(1)], "{lines:?}");
    assert!(lines[0].contains("fast"));
    assert!(lines[1].contains("\"slow\\n\""), "{lines:?}");
}

#[test]
fn a_panicking_leaf_answers_32603_and_logs_the_cause() {
    let bridge = Bridge::new().leaf(Leaf::new(&["boom"], "Panics", |_| panic!("kaboom")));
    let surface = mount(bridge, MountOptions::default());
    let (lines, log) = run(
        &surface,
        concat!(
            r#"{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"boom"}}"#,
            "\n",
            r#"{"jsonrpc":"2.0","method":"tools/call","params":{"name":"boom"}}"#,
            "\n",
            r#"{"jsonrpc":"2.0","id":5,"method":"ping"}"#,
            "\n",
        ),
    );
    assert_eq!(
        reply(&lines, 4.into()),
        serde_json::json!({"jsonrpc":"2.0","id":4,"error":{"code":-32603,"message":"internal error"}})
    );
    // The session survives the panic; the notification stays silent.
    assert_eq!(lines.len(), 2, "{lines:?}");
    assert_eq!(reply(&lines, 5.into())["result"], serde_json::json!({}));
    assert!(log.contains("kaboom"), "{log}");
}

/// Output that refuses every write.
struct Broken;

impl Write for Broken {
    fn write(&mut self, _: &[u8]) -> io::Result<usize> {
        Err(io::Error::new(io::ErrorKind::BrokenPipe, "pipe closed"))
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[test]
fn a_broken_output_ends_the_session_with_the_error() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let mut log = Vec::new();
    let err = surface
        .serve_stdio(
            Cursor::new("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n"),
            Broken,
            &mut log,
        )
        .expect_err("a write failure is reported");
    assert_eq!(err.kind(), io::ErrorKind::BrokenPipe);
    assert!(String::from_utf8(log).unwrap().contains("output error"));
}

#[test]
fn empty_input_ends_the_session_cleanly() {
    let surface = mount(ping_bridge(), MountOptions::default());
    let (lines, log) = run(&surface, "");
    assert!(lines.is_empty());
    assert!(log.is_empty());
}
