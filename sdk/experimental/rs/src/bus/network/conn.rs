//! One WebSocket connection: Go's `link` on a legacy link.
//!
//! The socket is split once. The write half sits behind an async mutex,
//! so concurrent forwards serialize as Go's `conn.Write` does; the read
//! half belongs to the link's reader task. Closing records the close
//! frame and wakes the reader, which sends it, drains until the peer
//! answers (bounded), then drops the socket: a close completes its
//! handshake like coder/websocket's.

use std::pin::Pin;
use std::sync::atomic::{AtomicBool, AtomicU32, AtomicU64, Ordering};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

use futures_util::{Sink, SinkExt, Stream, StreamExt};
use tokio::io::{AsyncRead, AsyncWrite};
use tokio_tungstenite::tungstenite::protocol::frame::coding::CloseCode;
use tokio_tungstenite::tungstenite::protocol::{CloseFrame, WebSocketConfig};
use tokio_tungstenite::tungstenite::{Error as WsError, Message};
use tokio_tungstenite::WebSocketStream;

use super::sync::Signal;
use crate::bus::context::{CancelHandle, Context};

pub(crate) const STATUS_NORMAL: u16 = 1000;
pub(crate) const STATUS_GOING_AWAY: u16 = 1001;
pub(crate) const STATUS_POLICY: u16 = 1008;
pub(crate) const STATUS_TOO_BIG: u16 = 1009;
pub(crate) const STATUS_INTERNAL: u16 = 1011;

/// Go's per-write deadline.
pub(crate) const WRITE_TIMEOUT: Duration = Duration::from_secs(5);
/// Consecutive write failures before a link is evicted.
pub(crate) const MAX_WRITE_FAILURES: u32 = 3;
/// How long a closing side waits for the peer's close frame:
/// coder/websocket's close handshake bound.
const CLOSE_HANDSHAKE: Duration = Duration::from_secs(5);
/// coder/websocket's default read limit, in bytes per message.
const READ_LIMIT: usize = 32768;

pub(crate) type WsSink = Pin<Box<dyn Sink<Message, Error = WsError> + Send>>;
pub(crate) type WsSource = Pin<Box<dyn Stream<Item = Result<Message, WsError>> + Send>>;

/// Socket settings shared by both sides: coder/websocket's read limit.
pub(crate) fn ws_config() -> WebSocketConfig {
    WebSocketConfig::default()
        .max_message_size(Some(READ_LIMIT))
        .max_frame_size(Some(READ_LIMIT))
}

/// Splits `ws` into its boxed halves.
pub(crate) fn split<S>(ws: WebSocketStream<S>) -> (WsSink, WsSource)
where
    S: AsyncRead + AsyncWrite + Unpin + Send + 'static,
{
    let (sink, source) = ws.split();
    (Box::pin(sink), Box::pin(source))
}

fn close_message(code: u16, reason: &str) -> Message {
    Message::Close(Some(CloseFrame {
        code: CloseCode::from(code),
        reason: reason.to_string().into(),
    }))
}

/// Closes a socket that never became a link: sends the close frame, then
/// reads until the peer answers or the handshake bound passes.
pub(crate) async fn close_raw(mut sink: WsSink, mut source: WsSource, code: u16, reason: &str) {
    let _ = tokio::time::timeout(WRITE_TIMEOUT, sink.send(close_message(code, reason))).await;
    drain(&mut source).await;
}

/// Reads and drops frames until the stream ends or the close handshake
/// bound passes; polling is what lets the socket answer a close.
pub(crate) async fn drain(source: &mut WsSource) {
    let _ = tokio::time::timeout(CLOSE_HANDSHAKE, async {
        while let Some(Ok(_)) = source.next().await {}
    })
    .await;
}

/// One read from a socket, past control frames.
pub(crate) enum Read {
    /// A text or binary message.
    Data(Vec<u8>),
    /// The peer closed, with its status code when it sent one.
    Closed(Option<u16>),
    /// The socket failed.
    Failed(WsError),
}

/// The next message from `source`, skipping pings and pongs.
pub(crate) async fn read_next(source: &mut WsSource) -> Read {
    loop {
        match source.next().await {
            Some(Ok(Message::Text(t))) => return Read::Data(t.as_bytes().to_vec()),
            Some(Ok(Message::Binary(b))) => return Read::Data(b.to_vec()),
            Some(Ok(Message::Close(f))) => return Read::Closed(f.map(|f| u16::from(f.code))),
            Some(Ok(_)) => continue,
            Some(Err(e)) => return Read::Failed(e),
            None => return Read::Closed(None),
        }
    }
}

/// Whether a read failed on coder/websocket's read limit.
pub(crate) fn is_capacity(e: &WsError) -> bool {
    matches!(e, WsError::Capacity(_))
}

