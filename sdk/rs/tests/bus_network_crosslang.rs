//! Go and Rust peers on one bus network: a Go hub with a Rust client, a
//! Rust hub with a Go client (each with and without auth), and a
//! three-node relay mixing both, under each hub. Every event must arrive
//! exactly once where it should and never echo back.
//!
//! The Go peer is `sdk/tests/cross-lang/bus-network/gonode`, built once
//! with `go build`. Set `BUS_NETWORK_CROSSLANG=1` to run (`make
//! test-parity-bus-network` does); without it each test returns early, so
//! a plain `cargo test` needs no Go toolchain.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::atomic::Ordering;
use std::sync::{mpsc, Arc, Mutex, OnceLock};
use std::thread;
use std::time::Duration;

use bus_network_support::*;
use hop_top_kit::bus::network::{NetworkOptions, StaticTokenAuth};
use serde_json::{json, Value};

const FROM_GO: &str = "xlang.go.event.published";
const FROM_RS: &str = "xlang.rust.event.published";
/// How long a settled count must hold: echoes and duplicates show up
/// well within it.
const SETTLE: u64 = 400;

fn enabled() -> bool {
    let on = std::env::var("BUS_NETWORK_CROSSLANG").is_ok_and(|v| !v.is_empty());
    if !on {
        eprintln!("set BUS_NETWORK_CROSSLANG=1 to run the Go/Rust bus network gate");
    }
    on
}

fn repo_root() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../..")
}

/// Builds the Go peer once per test binary.
fn gonode_bin() -> PathBuf {
    static BIN: OnceLock<PathBuf> = OnceLock::new();
    BIN.get_or_init(|| {
        let out = std::env::temp_dir().join(format!("kit-bus-gonode-{}", std::process::id()));
        let status = Command::new("go")
            .args(["build", "-o"])
            .arg(&out)
            .arg("./sdk/tests/cross-lang/bus-network/gonode")
            .current_dir(repo_root())
            .status()
            .expect("run go build");
        assert!(status.success(), "go build gonode failed");
        out
    })
    .clone()
}

/// A running Go peer.
struct GoNode {
    child: Child,
    stdin: ChildStdin,
    url: String,
    got: Arc<Mutex<Vec<Value>>>,
}

impl GoNode {
    fn start(args: &[&str]) -> GoNode {
        let mut child = Command::new(gonode_bin())
            .args(args)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .expect("start gonode");
        let stdin = child.stdin.take().unwrap();
        let stdout = child.stdout.take().unwrap();
        let got = Arc::new(Mutex::new(Vec::new()));
        let (ready_tx, ready_rx) = mpsc::channel();
        let g = Arc::clone(&got);
        thread::spawn(move || {
            for line in BufReader::new(stdout).lines().map_while(Result::ok) {
                let v: Value = serde_json::from_str(&line).expect("gonode line");
                match v["event"].as_str() {
                    Some("ready") => {
                        let _ = ready_tx.send(v["url"].as_str().unwrap_or_default().to_string());
                    }
                    Some("got") => g.lock().unwrap().push(v),
                    _ => panic!("unexpected gonode line {line}"),
                }
            }
        });
        let url = ready_rx
            .recv_timeout(WATCHDOG)
            .expect("gonode never became ready");
        GoNode {
            child,
            stdin,
            url,
            got,
        }
    }

    fn hub(origin: &str, extra: &[&str]) -> GoNode {
        let mut args = vec!["-mode", "hub", "-origin", origin];
        args.extend_from_slice(extra);
        GoNode::start(&args)
    }

    fn client(origin: &str, url: &str, extra: &[&str]) -> GoNode {
        let mut args = vec!["-mode", "client", "-origin", origin, "-connect", url];
        args.extend_from_slice(extra);
        GoNode::start(&args)
    }

    fn publish(&mut self, topic: &str, source: &str, payload: Value) {
        let cmd = json!({"cmd": "publish", "topic": topic, "source": source, "payload": payload});
        writeln!(self.stdin, "{cmd}").expect("gonode stdin");
    }

    /// Events of `topic` from `source` the Go bus delivered.
    fn count(&self, topic: &str, source: &str) -> usize {
        self.got
            .lock()
            .unwrap()
            .iter()
            .filter(|v| v["topic"] == topic && v["source"] == source)
            .count()
    }

    fn payload_of(&self, topic: &str) -> Option<Value> {
        self.got
            .lock()
            .unwrap()
            .iter()
            .find(|v| v["topic"] == topic)
            .map(|v| v["payload"].clone())
    }
}

