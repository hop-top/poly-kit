//! Sinks: side-effect consumers of published events (logging, audit
//! trails). Mirrors Go `go/runtime/bus/sink.go`, `jsonlsink.go`,
//! `stdoutsink.go` and `env_sink.go`.
//!
//! A [`TeeBus`] wraps any [`Adapter`] and hands every event a publish
//! accepted to its sinks. [`JsonlSink`] writes JSON lines,
//! [`StdoutSink`] one human-readable line per event. With
//! [`ENV_SINK_KIND`] set, [`DynBus::new`](super::DynBus::new) attaches a
//! sink itself (not on wasm32, which has no filesystem).
//!
//! Sink errors use [`BusError::Handler`], whose text is the error's own,
//! as Go's `error` values print.

use std::sync::Arc;

use super::adapter::{Adapter, AsyncHandler};
use super::async_dispatch::Spawn;
use super::context::Context;
use super::event::Event;
use super::mem::{BusError, Handler, SharedErrFn, SubscriptionId};

#[cfg(not(target_arch = "wasm32"))]
mod env;
mod gofmt;
mod jsonl;
mod stdout;

#[cfg(not(target_arch = "wasm32"))]
pub use env::{
    reset_env_sink_err_reporter, set_env_sink_err_reporter, ENV_SINK_KIND, ENV_SINK_PATH,
};
pub use jsonl::JsonlSink;
pub use stdout::StdoutSink;

/// Receives events for side-effect processing: Go's `Sink`. A sink
/// error never fails a publish or blocks handler delivery.
pub trait Sink: Send + Sync {
    /// Takes one event a publish accepted.
    fn drain(&self, ctx: &Context, event: &Event) -> Result<(), BusError>;

    /// Releases the sink; [`TeeBus`] calls it once the bus is closed.
    fn close(&self) -> Result<(), BusError>;
}

/// Wraps a bus and fans every published event out to a set of
/// [`Sink`]s: Go's `TeeBus`. Subscriptions go to the wrapped bus.
pub struct TeeBus {
    bus: Arc<dyn Adapter>,
    sinks: Vec<Box<dyn Sink>>,
    on_err: Option<SharedErrFn>,
}

impl TeeBus {
    /// Wraps `bus`, fanning out to `sinks` in order: Go's `NewTeeBus`.
    /// Sink errors are dropped until [`TeeBus::on_error`] sets a
    /// receiver.
    pub fn new(bus: impl Adapter + 'static, sinks: Vec<Box<dyn Sink>>) -> Self {
        Self::wrap(Arc::new(bus), sinks, None)
    }

    /// Sends every sink error to `f`: Go's optional `onErr`.
    pub fn on_error(mut self, f: impl Fn(&BusError) + Send + Sync + 'static) -> Self {
        self.on_err = Some(Arc::new(f));
        self
    }

    pub(super) fn wrap(
        bus: Arc<dyn Adapter>,
        sinks: Vec<Box<dyn Sink>>,
        on_err: Option<SharedErrFn>,
    ) -> Self {
        TeeBus { bus, sinks, on_err }
    }
}

impl Adapter for TeeBus {
    /// Publishes to the wrapped bus, then, only if that succeeded, drains
    /// the event into every sink. Sink errors go to the error receiver
    /// and never change the result.
    fn publish(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        self.bus.publish(ctx, event)?;
        for s in &self.sinks {
            if let (Err(e), Some(f)) = (s.drain(ctx, event), &self.on_err) {
                f(&e);
            }
        }
        Ok(())
    }
    fn subscribe(&self, pattern: &str, handler: Handler) -> SubscriptionId {
        self.bus.subscribe(pattern, handler)
    }
    fn subscribe_async(
        &self,
        pattern: &str,
        spawner: Arc<dyn Spawn>,
        handler: AsyncHandler,
    ) -> SubscriptionId {
        self.bus.subscribe_async(pattern, spawner, handler)
    }
    fn unsubscribe(&self, id: SubscriptionId) -> bool {
        self.bus.unsubscribe(id)
    }
    /// Closes the wrapped bus, then every sink. Returns the bus error if
    /// any, else the first sink error; every sink is closed regardless.
    fn close(&self, ctx: &Context) -> Result<(), BusError> {
        let bus = self.bus.close(ctx);
        let mut first = Ok(());
        for s in &self.sinks {
            if let Err(e) = s.close() {
                if first.is_ok() {
                    first = Err(e);
                }
            }
        }
        bus.and(first)
    }
}

/// The env-sink layer [`DynBus`](super::DynBus) composes last: with
/// sinks configured by the environment, `bus` behind a [`TeeBus`] whose
/// sink errors go to `reporter`; otherwise `bus` itself. Always `bus`
/// on wasm32.
pub(super) fn env_layer(bus: Arc<dyn Adapter>, reporter: Option<&SharedErrFn>) -> Arc<dyn Adapter> {
    #[cfg(not(target_arch = "wasm32"))]
    {
        let sinks = env::sinks_from_env();
        if !sinks.is_empty() {
            return Arc::new(TeeBus::wrap(bus, sinks, reporter.cloned()));
        }
    }
    #[cfg(target_arch = "wasm32")]
    let _ = reporter;
    bus
}
