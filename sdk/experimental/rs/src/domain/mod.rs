//! Domain primitives. Gated behind the `domain` feature, which builds on
//! `bus` for [`Topic`](crate::bus::Topic), [`Context`](crate::bus::Context),
//! [`prefix_topics`](crate::bus::prefix_topics) and the
//! [`Spawn`](crate::bus::Spawn) executor seam.
//!
//! Ports the state machine of the Go canonical runtime
//! (`hop.top/kit/go/runtime/domain`): [`StateMachine`] enforces allowed
//! transitions and, given an [`EventPublisher`], publishes a veto-able
//! pre-transition event and a fire-and-forget post-transition event.
//! The rest of the Go package (repository, service, validator) is not
//! ported.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::bus::Context;
//! use hop_top_kit::domain::{Rules, State, StateMachine};
//!
//! let rules: Rules = [
//!     (State::from("open"), vec![State::from("closed")]),
//!     (State::from("closed"), vec![]),
//! ]
//! .into_iter()
//! .collect();
//! let sm = StateMachine::new(rules, None);
//!
//! sm.transition(&Context::background(), "open", "closed", false).unwrap();
//! let err = sm.transition(&Context::background(), "closed", "open", false).unwrap_err();
//! assert!(err.is_invalid_transition());
//! assert_eq!(
//!     err.to_string(),
//!     r#"invalid state transition: "closed" → "open" (allowed: [])"#,
//! );
//! ```

pub(crate) mod quote;
pub mod statemachine;
pub mod topics;

pub use statemachine::{
    is_invalid_transition, EventPublisher, Payload, PublishError, Rules, State, StateMachine,
    StateMachineError, TransitionError, TransitionPayload, SOURCE,
};
pub use topics::{
    default_state_machine_topics, StateMachineTopics, DEFAULT_POST_TRANSITIONED,
    DEFAULT_PRE_TRANSITIONED,
};
