//! Redials after a drop, driven through the reconnect seams so nothing
//! depends on timing: the backoff wait records its attempt instead of
//! sleeping, and a loop can be held at its exit.

use std::net::TcpListener;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, PoisonError};
use std::thread;
use std::time::Duration;

use futures_util::{SinkExt, StreamExt};
use tokio::sync::{Notify, Semaphore};
use tokio_tungstenite::tungstenite::Message;

use super::{close, within};
use crate::bus::network::reconnect::ExitGate;
use crate::bus::network::{NetworkAdapter, NetworkOptions};
use crate::bus::DynBus;

/// An acceptor on loopback. Each upgraded connection is reported on the
/// returned channel; with `close_now` it is closed at once (1000), else
/// the first one waits for `drop_first` and later ones read until the
/// peer leaves.
fn acceptor(close_now: bool) -> (String, mpsc::Receiver<()>, Arc<Notify>, Arc<AtomicUsize>) {
    let ln = TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("ws://{}/", ln.local_addr().unwrap());
    let (tx, rx) = mpsc::channel();
    let drop_first = Arc::new(Notify::new());
    let count = Arc::new(AtomicUsize::new(0));
    let (d, n) = (Arc::clone(&drop_first), Arc::clone(&count));
    thread::spawn(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            ln.set_nonblocking(true).unwrap();
            let ln = tokio::net::TcpListener::from_std(ln).unwrap();
            while let Ok((tcp, _)) = ln.accept().await {
                let (tx, d, n) = (tx.clone(), Arc::clone(&d), Arc::clone(&n));
                tokio::spawn(async move {
                    let Ok(mut ws) = tokio_tungstenite::accept_async(tcp).await else {
                        return;
                    };
                    let idx = n.fetch_add(1, Ordering::SeqCst);
                    let _ = tx.send(());
                    if close_now || idx == 0 {
                        if !close_now {
                            d.notified().await;
                        }
                        let _ = ws.send(Message::Close(None)).await;
                    }
                    while let Some(Ok(_)) = ws.next().await {}
                });
            }
        });
    });
    (url, rx, drop_first, count)
}

/// Go's `TestNetworkAdapter_ReconnectBacksOffAgainstAFlappingAcceptor`:
/// every redial after a drop waits the backoff first, and the attempt
/// keeps growing across short-lived links (the 30 s default window is
/// never reached). Deterministic on the wait seam.
#[test]
fn reconnect_backs_off_against_a_flapping_acceptor() {
    let (url, _accepted, _, links) = acceptor(true);
    const WAITS: usize = 20;
    let n = NetworkAdapter::new(DynBus::new(), NetworkOptions::new());
    let (tx, waits) = mpsc::channel();
    let tx = std::sync::Mutex::new(tx);
    let total = Arc::new(AtomicUsize::new(0));
    let t = Arc::clone(&total);
    let seam = move |attempt: u32| {
        t.fetch_add(1, Ordering::SeqCst);
        let _ = tx
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .send(attempt);
    };
    *n.0.backoff_wait
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Some(Arc::new(seam));
    n.dial(&url);
    let got: Vec<u32> = (0..WAITS)
        .map(|i| {
            waits
                .recv_timeout(Duration::from_secs(5))
                .unwrap_or_else(|_| panic!("redials stopped after {i} waits"))
        })
        .collect();
    close(&n);
    assert_eq!(got[0], 1, "the first redial waits at attempt 1: {got:?}");
    // No wait restarts from scratch while the links stay short (a failed
    // dial inside a loop may raise one wait above the next, so the order
    // is not asserted).
    assert!(got.iter().all(|&a| a >= 1), "a wait restarted: {got:?}");
    // Every link but the first follows a wait: no redial without one.
    let (up, w) = (links.load(Ordering::SeqCst), total.load(Ordering::SeqCst));
    assert!(
        up <= w + 1,
        "{up} links after {w} waits: a redial skipped its wait"
    );
    assert!(
        got[WAITS - 1] as usize >= WAITS / 2,
        "after {WAITS} waits the attempt is {}; want it to track the short links",
        got[WAITS - 1]
    );
}

