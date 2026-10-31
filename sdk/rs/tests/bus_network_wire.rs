//! Replays `contracts/bus-v1/streams/wire/*.jsonl` against a Rust
//! acceptor over a real WebSocket, as Go's `TestStreamConformanceWire`
//! does against Go's: the acceptor runs with origin `hub` and `OpenAuth`,
//! each `send` goes out, each `recv` must be the next frame (as a JSON
//! value, and byte for byte when `bytes` is given), each `publish` is
//! published on the acceptor's bus. Strict: unknown fields and files
//! without a runner fail.
//!
//! This adapter speaks links without streams, so it replays the
//! transcript of that link; the stream transcripts are listed with the
//! reason they wait.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::fs;
use std::path::{Path, PathBuf};
use std::time::Duration;

use bus_network_support::*;
use futures_util::{SinkExt, StreamExt};
use hop_top_kit::bus::network::{NetworkOptions, OpenAuth};
use hop_top_kit::bus::Event;
use serde::Deserialize;
use serde_json::Value;
use tokio_tungstenite::tungstenite::Message;

enum Runner {
    Replay,
    Waits(&'static str),
}

fn runner(name: &str) -> Option<Runner> {
    const STREAMS: &str =
        "needs a stream acceptor (resume, subscriptions); bus streams are not ported";
    match name {
        "plain_ack.jsonl" => Some(Runner::Replay),
        "replay.jsonl" | "reset_ack.jsonl" | "subscribe.jsonl" => Some(Runner::Waits(STREAMS)),
        "acks.jsonl" => Some(Runner::Waits(
            "needs a stream acceptor that answers pubacks; this port has the dialer side only",
        )),
        _ => None,
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Header {
    version: String,
    description: String,
    setup: Setup,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Setup {
    #[allow(dead_code)]
    log_size: u64,
    publish: Vec<Value>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Step {
    send: Option<Value>,
    recv: Option<Value>,
    bytes: Option<String>,
    publish: Option<Value>,
}

fn wire_dir() -> PathBuf {
    Path::new(env!("CARGO_MANIFEST_DIR")).join("../../contracts/bus-v1/streams/wire")
}

fn event(v: &Value) -> Event {
    serde_json::from_value(v.clone()).expect("transcript event")
}

#[test]
fn every_transcript_has_a_runner() {
    let mut seen = 0;
    for entry in fs::read_dir(wire_dir()).expect("wire dir") {
        let name = entry.unwrap().file_name().to_string_lossy().into_owned();
        if !name.ends_with(".jsonl") {
            continue;
        }
        seen += 1;
        match runner(&name) {
            None => panic!("{name} has no runner"),
            Some(Runner::Waits(why)) => eprintln!("{name}: waits: {why}"),
            Some(Runner::Replay) => {}
        }
    }
    assert!(seen >= 4, "transcripts found: {seen}");
}

#[test]
fn plain_ack_transcript() {
    replay("plain_ack.jsonl");
}

fn replay(name: &str) {
    let text = fs::read_to_string(wire_dir().join(name)).expect("transcript");
    let mut lines = text.lines();
    let head: Header = serde_json::from_str(lines.next().expect("header")).expect("strict header");
    assert_eq!(head.version, "v1");
    assert!(!head.description.is_empty());

    let hub = Hub::start(NetworkOptions::new().origin_id("hub").auth(OpenAuth));
    for e in &head.setup.publish {
        publish(&hub.bus, event(e));
    }
    let steps: Vec<Step> = lines
        .map(|l| serde_json::from_str(l).expect("strict step"))
        .collect();
    let url = hub.url.clone();
    let bus = hub.bus.clone();
    within(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            let (mut ws, _) = tokio_tungstenite::connect_async(url).await.unwrap();
            for (i, st) in steps.iter().enumerate() {
                let line = i + 2;
                if let Some(f) = &st.send {
                    ws.send(Message::text(f.to_string())).await.unwrap();
                } else if let Some(want) = &st.recv {
                    let got = next_text(&mut ws)
                        .await
                        .unwrap_or_else(|| panic!("line {line}: no frame"));
                    if let Some(bytes) = &st.bytes {
                        assert_eq!(&got, bytes, "line {line}: bytes");
                    }
                    let mut have: Value = serde_json::from_str(&got).unwrap();
                    if want.get("message").is_none() {
                        if let Some(m) = have.as_object_mut() {
                            m.remove("message");
                        }
                    }
                    assert_eq!(&have, want, "line {line}");
                } else if let Some(e) = &st.publish {
                    let (b, e) = (bus.clone(), event(e));
                    tokio::task::spawn_blocking(move || b.publish(&e))
                        .await
                        .unwrap()
                        .unwrap();
                } else {
                    panic!("line {line}: empty step");
                }
            }
            let extra = tokio::time::timeout(Duration::from_millis(500), next_text(&mut ws)).await;
            assert!(extra.is_err(), "unexpected trailing frame {extra:?}");
        });
    });
}

async fn next_text<S>(ws: &mut S) -> Option<String>
where
    S: futures_util::Stream<Item = Result<Message, tokio_tungstenite::tungstenite::Error>> + Unpin,
{
    let read = async {
        while let Some(Ok(m)) = ws.next().await {
            if let Message::Text(t) = m {
                return Some(t.to_string());
            }
        }
        None
    };
    tokio::time::timeout(Duration::from_secs(5), read)
        .await
        .ok()
        .flatten()
}

/// The same transcript from the dialer's side: a scripted acceptor plays
/// the `recv` frames to a Rust dialer (with `OpenAuth`), checks the
/// dialer's `send` frames, and the dialer's bus gets each event.
#[test]
fn plain_ack_transcript_dialer_side() {
    use hop_top_kit::bus::network::NetworkOptions;
    use std::sync::{Arc, Mutex};

    let text = fs::read_to_string(wire_dir().join("plain_ack.jsonl")).expect("transcript");
    let mut lines = text.lines();
    let _: Header = serde_json::from_str(lines.next().unwrap()).unwrap();
    let steps: Vec<Step> = lines.map(|l| serde_json::from_str(l).unwrap()).collect();
    let want_events: Vec<Value> = steps
        .iter()
        .filter_map(|s| s.recv.clone())
        .filter(|r| r.get("topic").is_some())
        .collect();

    let ln = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let addr = ln.local_addr().unwrap();
    let script = std::thread::spawn(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            ln.set_nonblocking(true).unwrap();
            let ln = tokio::net::TcpListener::from_std(ln).unwrap();
            let (tcp, _) = tokio::time::timeout(Duration::from_secs(10), ln.accept())
                .await
                .unwrap()
                .unwrap();
            let mut ws = tokio_tungstenite::accept_async(tcp).await.unwrap();
            for st in steps {
                if let Some(want) = st.send {
                    let got = next_text(&mut ws).await.expect("dialer frame");
                    assert_eq!(serde_json::from_str::<Value>(&got).unwrap(), want);
                } else if let Some(frame) = st.recv {
                    let out = st.bytes.unwrap_or_else(|| frame.to_string());
                    ws.send(Message::text(out)).await.unwrap();
                }
            }
            sleep_ms_async(300).await;
        });
    });

    let b = Peer::new(NetworkOptions::new().origin_id("dialer").auth(OpenAuth));
    let got = Arc::new(Mutex::new(Vec::new()));
    let g = Arc::clone(&got);
    b.bus.subscribe("#", move |e| {
        // The adapter's own link events are local, not from the wire.
        if !e.topic.as_str().starts_with("kit.runtime.") {
            g.lock().unwrap().push(e.clone());
        }
        Ok(())
    });
    b.connect(&format!("ws://{addr}/"))
        .expect("dialer handshake per transcript");
    assert!(eventually(DELIVERY, || got.lock().unwrap().len() == want_events.len()));
    for (e, want) in got.lock().unwrap().iter().zip(&want_events) {
        let mut want = want.clone();
        want.as_object_mut().unwrap().remove("origin");
        assert_eq!(serde_json::to_value(e).unwrap(), want);
    }
    script.join().expect("script");
}

async fn sleep_ms_async(ms: u64) {
    tokio::time::sleep(Duration::from_millis(ms)).await;
}
