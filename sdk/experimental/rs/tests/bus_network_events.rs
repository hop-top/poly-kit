//! The adapter's own events (`kit.runtime.link.*`) and the error
//! callback, on links without streams: Go's
//! `TestHandshake_NoHookDialerConnectsToOrderedHub`,
//! `TestHandshake_AuthRejectIsSurfacedOnceAndRetryable`,
//! `TestConnect_RacingDisconnectReturnsAnError` and
//! `TestConnect_ConcurrentDialsOfOneAddress` (the resume field read as a
//! legacy link reports it), plus the payloads Go publishes.

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, Mutex};
use std::time::Duration;

use bus_network_support::*;
use hop_top_kit::bus::network::{AuthError, Authenticator, NetworkError, NetworkOptions, OpenAuth};
use hop_top_kit::bus::{Context, DynBus};
use serde_json::{json, Value};

/// Records every `kit.runtime.*` event a bus delivers.
struct Collector(Mutex<mpsc::Receiver<(String, Value, String)>>);

impl Collector {
    fn on(bus: &DynBus) -> Collector {
        let (tx, rx) = mpsc::channel();
        let tx = Mutex::new(tx);
        bus.subscribe("kit.runtime.#", move |e| {
            let _ = tx.lock().unwrap().send((
                e.topic.as_str().to_string(),
                e.payload.clone(),
                e.source.clone(),
            ));
            Ok(())
        });
        Collector(Mutex::new(rx))
    }

    fn next_topic(&self, want: &str) -> Value {
        let (topic, payload, source) = self
            .0
            .lock()
            .unwrap()
            .recv_timeout(Duration::from_secs(5))
            .unwrap_or_else(|_| panic!("no event, want {want}"));
        assert_eq!(topic, want, "payload {payload}");
        assert_eq!(source, "kit.bus.network");
        payload
    }

    fn none(&self) {
        if let Ok(e) = self
            .0
            .lock()
            .unwrap()
            .recv_timeout(Duration::from_millis(200))
        {
            panic!("unexpected event {e:?}");
        }
    }
}

/// Go's `gateAuth`: accepts any token; armed, verify blocks until
/// released; reject fails verify.
#[derive(Default)]
struct GateAuth {
    reject: AtomicBool,
    armed: Mutex<Option<mpsc::Receiver<()>>>,
    entered: Mutex<Option<mpsc::Sender<()>>>,
}

impl GateAuth {
    /// Arms the gate; returns the release handle and the entered signal.
    fn arm(&self) -> (mpsc::Sender<()>, mpsc::Receiver<()>) {
        let (rel_tx, rel_rx) = mpsc::channel();
        let (ent_tx, ent_rx) = mpsc::channel();
        *self.armed.lock().unwrap() = Some(rel_rx);
        *self.entered.lock().unwrap() = Some(ent_tx);
        (rel_tx, ent_rx)
    }
}

/// The hub-side handle on a shared [`GateAuth`].
struct Gate(Arc<GateAuth>);

impl Authenticator for Gate {
    fn token(&self) -> Result<String, AuthError> {
        Ok("open".into())
    }
    fn verify(&self, _: &str) -> Result<(), AuthError> {
        if self.0.reject.load(Ordering::SeqCst) {
            return Err(Box::new(NetworkError::AuthFailed));
        }
        if let Some(e) = self.0.entered.lock().unwrap().as_ref() {
            let _ = e.send(());
        }
        let gate = self.0.armed.lock().unwrap().take();
        if let Some(g) = gate {
            let _ = g.recv_timeout(Duration::from_secs(10));
        }
        Ok(())
    }
}

#[test]
fn handshake_no_hook_dialer_connects_to_ordered_hub() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub").auth(OpenAuth));
    let cl = Peer::new(NetworkOptions::new().auth(OpenAuth));
    let events = Collector::on(&cl.bus);
    cl.connect(&hub.url).expect("connect");
    let st = cl.net.link_state(&hub.url).expect("state");
    assert!(
        st.connected && !st.streams,
        "want a connected legacy link: {st:?}"
    );
    assert_eq!(
        events.next_topic("kit.runtime.link.connected"),
        json!({"peer": hub.url, "stream": "", "resume": "unsupported", "format": "kit"})
    );
    let got = flag_on(&cl.bus, "acme.crm.deal.created");
    publish(&hub.bus, ev("acme.crm.deal.created", "crm", Value::Null));
    wait_for(&got, "a legacy link delivers");
}

#[test]
fn handshake_auth_reject_is_surfaced_once_and_retryable() {
    let auth = Arc::new(GateAuth::default());
    auth.reject.store(true, Ordering::SeqCst);
    let hub = Hub::start(
        NetworkOptions::new()
            .origin_id("hub")
            .auth(Gate(Arc::clone(&auth))),
    );
    let errs = Arc::new(Mutex::new(Vec::new()));
    let e = Arc::clone(&errs);
    let cl = Peer::new(
        NetworkOptions::new()
            .auth(OpenAuth)
            .error_func(move |err: &NetworkError| e.lock().unwrap().push(err.to_string())),
    );
    let events = Collector::on(&cl.bus);
    for _ in 0..2 {
        let err = cl.connect(&hub.url).expect_err("refused");
        assert!(err.is_auth_failed(), "{err}");
    }
    assert_eq!(
        events.next_topic("kit.runtime.link.disconnected"),
        json!({"peer": hub.url, "final": false, "code": "auth"})
    );
    events.none(); // the repeat is not published again
    assert_eq!(
        *errs.lock().unwrap(),
        vec![format!("bus: handshake with {}: bus: network auth failed", hub.url); 2]
    );
    auth.reject.store(false, Ordering::SeqCst);
    cl.connect(&hub.url).expect("retry connects");
    let st = cl.net.link_state(&hub.url).unwrap();
    assert!(st.connected && !st.is_final && st.code.is_empty(), "{st:?}");
}