/// Go's `TestNetworkAdapter_RedialPendingDuringLoopExit`: a link that
/// drops before the loop that dialed it has finished is still redialed.
/// The loop is held at its exit while the link drops.
#[test]
fn redial_pending_during_loop_exit() {
    let (url, accepted, drop_first, _) = acceptor(false);
    let bus = DynBus::new();
    let n = NetworkAdapter::new(
        bus.clone(),
        NetworkOptions::new().backoff(Duration::from_millis(1), Duration::from_millis(4)),
    );
    let (down_tx, down) = mpsc::channel();
    let down_tx = std::sync::Mutex::new(down_tx);
    bus.subscribe("kit.runtime.link.disconnected", move |_| {
        let _ = down_tx
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .send(());
        Ok(())
    });
    let (entered_tx, entered) = mpsc::channel();
    let release = Arc::new(Semaphore::new(0));
    *n.0.exit_gate.lock().unwrap_or_else(PoisonError::into_inner) = Some(ExitGate {
        entered: entered_tx,
        release: Arc::clone(&release),
    });
    n.dial(&url);
    accepted
        .recv_timeout(Duration::from_secs(5))
        .expect("first link");
    entered
        .recv_timeout(Duration::from_secs(5))
        .expect("the loop that dialed reaches its exit");
    let link = loop {
        // The loop registers the link right after the upgrade.
        if let Some(l) = n.0.state().conns.get(&url).cloned() {
            break l;
        }
        std::thread::yield_now();
    };
    drop_first.notify_one();
    down.recv_timeout(Duration::from_secs(5))
        .expect("the drop is seen while the loop is held");
    // The reader fires `done` after it asked for the redial: the request
    // met the held loop.
    within(move || link.done.wait_blocking());
    release.add_permits(1);
    accepted
        .recv_timeout(Duration::from_secs(5))
        .expect("the drop is redialed");
    let nn = n.clone();
    within(move || nn.close()).unwrap();
}

/// Close during a backoff wait returns at once, not after the wait. The
/// test closes only once the dropped link's reader has asked for the
/// redial, so the loop is in its wait.
#[test]
fn close_during_backoff_is_prompt() {
    let (url, accepted, drop_first, _) = acceptor(false);
    let n = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().backoff(Duration::from_secs(20), Duration::from_secs(30)),
    );
    n.dial(&url);
    accepted
        .recv_timeout(Duration::from_secs(5))
        .expect("first link");
    let link = loop {
        // The loop registers the link right after the upgrade.
        if let Some(l) = n.0.state().conns.get(&url).cloned() {
            break l;
        }
        std::thread::yield_now();
    };
    drop_first.notify_one();
    within(move || link.done.wait_blocking());
    assert!(
        n.0.state().reconnecting.contains(&url),
        "the redial is waiting"
    );
    let started = std::time::Instant::now();
    close(&n);
    assert!(
        started.elapsed() < Duration::from_millis(500),
        "close took {:?} during a backoff wait",
        started.elapsed()
    );
}

/// Go's `TestNetworkAdapter_RedialAfterDropWaitsFirst`: a link that
/// drops after the loop that dialed it has exited starts a new loop,
/// which waits the backoff before it dials. The link drops only once no
/// loop runs.
#[test]
fn redial_after_drop_waits_first() {
    let (url, accepted, drop_first, _) = acceptor(false);
    let n = NetworkAdapter::new(DynBus::new(), NetworkOptions::new());
    let waits = Arc::new(AtomicUsize::new(0));
    let w = Arc::clone(&waits);
    *n.0.backoff_wait
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Some(Arc::new(move |_| {
        w.fetch_add(1, Ordering::SeqCst);
    }));
    n.dial(&url);
    accepted
        .recv_timeout(Duration::from_secs(5))
        .expect("first link");
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    loop {
        {
            let st = n.0.state();
            if !st.reconnecting.contains(&url) && st.conns.contains_key(&url) {
                break;
            }
        }
        assert!(
            std::time::Instant::now() < deadline,
            "the dialing loop never exited"
        );
        std::thread::yield_now();
    }
    drop_first.notify_one();
    accepted
        .recv_timeout(Duration::from_secs(5))
        .expect("the drop is redialed");
    assert_eq!(
        waits.load(Ordering::SeqCst),
        1,
        "the redial waited the backoff before it dialed"
    );
    close(&n);
}
