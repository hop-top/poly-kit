//! The redial state machine (see `Inner::request_redial`), one test per
//! transition the other reconnect tests do not already pin: Go's
//! `TestRedialFSM_*`. Deterministic: the tests hold the acceptor or the
//! loop at the step that matters.

use std::net::TcpListener;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, PoisonError};
use std::thread;
use std::time::{Duration, Instant};

use futures_util::StreamExt;
use tokio::sync::{watch, Semaphore};

use super::{close, within};
use crate::bus::network::reconnect::ExitGate;
use crate::bus::network::{NetworkAdapter, NetworkOptions};
use crate::bus::DynBus;

/// An acceptor that upgrades a connection only once the gate opens,
/// reports each request on `entered` and each upgrade on `accepted`, and
/// keeps every link open until the peer leaves.
struct Gated {
    url: String,
    gate: watch::Sender<bool>,
    entered: mpsc::Receiver<()>,
    accepted: mpsc::Receiver<()>,
    accepts: Arc<AtomicUsize>,
}

fn gated_acceptor() -> Gated {
    let ln = TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("ws://{}/", ln.local_addr().unwrap());
    let (gate, gate_rx) = watch::channel(false);
    let (ent_tx, entered) = mpsc::channel();
    let (acc_tx, accepted) = mpsc::channel();
    let accepts = Arc::new(AtomicUsize::new(0));
    let n = Arc::clone(&accepts);
    thread::spawn(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            ln.set_nonblocking(true).unwrap();
            let ln = tokio::net::TcpListener::from_std(ln).unwrap();
            while let Ok((tcp, _)) = ln.accept().await {
                let (ent, acc, n) = (ent_tx.clone(), acc_tx.clone(), Arc::clone(&n));
                let mut gate = gate_rx.clone();
                tokio::spawn(async move {
                    let _ = ent.send(());
                    let _ = gate.wait_for(|open| *open).await;
                    let Ok(mut ws) = tokio_tungstenite::accept_async(tcp).await else {
                        return;
                    };
                    n.fetch_add(1, Ordering::SeqCst);
                    let _ = acc.send(());
                    while let Some(Ok(_)) = ws.next().await {}
                });
            }
        });
    });
    Gated {
        url,
        gate,
        entered,
        accepted,
        accepts,
    }
}

fn recv(rx: &mpsc::Receiver<()>, what: &str) {
    rx.recv_timeout(Duration::from_secs(5))
        .unwrap_or_else(|_| panic!("{what}"));
}

/// Waits until no reconnect loop runs for `addr`.
fn loop_idle(n: &NetworkAdapter, addr: &str) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while n.0.state().reconnecting.contains(addr) {
        assert!(Instant::now() < deadline, "the reconnect loop never exited");
        thread::yield_now();
    }
}

/// Arms the exit gate: the next loop to finish a run holds there.
fn hold_exit(n: &NetworkAdapter) -> (mpsc::Receiver<()>, Arc<Semaphore>) {
    let (entered, at_exit) = mpsc::channel();
    let release = Arc::new(Semaphore::new(0));
    *n.0.exit_gate.lock().unwrap_or_else(PoisonError::into_inner) = Some(ExitGate {
        entered,
        release: Arc::clone(&release),
    });
    (at_exit, release)
}

/// Records whether a backoff wait happened while the adapter was open.
fn watch_waits(n: &NetworkAdapter) -> Arc<AtomicBool> {
    let waited = Arc::new(AtomicBool::new(false));
    let (w, inner) = (Arc::clone(&waited), Arc::downgrade(&n.0));
    *n.0.backoff_wait
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Some(Arc::new(move |_| {
        // A dial that close refused may wait after it.
        if inner
            .upgrade()
            .is_some_and(|i| !i.closed.load(Ordering::SeqCst))
        {
            w.store(true, Ordering::SeqCst);
        }
        true
    }));
    waited
}

fn slow() -> NetworkOptions {
    NetworkOptions::new().backoff(Duration::from_secs(3600), Duration::from_secs(3600))
}

fn fast() -> NetworkOptions {
    NetworkOptions::new().backoff(Duration::from_millis(1), Duration::from_millis(4))
}

