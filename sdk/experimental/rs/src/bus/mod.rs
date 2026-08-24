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
//! adapter. Neither is ported. See ADR 0040 for the rationale and for
//! what to build instead should cross-process eventing become a real
//! requirement.
//!
//! Dispatch is synchronous — see [`mem`] for that decision and its
//! consequences.
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

pub mod event;
pub mod mem;
pub mod qualifiers;
pub mod topic;

pub use event::Event;
pub use mem::{Bus, BusBuilder, BusError, ErrFn, Handler, Mode, SharedBus, SubscriptionId};
pub use qualifiers::{qualifiers_from, Qualifiers};
pub use topic::{
    prefix_topics, validate, validate_topic, InvalidTopicError, Topic, TopicMap,
    PAST_TENSE_WHITELIST,
};