/// The reason coder/websocket closes with when a message passes the read
/// limit: it reads one byte past the limit.
pub(crate) fn too_big_reason() -> String {
    format!("read limited at {} bytes", READ_LIMIT + 1)
}

/// One live connection to a peer.
pub(crate) struct Link {
    /// Unique per link: a relay never sends an event back on the link
    /// it arrived on.
    pub(crate) id: u64,
    pub(crate) addr: String,
    pub(crate) outbound: bool,
    sink: tokio::sync::Mutex<WsSink>,
    /// Fired once the link is closing: Go's `l.ctx` canceled.
    pub(crate) closing: Signal,
    /// The context inbound events are published with.
    pub(crate) ctx: Context,
    ctx_cancel: CancelHandle,
    /// The peer's origin id: from its auth frame, else learned from the
    /// first event frame that carries one.
    origin: Mutex<String>,
    write_fail: AtomicU32,
    closed: AtomicBool,
    /// The close frame the reader sends on its way out.
    close_frame: Mutex<Option<(u16, String)>>,
    /// Fired when the reader exits: Go's `l.done`.
    pub(crate) done: Signal,
}

impl Link {
    pub(crate) fn new(addr: String, outbound: bool, sink: WsSink, origin: String) -> Arc<Link> {
        let (ctx, ctx_cancel) = Context::background().with_cancel();
        static NEXT: AtomicU64 = AtomicU64::new(1);
        Arc::new(Link {
            id: NEXT.fetch_add(1, Ordering::Relaxed),
            addr,
            outbound,
            sink: tokio::sync::Mutex::new(sink),
            closing: Signal::default(),
            ctx,
            ctx_cancel,
            origin: Mutex::new(origin),
            write_fail: AtomicU32::new(0),
            closed: AtomicBool::new(false),
            close_frame: Mutex::new(None),
            done: Signal::default(),
        })
    }

    /// The peer's origin id; empty until known.
    pub(crate) fn peer_origin(&self) -> String {
        self.origin
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    /// Records the peer's origin id unless one is known already.
    pub(crate) fn learn_origin(&self, origin: &str) {
        let mut o = self.origin.lock().unwrap_or_else(PoisonError::into_inner);
        if o.is_empty() {
            *o = origin.to_string();
        }
    }

    /// Writes one text frame within [`WRITE_TIMEOUT`]. Fails at once on a
    /// closing link.
    pub(crate) async fn write(&self, text: String) -> Result<(), ()> {
        if self.closing.is_fired() {
            return Err(());
        }
        let send = async {
            let mut sink = self.sink.lock().await;
            sink.send(Message::text(text)).await
        };
        tokio::select! {
            r = tokio::time::timeout(WRITE_TIMEOUT, send) => match r {
                Ok(Ok(())) => Ok(()),
                _ => Err(()),
            },
            () = self.closing.wait() => Err(()),
        }
    }

    /// Counts one write outcome; returns whether the link reached
    /// [`MAX_WRITE_FAILURES`] consecutive failures.
    pub(crate) fn record_write(&self, ok: bool) -> bool {
        if ok {
            self.write_fail.store(0, Ordering::SeqCst);
            return false;
        }
        self.write_fail.fetch_add(1, Ordering::SeqCst) + 1 >= MAX_WRITE_FAILURES
    }

    /// Closes the link once with `code` and `reason`: records the close
    /// frame and wakes the reader, which sends it.
    pub(crate) fn close(&self, code: u16, reason: &str) {
        if self.closed.swap(true, Ordering::SeqCst) {
            return;
        }
        *self
            .close_frame
            .lock()
            .unwrap_or_else(PoisonError::into_inner) = Some((code, reason.to_string()));
        self.closing.fire();
        self.ctx_cancel.cancel();
    }

    /// Sends the close frame [`close`](Self::close) recorded, if any.
    pub(crate) async fn send_close(&self) {
        let frame = self
            .close_frame
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .take();
        if let Some((code, reason)) = frame {
            let _ = tokio::time::timeout(WRITE_TIMEOUT, async {
                let mut sink = self.sink.lock().await;
                let _ = sink.send(close_message(code, &reason)).await;
            })
            .await;
        }
    }

    /// Drops the socket's write half once the reader is done, so the
    /// connection closes even while something still holds the link.
    /// Later writes fail.
    pub(crate) async fn release(&self) {
        let dead = futures_util::sink::unfold((), |(), _: Message| async {
            Err::<(), _>(WsError::AlreadyClosed)
        });
        *self.sink.lock().await = Box::pin(dead);
    }

    /// Marks the link down without a close frame: the socket already
    /// failed.
    pub(crate) fn mark_down(&self) {
        self.closed.store(true, Ordering::SeqCst);
        self.closing.fire();
        self.ctx_cancel.cancel();
    }
}
