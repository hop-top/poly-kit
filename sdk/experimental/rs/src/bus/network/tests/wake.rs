//! Wakes of a reconnect loop's backoff wait (see `Inner::request_redial`):
//! Go's wake tests. The first link flaps, so the loop enters a backoff
//! wait; the wait seam holds it until something wakes it.

use std::net::TcpListener;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{mpsc, Arc, PoisonError};
use std::thread;
use std::time::{Duration, Instant};

use futures_util::{SinkExt, StreamExt};
use tokio_tungstenite::tungstenite::Message;

use super::{close, within};
use crate::bus::context::Context;
use crate::bus::network::{NetworkAdapter, NetworkOptions};
use crate::bus::DynBus;

/// Upgrades every connection, closes the first at once and keeps the
/// rest open; reports each upgrade.
fn flap_first() -> (String, mpsc::Receiver<()>, Arc<AtomicUsize>) {
    let ln = TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("ws://{}/", ln.local_addr().unwrap());
    let (tx, rx) = mpsc::channel();
    let count = Arc::new(AtomicUsize::new(0));
    let n = Arc::clone(&count);
    thread::spawn(move || {
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        rt.block_on(async move {
            ln.set_nonblocking(true).unwrap();
            let ln = tokio::net::TcpListener::from_std(ln).unwrap();
            while let Ok((tcp, _)) = ln.accept().await {
                let (tx, n) = (tx.clone(), Arc::clone(&n));
                tokio::spawn(async move {
                    let Ok(mut ws) = tokio_tungstenite::accept_async(tcp).await else {
                        return;
                    };
                    let first = n.fetch_add(1, Ordering::SeqCst) == 0;
                    let _ = tx.send(());
                    if first {
                        let _ = ws.send(Message::Close(None)).await;
                    }
                    while let Some(Ok(_)) = ws.next().await {}
                });
            }
        });
    });
    (url, rx, count)
}

fn recv(rx: &mpsc::Receiver<()>, what: &str) {
    rx.recv_timeout(Duration::from_secs(5))
        .unwrap_or_else(|_| panic!("{what}"));
}

fn loop_idle(n: &NetworkAdapter, addr: &str) {
    let deadline = Instant::now() + Duration::from_secs(5);
    while n.0.state().reconnecting.contains(addr) {
        assert!(Instant::now() < deadline, "the reconnect loop never exited");
        thread::yield_now();
    }
}

/// Dials `url` and returns once the redial loop sits in a backoff wait
/// the seam holds.
fn held_in_backoff(url: &str, accepted: &mpsc::Receiver<()>) -> NetworkAdapter {
    let n = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().backoff(Duration::from_secs(3600), Duration::from_secs(3600)),
    );
    let (tx, waiting) = mpsc::channel();
    let tx = std::sync::Mutex::new(tx);
    *n.0.backoff_wait
        .lock()
        .unwrap_or_else(PoisonError::into_inner) = Some(Arc::new(move |_| {
        let _ = tx.lock().unwrap_or_else(PoisonError::into_inner).send(());
        false // hold until woken or closed
    }));
    n.dial(url);
    recv(accepted, "first link");
    recv(&waiting, "the redial never entered its backoff wait");
    n
}

/// backoff-wait --disconnect, dial--> woken, dials now.
#[test]
fn dial_during_backoff_wait_is_served_now() {
    let (url, accepted, _) = flap_first();
    let n = held_in_backoff(&url, &accepted);
    let (nn, u) = (n.clone(), url.clone());
    within(move || nn.disconnect(&u)).unwrap();
    n.dial(&url);
    recv(&accepted, "dial during the backoff wait not served now");
    close(&n);
}

/// backoff-wait --dial (address still registered)--> woken, dials now.
#[test]
fn dial_of_a_backing_off_address_is_served_now() {
    let (url, accepted, _) = flap_first();
    let n = held_in_backoff(&url, &accepted);
    n.dial(&url);
    recv(&accepted, "dial of a backing-off address not served now");
    close(&n);
}

/// backoff-wait --connect--> woken; the loop's own dial finds the link.
#[test]
fn connect_during_backoff_wait_wakes_the_loop() {
    let (url, accepted, accepts) = flap_first();
    let n = held_in_backoff(&url, &accepted);
    let (nn, u) = (n.clone(), url.clone());
    within(move || nn.connect(&Context::background(), &u)).expect("connect");
    recv(&accepted, "connect");
    loop_idle(&n, &url);
    assert!(
        accepts.load(Ordering::SeqCst) <= 3,
        "a woken loop must not add links"
    );
    close(&n);
}

/// backoff-wait --disconnect--> woken, the loop exits, nothing dials.
#[test]
fn disconnect_during_backoff_wait_exits() {
    let (url, accepted, accepts) = flap_first();
    let n = held_in_backoff(&url, &accepted);
    let (nn, u) = (n.clone(), url.clone());
    within(move || nn.disconnect(&u)).unwrap();
    loop_idle(&n, &url);
    assert_eq!(accepts.load(Ordering::SeqCst), 1, "links after disconnect");
    close(&n);
}

/// backoff-wait --close--> ended, nothing dials.
#[test]
fn close_during_backoff_wait_dials_nothing() {
    let (url, accepted, accepts) = flap_first();
    let n = held_in_backoff(&url, &accepted);
    close(&n);
    assert!(n.0.state().reconnecting.is_empty(), "a loop survived close");
    assert_eq!(accepts.load(Ordering::SeqCst), 1, "a dial after close");
}

/// backoff-wait --automatic redial (a drop)--> not woken: a drop honors
/// the full backoff.
#[test]
fn auto_redial_during_backoff_wait_is_not_woken() {
    let (url, accepted, accepts) = flap_first();
    let n = held_in_backoff(&url, &accepted);
    let before = n.0.wakes_sent.load(Ordering::SeqCst);
    n.0.request_redial(&url, true);
    assert_eq!(
        n.0.wakes_sent.load(Ordering::SeqCst),
        before,
        "an automatic redial woke the wait"
    );
    assert!(n.0.state().reconnecting.contains(&url), "still waiting");
    assert_eq!(accepts.load(Ordering::SeqCst), 1);
    close(&n);
}

/// Go's `TestNetworkAdapter_DialDuringA60sBackoffLinksWithin2s`: in real
/// time, with no seam, a dial after disconnect during a 60 s backoff
/// links within 2 s.
#[test]
fn dial_during_a_60s_backoff_links_within_2s() {
    let (url, accepted, _) = flap_first();
    let n = NetworkAdapter::new(
        DynBus::new(),
        NetworkOptions::new().backoff(Duration::from_secs(60), Duration::from_secs(60)),
    );
    n.dial(&url);
    recv(&accepted, "first link");
    // The first link dropped and its redial loop is in its wait.
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        {
            let st = n.0.state();
            let down = st
                .peers
                .get(&url)
                .is_some_and(|p| !p.state.connected && p.flaps > 0);
            if down && !st.conns.contains_key(&url) && st.reconnecting.contains(&url) {
                break;
            }
        }
        assert!(Instant::now() < deadline, "no redial loop after the drop");
        thread::yield_now();
    }
    let started = Instant::now();
    let (nn, u) = (n.clone(), url.clone());
    within(move || nn.disconnect(&u)).unwrap();
    n.dial(&url);
    accepted
        .recv_timeout(Duration::from_secs(2))
        .expect("not linked within 2 s of the dial");
    assert!(
        started.elapsed() < Duration::from_secs(2),
        "{:?}",
        started.elapsed()
    );
    close(&n);
}
