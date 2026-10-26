//! SDK event bus. Gated behind the `bus` feature.
//!
//! Ports the **core** of the Go canonical runtime
//! (`hop.top/kit/go/runtime/bus`): the [`Topic`] type and its wildcard
//! matching, the two topic validators, the [`Event`] envelope,
//! [`Qualifiers`], and in-memory publish/subscribe dispatch.
//!
//! # Topic notation
//!
//! Topics follow `[Source].[Category].[Object].[Action]`, with a
//! past-tense action segment — `crm.sales.deal.created`,
//! `billing.finance.invoice.paid`. See [`topic`] for the two validators
//! and how they differ.
//!
//! # What is not ported
//!
//! The Go package also ships a `NetworkAdapter` (WebSocket peer relay,
//! reconnect/backoff, star topology, auth handshake) and a SQLite-backed
//! adapter. The network adapter is not yet ported but is in scope, behind
//! its own feature; the SQLite adapter stays out. The plan and feature
//! layout live in `docs/contributors/architecture/rust-bus-parity.md`.
//!
//! See [`mem`] for sync dispatch, [`SharedBus`] for threads, draining
//! close and re-entrancy, and [`async_dispatch`] for async subscribe,
//! its bounded pool and the executor seam, which needs no async runtime.
//! Publish, handlers and close carry a [`Context`], the Rust form of
//! Go's `context.Context`.
//!
//! [`DynBus`] is the bus to use: Go's `New`, composing the adapter,
//! network and sink layers in Go's order. [`adapter`] holds the
//! transport seam, [`Adapter`] and [`MemoryAdapter`]; [`sink`] the
//! [`TeeBus`], its sinks and the `KIT_BUS_SINK` switch. [`Bus`] and
//! [`SharedBus`] are the in-memory building block under it, with no
//! adapter, network or env sinks.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::bus::{DynBus, Event, Mode};
//! use serde_json::json;
//!
//! let bus = DynBus::builder().enforce(Mode::Strict).build();
//! bus.subscribe("crm.sales.deal.*", |e| {
//!     println!("{} from {}", e.topic, e.source);
//!     Ok(())
//! });
//!
//! let event = Event::new("crm.sales.deal.created", "crm", json!({"id": 1}));
//! bus.publish(&event).unwrap();
//! ```

pub mod adapter;
pub mod async_dispatch;
pub mod builder;
pub mod cloudevents;
mod compose;
pub mod config;
pub mod context;
mod drain;
pub mod event;
pub mod mem;
pub mod qualifiers;
mod quote;
mod shared;
pub mod sink;
pub mod stream;
pub mod topic;

pub use adapter::{async_handler, Adapter, AsyncHandler, MemoryAdapter};
pub use async_dispatch::{MaybeSend, Spawn, Task, DEFAULT_MAX_ASYNC};
pub use builder::{parse_topic, prefixed_topic_of, topic_of, TopicBuilder};
pub use cloudevents::{CloudEvent, CloudEventError, FrameFormat};
pub use compose::{DynBus, DynBusBuilder};
pub use config::{
    mode_from_config, mode_from_config_with, mode_from_env, mode_from_env_with, mode_from_string,
    ConfigGetter, ParseModeError, CONFIG_KEY_ENFORCE, ENV_ENFORCE,
};
pub use context::{CancelHandle, Context, ContextError};
pub use event::Event;
pub use mem::{Bus, BusBuilder, BusError, ErrFn, Handler, Mode, SharedBus, SubscriptionId};
pub use qualifiers::{qualifiers_from, Qualifiers};
#[cfg(not(target_arch = "wasm32"))]
pub use sink::{
    reset_env_sink_err_reporter, set_env_sink_err_reporter, ENV_SINK_KIND, ENV_SINK_PATH,
};
pub use sink::{JsonlSink, Sink, StdoutSink, TeeBus};
pub use topic::{
    prefix_topics, validate, validate_topic, InvalidTopicError, Topic, TopicMap,
    PAST_TENSE_WHITELIST,
};
