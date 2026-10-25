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
//! adapter. The network adapter is not yet ported but is in scope, along
//! with async dispatch, behind their own features; the SQLite adapter
//! stays out. The plan and feature layout live in
//! `docs/contributors/architecture/rust-bus-parity.md`.
//!
//! Dispatch is currently synchronous — see [`mem`] for the model and its
//! consequences, and [`SharedBus`] for threads, draining close and
//! re-entrancy. Publish, handlers and close carry a [`Context`], the
//! Rust form of Go's `context.Context`.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::bus::{Bus, Event, Mode};
//! use serde_json::json;
//!
//! let mut bus = Bus::builder().enforce(Mode::Strict).build();
//! bus.subscribe("crm.sales.deal.*", |e| {
//!     println!("{} from {}", e.topic, e.source);
//!     Ok(())
//! });
//!
//! let event = Event::new("crm.sales.deal.created", "crm", json!({"id": 1}));
//! bus.publish(&event).unwrap();
//! ```

pub mod builder;
pub mod cloudevents;
pub mod config;
pub mod context;
pub mod event;
pub mod mem;
pub mod qualifiers;
mod quote;
mod shared;
pub mod stream;
pub mod topic;

pub use builder::{parse_topic, prefixed_topic_of, topic_of, TopicBuilder};
pub use cloudevents::{CloudEvent, CloudEventError, FrameFormat};
pub use config::{
    mode_from_config, mode_from_config_with, mode_from_env, mode_from_env_with, mode_from_string,
    ConfigGetter, ParseModeError, CONFIG_KEY_ENFORCE, ENV_ENFORCE,
};
pub use context::{CancelHandle, Context, ContextError};
pub use event::Event;
pub use mem::{Bus, BusBuilder, BusError, ErrFn, Handler, Mode, SharedBus, SubscriptionId};
pub use qualifiers::{qualifiers_from, Qualifiers};
pub use topic::{
    prefix_topics, validate, validate_topic, InvalidTopicError, Topic, TopicMap,
    PAST_TENSE_WHITELIST,
};
