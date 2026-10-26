//! Moving events: the outbound forwarder (with relay) and the inbound
//! reader. Go's `forwardToRemotes` and `readLoop` on legacy links.

use std::sync::atomic::Ordering;
use std::sync::{Arc, PoisonError};

use futures_util::future::join_all;

use super::adapter::{Inner, NetworkOrigin};
use super::conn::{drain, is_capacity, read_next, too_big_reason, Link, Read, WsSource};
use super::conn::{STATUS_GOING_AWAY, STATUS_TOO_BIG};
use super::wire::{decode_event, encode_event};
use crate::bus::context::Context;
use crate::bus::event::Event;
use crate::bus::stream::is_reserved_topic;

impl Inner {
    /// The async `#` subscriber: forwards an accepted event to the peers.
    ///
    /// Local events go out stamped with this adapter's origin id. Events
    /// the adapter injected from the network go out only in relay mode,
    /// keeping the origin of the adapter that first published them, and
    /// never to the peer with that origin. The filter applies to both.
    pub(crate) async fn observe(&self, ctx: Context, e: Event) {
        if self.closed.load(Ordering::SeqCst) || is_reserved_topic(e.topic.as_str()) {
            return;
        }
        let origin = match ctx.value::<NetworkOrigin>() {
            Some(NetworkOrigin(o)) if self.relay => o.clone(),
            Some(_) => return,
            None => self.origin_id.clone(),
        };
        if !self.filter.matches(e.topic.as_str()) {
            return;
        }
        let frame = encode_event(&e, &origin);
        let links: Vec<Arc<Link>> = self
            .state()
            .conns
            .values()
            .filter(|l| {
                let peer = l.peer_origin();
                peer.is_empty() || peer != origin
            })
            .cloned()
            .collect();
        join_all(links.iter().map(|l| self.forward(l, frame.clone()))).await;
    }

    /// Writes one frame to `link`. The third failure in a row evicts the
    /// link; its reader then reconnects an outbound address.
    async fn forward(&self, link: &Arc<Link>, frame: String) {
        let fault = self
            .write_fault
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone();
        let ok = match fault {
            Some(f) if f(&frame) => false,
            _ => link.write(frame).await.is_ok(),
        };
        if link.record_write(ok) {
            {
                let mut st = self.state();
                if st
                    .conns
                    .get(&link.addr)
                    .is_some_and(|l| Arc::ptr_eq(l, link))
                {
                    st.conns.remove(&link.addr);
                }
            }
            link.close(STATUS_GOING_AWAY, "write failures");
        }
    }

    /// Reads `link` until it closes, injecting events into the bus, then
    /// unregisters it and reconnects an outbound address.
    pub(crate) async fn read_loop(self: Arc<Self>, link: Arc<Link>, mut source: WsSource) {
        loop {
            let read = tokio::select! {
                biased;
                () = link.closing.wait() => break,
                r = read_next(&mut source) => r,
            };
            match read {
                Read::Data(d) => self.on_frame(&link, &d).await,
                Read::Closed(_) => break,
                Read::Failed(e) => {
                    if is_capacity(&e) {
                        link.close(STATUS_TOO_BIG, &too_big_reason());
                    }
                    break;
                }
            }
        }
        link.send_close().await;
        // Keep reading so the close handshake completes either way.
        drain(&mut source).await;
        link.mark_down();
        drop(source);
        link.release().await;
        {
            let mut st = self.state();
            if st
                .conns
                .get(&link.addr)
                .is_some_and(|l| Arc::ptr_eq(l, &link))
            {
                st.conns.remove(&link.addr);
            }
            if link.outbound {
                if let Some(ps) = st.peers.get_mut(&link.addr) {
                    ps.connected = false;
                }
            }
        }
        link.done.fire();
        if link.outbound && !self.closed.load(Ordering::SeqCst) {
            self.go_reconnect(&link.addr);
        }
        self.tasks.done();
    }

    /// Injects one inbound frame: learns the peer's origin, drops frames
    /// this adapter originated and the adapter's reserved topics, and
    /// publishes the rest marked as network-originated. The publish runs
    /// on the blocking pool: it runs bus handlers, which may block.
    async fn on_frame(&self, link: &Link, data: &[u8]) {
        let Some((event, origin)) = decode_event(data) else {
            return;
        };
        if !origin.is_empty() {
            link.learn_origin(&origin);
        }
        if origin == self.origin_id || is_reserved_topic(event.topic.as_str()) {
            return;
        }
        let ctx = link.ctx.with_value(NetworkOrigin(origin));
        let bus = Arc::clone(&self.bus);
        let _ = tokio::task::spawn_blocking(move || {
            let _ = bus.publish(&ctx, &event);
        })
        .await;
    }
}