/// idle --dial--> running (dial now): the first dial does not wait.
#[test]
fn redial_fsm_dial_from_idle_dials_now() {
    let a = gated_acceptor();
    a.gate.send_replace(true);
    let n = NetworkAdapter::new(DynBus::new(), slow());
    let waited = watch_waits(&n);
    n.dial(&a.url);
    recv(&a.entered, "no dial");
    recv(&a.accepted, "no link");
    loop_idle(&n, &a.url);
    assert!(!waited.load(Ordering::SeqCst), "a dial from idle waited");
    close(&n);
}

/// running (dialing) --disconnect, dial--> the new peer is served.
#[test]
fn redial_fsm_dial_after_disconnect_during_dial_is_served() {
    let a = gated_acceptor();
    let n = NetworkAdapter::new(DynBus::new(), fast());
    n.dial(&a.url);
    recv(&a.entered, "the loop never dialed");
    let (nn, u) = (n.clone(), a.url.clone());
    within(move || nn.disconnect(&u)).unwrap();
    n.dial(&a.url);
    a.gate.send_replace(true);
    recv(&a.accepted, "first upgrade");
    loop_idle(&n, &a.url);
    let up = n.0.state().conns.contains_key(&a.url);
    assert!(up, "dial after disconnect lost: no live link");
    assert!(n.link_state(&a.url).is_some_and(|s| s.connected));
    close(&n);
}

/// running (at exit) --disconnect, dial--> the loop runs again, now, for
/// the new peer.
#[test]
fn redial_fsm_dial_during_loop_exit_is_served() {
    let a = gated_acceptor();
    a.gate.send_replace(true);
    let n = NetworkAdapter::new(DynBus::new(), slow());
    let (at_exit, release) = hold_exit(&n);
    let waited = watch_waits(&n);
    n.dial(&a.url);
    recv(&a.accepted, "first link");
    recv(&at_exit, "the loop never reached its exit");
    let (nn, u) = (n.clone(), a.url.clone());
    within(move || nn.disconnect(&u)).unwrap();
    n.dial(&a.url);
    release.add_permits(1);
    recv(&a.accepted, "dial after disconnect lost");
    loop_idle(&n, &a.url);
    assert!(
        !waited.load(Ordering::SeqCst),
        "a dial request waited the backoff"
    );
    close(&n);
}

/// running (at exit, mark set) --disconnect--> the mark is cleared and
/// the loop exits without dialing.
#[test]
fn redial_fsm_disconnect_clears_mark_and_stops_loop() {
    let a = gated_acceptor();
    a.gate.send_replace(true);
    let n = NetworkAdapter::new(DynBus::new(), fast());
    let (at_exit, release) = hold_exit(&n);
    n.dial(&a.url);
    recv(&a.accepted, "first link");
    recv(&at_exit, "the loop never reached its exit");
    n.0.request_redial(&a.url, true); // a mark, as a drop would leave
    let (nn, u) = (n.clone(), a.url.clone());
    within(move || nn.disconnect(&u)).unwrap();
    assert!(
        !n.0.state().pending_redial.contains_key(&a.url),
        "disconnect left the mark"
    );
    release.add_permits(1);
    loop_idle(&n, &a.url);
    assert_eq!(
        a.accepts.load(Ordering::SeqCst),
        1,
        "the loop dialed a forgotten address"
    );
    close(&n);
}

/// running (at exit, mark set) --close--> marks are cleared, the loop
/// exits, and nothing dials after close.
#[test]
fn redial_fsm_close_clears_marks_and_nothing_dials() {
    let a = gated_acceptor();
    a.gate.send_replace(true);
    let n = NetworkAdapter::new(DynBus::new(), fast());
    let (at_exit, release) = hold_exit(&n);
    n.dial(&a.url);
    recv(&a.accepted, "first link");
    recv(&at_exit, "the loop never reached its exit");
    n.0.request_redial(&a.url, false);
    let nn = n.clone();
    let closing = thread::spawn(move || nn.close());
    let deadline = Instant::now() + Duration::from_secs(5);
    while !n.0.closed.load(Ordering::SeqCst) {
        assert!(Instant::now() < deadline, "close never started");
        thread::yield_now();
    }
    release.add_permits(1);
    within(move || closing.join().unwrap()).unwrap();
    let st = n.0.state();
    assert!(st.pending_redial.is_empty(), "close left marks");
    assert!(st.reconnecting.is_empty(), "a loop survived close");
    assert_eq!(a.accepts.load(Ordering::SeqCst), 1, "a dial after close");
}