#[test]
fn connect_racing_disconnect_returns_an_error() {
    let auth = Arc::new(GateAuth::default());
    let (release, entered) = auth.arm();
    let hub = Hub::start(
        NetworkOptions::new()
            .origin_id("hub")
            .auth(Gate(Arc::clone(&auth))),
    );
    let cl = Peer::new(NetworkOptions::new().auth(OpenAuth));
    let (n, u) = (cl.net.clone(), hub.url.clone());
    let done = std::thread::spawn(move || n.connect(&Context::background(), &u));
    entered
        .recv_timeout(Duration::from_secs(5))
        .expect("the dialer is inside the handshake");
    let (n, u) = (cl.net.clone(), hub.url.clone());
    within(move || n.disconnect(&u)).unwrap();
    release.send(()).unwrap();
    let err = within(move || done.join().unwrap()).expect_err("lost the race");
    assert!(matches!(err, NetworkError::Disconnected), "{err:?}");
    assert_eq!(err.to_string(), "bus: address disconnected during connect");
    assert!(
        cl.net.link_state(&hub.url).is_none(),
        "the address came back"
    );
}

#[test]
fn connect_concurrent_dials_of_one_address() {
    let auth = Arc::new(GateAuth::default());
    let hub = Hub::start(
        NetworkOptions::new()
            .origin_id("hub")
            .auth(Gate(Arc::clone(&auth))),
    );
    let cl = Peer::new(NetworkOptions::new().auth(OpenAuth));
    let events = Collector::on(&cl.bus);
    let (rel1, ent1) = auth.arm();
    let dials: Vec<_> = (0..2)
        .map(|_| {
            let (n, u) = (cl.net.clone(), hub.url.clone());
            std::thread::spawn(move || n.connect(&Context::background(), &u))
        })
        .collect();
    ent1.recv_timeout(Duration::from_secs(5))
        .expect("first dial gated");
    // The second verify runs ungated; release the first after it.
    std::thread::sleep(Duration::from_millis(100));
    rel1.send(()).unwrap();
    for d in dials {
        within(move || d.join().unwrap()).expect("both dials return Ok");
    }
    let st = cl.net.link_state(&hub.url).unwrap();
    assert!(st.connected && !st.is_final, "{st:?}");
    assert_eq!(
        events.next_topic("kit.runtime.link.connected")["resume"],
        json!("unsupported")
    );
    events.none(); // the loser publishes nothing: no spurious final disconnect
}

#[test]
fn disconnect_publishes_a_final_disconnected_and_close_publishes_nothing() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub"));
    let other = Hub::start(NetworkOptions::new().origin_id("other"));
    let cl = Peer::new(NetworkOptions::new());
    let events = Collector::on(&cl.bus);
    cl.connect(&hub.url).unwrap();
    cl.connect(&other.url).unwrap();
    events.next_topic("kit.runtime.link.connected");
    events.next_topic("kit.runtime.link.connected");
    let (n, u) = (cl.net.clone(), hub.url.clone());
    within(move || n.disconnect(&u)).unwrap();
    assert_eq!(
        events.next_topic("kit.runtime.link.disconnected"),
        json!({"peer": hub.url, "final": true})
    );
    let n = cl.net.clone();
    within(move || n.close()).unwrap();
    events.none();
}

/// Link events are the adapter's own: published on the local bus only,
/// never forwarded, and inbound frames carrying them are dropped.
#[test]
fn link_events_never_leave_the_process() {
    let hub = Hub::start(NetworkOptions::new().origin_id("hub").relay(true));
    let hub_events = Arc::new(AtomicUsize::new(0));
    let h = Arc::clone(&hub_events);
    hub.bus.subscribe("kit.runtime.#", move |_| {
        h.fetch_add(1, Ordering::SeqCst);
        Ok(())
    });
    let cl = Peer::new(NetworkOptions::new());
    cl.connect(&hub.url).unwrap();
    sleep_ms(300);
    assert_eq!(
        hub_events.load(Ordering::SeqCst),
        0,
        "an acceptor publishes none, receives none"
    );
}

/// Go's `TestNetworkAdapter_HandshakeErrorRedactsCredentials`: the error
/// callback never sees the dial address's userinfo, query or fragment.
#[test]
fn handshake_error_redacts_credentials() {
    let auth = Arc::new(GateAuth::default());
    auth.reject.store(true, Ordering::SeqCst);
    let hub = Hub::start(
        NetworkOptions::new()
            .origin_id("hub")
            .auth(Gate(Arc::clone(&auth))),
    );
    let errs = Arc::new(Mutex::new(Vec::new()));
    let e = Arc::clone(&errs);
    let cl = Peer::new(
        NetworkOptions::new()
            .auth(OpenAuth)
            .error_func(move |err: &NetworkError| e.lock().unwrap().push(err.to_string())),
    );
    let host = hub
        .url
        .trim_start_matches("ws://")
        .trim_end_matches('/')
        .to_string();
    let err = cl
        .connect(&format!("ws://user:hunter2@{host}/bus?token=abc#frag"))
        .expect_err("refused");
    assert!(err.is_auth_failed(), "{err}");
    assert_eq!(
        *errs.lock().unwrap(),
        vec![format!(
            "bus: handshake with ws://{host}/bus: bus: network auth failed"
        )]
    );
}