impl Drop for GoNode {
    fn drop(&mut self) {
        let _ = writeln!(self.stdin, r#"{{"cmd":"quit"}}"#);
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        while std::time::Instant::now() < deadline {
            if let Ok(Some(_)) = self.child.try_wait() {
                return;
            }
            thread::sleep(Duration::from_millis(20));
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

/// Counts deliveries of `topic` from `source` on a Rust bus.
fn rs_count(
    bus: &hop_top_kit::bus::DynBus,
    topic: &str,
    source: &str,
) -> Arc<std::sync::atomic::AtomicUsize> {
    let n = Arc::new(std::sync::atomic::AtomicUsize::new(0));
    let (c, s) = (Arc::clone(&n), source.to_string());
    bus.subscribe(topic, move |e| {
        if e.source == s {
            c.fetch_add(1, Ordering::SeqCst);
        }
        Ok(())
    });
    n
}

fn settle_exactly(what: &str, want: usize, count: impl Fn() -> usize) {
    assert!(
        eventually(DELIVERY, || count() >= want),
        "{what}: got {} of {want}",
        count()
    );
    sleep_ms(SETTLE);
    assert_eq!(
        count(),
        want,
        "{what}: exactly {want}, no echo or duplicate"
    );
}

fn opts(origin: &str, token: Option<&str>) -> NetworkOptions {
    let o = NetworkOptions::new().origin_id(origin);
    match token {
        Some(t) => o.auth(StaticTokenAuth::new(t)),
        None => o,
    }
}

fn go_hub_rust_client(token: Option<&str>) {
    let extra: Vec<&str> = token.map(|t| vec!["-token", t]).unwrap_or_default();
    let mut go = GoNode::hub("go-hub", &extra);
    let rs = Peer::new(opts("rs-client", token));
    rs.connect(&go.url)
        .expect("Rust client connects to the Go hub");
    let rs_from_go = rs_count(&rs.bus, FROM_GO, "go");
    let rs_from_rs = rs_count(&rs.bus, FROM_RS, "rs");

    publish(&rs.bus, ev(FROM_RS, "rs", json!({"n": 1, "who": "rust"})));
    settle_exactly("Go hub gets the Rust event", 1, || go.count(FROM_RS, "rs"));
    assert_eq!(go.payload_of(FROM_RS), Some(json!({"n": 1, "who": "rust"})));

    go.publish(FROM_GO, "go", json!({"n": 2}));
    settle_exactly("Rust client gets the Go event", 1, || {
        rs_from_go.load(Ordering::SeqCst)
    });
    assert_eq!(go.count(FROM_GO, "go"), 1, "no echo back to the Go hub");
    assert_eq!(rs_from_rs.load(Ordering::SeqCst), 1, "no echo back to Rust");
}

fn rust_hub_go_client(token: Option<&str>) {
    let hub = Hub::start(opts("rs-hub", token));
    let extra: Vec<&str> = token.map(|t| vec!["-token", t]).unwrap_or_default();
    let mut go = GoNode::client("go-client", &hub.url, &extra);
    let rs_from_go = rs_count(&hub.bus, FROM_GO, "go");
    let rs_from_rs = rs_count(&hub.bus, FROM_RS, "rs");

    go.publish(FROM_GO, "go", json!({"n": 1}));
    settle_exactly("Rust hub gets the Go event", 1, || {
        rs_from_go.load(Ordering::SeqCst)
    });

    publish(&hub.bus, ev(FROM_RS, "rs", json!({"n": 2})));
    settle_exactly("Go client gets the Rust event", 1, || {
        go.count(FROM_RS, "rs")
    });
    assert_eq!(go.count(FROM_GO, "go"), 1, "no echo back to Go");
    assert_eq!(
        rs_from_rs.load(Ordering::SeqCst),
        1,
        "no echo back to the Rust hub"
    );
}

#[test]
fn go_hub_rust_client_bidirectional() {
    if enabled() {
        go_hub_rust_client(None);
    }
}

#[test]
fn go_hub_rust_client_with_auth() {
    if enabled() {
        go_hub_rust_client(Some("xlang-secret"));
    }
}

#[test]
fn rust_hub_go_client_bidirectional() {
    if enabled() {
        rust_hub_go_client(None);
    }
}

/// The Go dialer advertises stream caps in its auth frame; the Rust hub
/// answers with the plain ack and the link runs without streams.
#[test]
fn rust_hub_go_client_with_auth() {
    if enabled() {
        rust_hub_go_client(Some("xlang-secret"));
    }
}

#[test]
fn rust_hub_refuses_a_go_client_with_the_wrong_token() {
    if !enabled() {
        return;
    }
    let hub = Hub::start(opts("rs-hub", Some("right")));
    let out = Command::new(gonode_bin())
        .args([
            "-mode", "client", "-origin", "go", "-connect", &hub.url, "-token", "wrong",
        ])
        .stdin(Stdio::null())
        .output()
        .expect("run gonode");
    assert!(!out.status.success(), "Go client must fail to connect");
    let err = String::from_utf8_lossy(&out.stderr);
    assert!(err.contains("bus: network auth failed"), "{err}");
}

/// Rust hub relaying between a Go leaf and a Rust leaf.
fn mixed_relay_under_a_rust_hub(token: Option<&str>) {
    let extra: Vec<&str> = token.map(|t| vec!["-token", t]).unwrap_or_default();
    let hub = Hub::start(opts("rs-hub", token).relay(true));
    let mut go = GoNode::client("go-leaf", &hub.url, &extra);
    let rs = Peer::new(opts("rs-leaf", token));
    rs.connect(&hub.url).expect("Rust leaf connects");
    let rs_from_go = rs_count(&rs.bus, FROM_GO, "go");
    let rs_from_rs = rs_count(&rs.bus, FROM_RS, "rs");
    let hub_from_go = rs_count(&hub.bus, FROM_GO, "go");

    go.publish(FROM_GO, "go", json!({"n": 1}));
    settle_exactly("Rust leaf gets the Go leaf's event via the hub", 1, || {
        rs_from_go.load(Ordering::SeqCst)
    });
    assert_eq!(
        hub_from_go.load(Ordering::SeqCst),
        1,
        "the hub sees it once"
    );
    assert_eq!(go.count(FROM_GO, "go"), 1, "no echo to the Go leaf");

    publish(&rs.bus, ev(FROM_RS, "rs", json!({"n": 2})));
    settle_exactly("Go leaf gets the Rust leaf's event via the hub", 1, || {
        go.count(FROM_RS, "rs")
    });
    assert_eq!(
        rs_from_rs.load(Ordering::SeqCst),
        1,
        "no echo to the Rust leaf"
    );
}

/// Go hub relaying between a Rust leaf and a Go leaf. With a token the
/// Go leaf's link runs streams and the Rust leaf's does not, on one hub.
fn mixed_relay_under_a_go_hub(token: Option<&str>) {
    let extra: Vec<&str> = token.map(|t| vec!["-token", t]).unwrap_or_default();
    let mut hub_args = vec!["-relay"];
    hub_args.extend_from_slice(&extra);
    let hub = GoNode::hub("go-hub", &hub_args);
    let mut go = GoNode::client("go-leaf", &hub.url, &extra);
    let rs = Peer::new(opts("rs-leaf", token));
    rs.connect(&hub.url)
        .expect("Rust leaf connects to the Go hub");
    let rs_from_go = rs_count(&rs.bus, FROM_GO, "go");
    let rs_from_rs = rs_count(&rs.bus, FROM_RS, "rs");

    publish(&rs.bus, ev(FROM_RS, "rs", json!({"n": 1})));
    settle_exactly(
        "Go leaf gets the Rust leaf's event via the Go hub",
        1,
        || go.count(FROM_RS, "rs"),
    );
    assert_eq!(hub.count(FROM_RS, "rs"), 1, "the Go hub sees it once");
    assert_eq!(
        rs_from_rs.load(Ordering::SeqCst),
        1,
        "no echo to the Rust leaf"
    );

    go.publish(FROM_GO, "go", json!({"n": 2}));
    settle_exactly(
        "Rust leaf gets the Go leaf's event via the Go hub",
        1,
        || rs_from_go.load(Ordering::SeqCst),
    );
    assert_eq!(go.count(FROM_GO, "go"), 1, "no echo to the Go leaf");
}

#[test]
fn mixed_relay_rust_hub() {
    if enabled() {
        mixed_relay_under_a_rust_hub(None);
    }
}

#[test]
fn mixed_relay_rust_hub_with_auth() {
    if enabled() {
        mixed_relay_under_a_rust_hub(Some("xlang-secret"));
    }
}

#[test]
fn mixed_relay_go_hub() {
    if enabled() {
        mixed_relay_under_a_go_hub(None);
    }
}

#[test]
fn mixed_relay_go_hub_with_auth() {
    if enabled() {
        mixed_relay_under_a_go_hub(Some("xlang-secret"));
    }
}
