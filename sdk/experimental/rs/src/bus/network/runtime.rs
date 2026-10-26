//! Where the adapter's I/O runs, and the tokio [`Spawn`].
//!
//! Go runs each link on goroutines the scheduler owns. The Rust port runs
//! them on one process-wide tokio runtime, a current-thread runtime on a
//! dedicated thread (`kit-bus-network`) started on first use. Callers
//! need no runtime of their own, and nothing here assumes one: the
//! blocking methods hand work to that thread and wait, and the inbound
//! publish, which runs bus handlers, moves to tokio's blocking pool so a
//! slow handler never stalls the sockets.

use std::cell::Cell;
use std::future::Future;
use std::sync::{mpsc, OnceLock};
use std::thread;

use tokio::runtime::{Builder, Handle};

use crate::bus::async_dispatch::{Spawn, Task};

/// Runs bus [`Task`]s on a tokio runtime: the executor seam of
/// [`DynBus::subscribe_async`](crate::bus::DynBus::subscribe_async) for
/// tokio users, and what the adapter subscribes its outbound forwarder
/// with.
///
/// Built from a [`Handle`], so it works from any thread, inside the
/// runtime or not (bare `tokio::spawn` panics off the runtime).
#[derive(Clone, Debug)]
pub struct TokioSpawn(Handle);

impl TokioSpawn {
    /// Spawns onto the runtime `handle` belongs to.
    pub fn new(handle: Handle) -> Self {
        TokioSpawn(handle)
    }

    /// Spawns onto the runtime of the calling thread; `None` off any
    /// runtime.
    pub fn current() -> Option<Self> {
        Handle::try_current().ok().map(TokioSpawn)
    }
}

impl From<Handle> for TokioSpawn {
    fn from(handle: Handle) -> Self {
        TokioSpawn(handle)
    }
}

impl Spawn for TokioSpawn {
    fn spawn(&self, task: Task) {
        drop(self.0.spawn(task));
    }
}

thread_local! {
    static ON_NETWORK_THREAD: Cell<bool> = const { Cell::new(false) };
}

/// The shared network runtime, started on first use.
pub(crate) fn handle() -> Handle {
    static RUNTIME: OnceLock<Handle> = OnceLock::new();
    RUNTIME
        .get_or_init(|| {
            let (tx, rx) = mpsc::channel();
            thread::Builder::new()
                .name("kit-bus-network".into())
                .spawn(move || {
                    ON_NETWORK_THREAD.with(|f| f.set(true));
                    let rt = Builder::new_current_thread()
                        .enable_all()
                        .build()
                        .expect("bus: network: build the tokio runtime");
                    let _ = tx.send(rt.handle().clone());
                    rt.block_on(std::future::pending::<()>());
                })
                .expect("bus: network: spawn the runtime thread");
            rx.recv().expect("bus: network: runtime thread died")
        })
        .clone()
}

/// Panics when called on the network thread, where waiting for the
/// network would wait forever.
pub(crate) fn assert_off_network_thread(what: &str) {
    assert!(
        !ON_NETWORK_THREAD.with(Cell::get),
        "bus: network: {what} blocks; call it from outside the network runtime \
         (or use the async variant)"
    );
}

/// Runs `fut` on the network runtime and blocks the calling thread for
/// its result.
pub(crate) fn block_on<F>(what: &str, fut: F) -> F::Output
where
    F: Future + Send + 'static,
    F::Output: Send + 'static,
{
    assert_off_network_thread(what);
    let (tx, rx) = mpsc::channel();
    handle().spawn(async move {
        let _ = tx.send(fut.await);
    });
    rx.recv().expect("bus: network: task panicked")
}

/// Runs `fut` on the network runtime and awaits its result from any
/// executor.
pub(crate) async fn run<F>(fut: F) -> F::Output
where
    F: Future + Send + 'static,
    F::Output: Send + 'static,
{
    handle()
        .spawn(fut)
        .await
        .expect("bus: network: task panicked")
}
