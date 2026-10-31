//! Declarative guard engine. Gated behind the `policy` feature, which
//! builds on `domain` for the state machine's veto-able events.
//!
//! Ports `hop.top/kit/go/runtime/policy`: rules written as YAML
//! (`name`, `on`, `when` as a CEL expression, `effect` when it is true,
//! `otherwise` when it is not, `message`) are compiled at boot and
//! evaluated on three veto-able topics, composed deny-overrides. The
//! rule language and semantics are CEL's; see the module README for the
//! library choice and its vetting.
//!
//! Default for an event no rule watches: allow. A rule whose
//! expression errors denies.
//!
//! # Example
//!
//! ```
//! use std::sync::Arc;
//! use hop_top_kit::bus::{Context, DynBus, Event};
//! use hop_top_kit::policy::{parse_config, wire, with_principal, Engine, Principal};
//! use serde_json::json;
//!
//! let cfg = parse_config(br#"
//! policies:
//!   - name: admin-only-cancel
//!     on: kit.runtime.state.pre_transitioned
//!     when: 'payload.To == "CANCELED" && principal.role == "admin"'
//!     effect: allow
//!     otherwise: deny
//!     message: only admin may cancel
//! "#).unwrap();
//! let engine = Arc::new(Engine::with_cel(&cfg).unwrap());
//!
//! let bus = DynBus::builder().build();
//! let _unwire = wire(&bus, engine);
//!
//! let ev = Event::new(
//!     "kit.runtime.state.pre_transitioned",
//!     "domain.statemachine",
//!     json!({"From": "OPEN", "To": "CANCELED", "Force": false}),
//! );
//! let user = with_principal(&Context::background(), Principal { role: "user".into(), ..Default::default() });
//! let err = bus.publish_ctx(&user, &ev).unwrap_err();
//! assert!(err.to_string().contains("only admin may cancel"));
//!
//! let admin = with_principal(&Context::background(), Principal { role: "admin".into(), ..Default::default() });
//! bus.publish_ctx(&admin, &ev).unwrap();
//! ```
//!
//! # Where this differs from Go
//!
//! - The expression backend is `cel-rust`, not `cel-go`: no static type
//!   check (an unknown identifier or operand type fails at evaluation
//!   and denies), and `timestamp()`/`duration()` are not compiled in.
//! - The bus carries JSON, so a [`PreEntityPayload`] is recognised by
//!   shape where Go type-asserts, and a denial reaches the publisher as
//!   [`BusError::Handler`] text; `errors.Is(err, domain.ErrConflict)` is
//!   [`PolicyDeniedError::is_conflict`] on the typed error from
//!   [`Engine::decide`].
//! - The principal and the `context` attributes ride on the
//!   [`Context`] by type ([`with_principal`], [`with_context_attrs`]),
//!   not by key.
//! - The entity service is not ported (`domain` ports the state
//!   machine only); the entity topics are served to whatever publishes
//!   them.
//!
//! [`BusError::Handler`]: crate::bus::BusError::Handler
//! [`Context`]: crate::bus::Context

mod config;
mod engine;
mod error;
mod eval;
mod wire;

#[cfg(not(target_arch = "wasm32"))]
pub use config::load_config;
pub use config::{
    parse_config, stage_config, Config, Decision, Effect, Policy, STAGE_YAML,
    TOPIC_ENTITY_PRE_PERSISTED, TOPIC_ENTITY_PRE_VALIDATED, TOPIC_STAGE_VIOLATED,
    TOPIC_STATE_PRE_TRANSITIONED, VETO_TOPICS,
};
pub use engine::{
    default_principal_resolver, with_context_attrs, with_principal, ContextAttrs, Engine,
    EngineBuilder, Principal, PrincipalResolver, ScopeResolver, StageResolver, ViolationPayload,
    ViolationPublisher,
};
pub use error::{is_policy_denied, Error, PolicyDeniedError};
pub use eval::{Activation, CelEvaluator, EvalError, Evaluator};
pub use wire::{wire, Op, Phase, PolicyBus, PreEntityPayload, Unwire};
