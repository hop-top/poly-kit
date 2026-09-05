//! Serve hierarchy and service lifecycle — the Rust port of the
//! contract in `docs/contracts/serve-lifecycle.md`, §"Cross-language
//! parity".
//!
//! `<tool> serve` supervises every configured and enabled service;
//! `<tool> serve <service>` selects exactly one and overrides aggregate
//! enablement. Both forms share one lifecycle implementation, so a
//! service started by the selector observes the same readiness,
//! shutdown and exit semantics as the same service started by the
//! supervisor.
//!
//! # What this module is, and is not
//!
//! The contract names one prerequisite for this port specifically: the
//! Rust SDK's `cli.rs` is empty, so there is no kit-owned place to
//! mount a `serve` parent. That is still true — this module ships the
//! **library half** of the contract and stops at the seam a command
//! layer would mount onto.
//!
//! Implemented here, and testable without a parser:
//!
//! - The registration seam ([`ServiceRegistry`], [`Service`]) with the
//!   four capabilities, the name grammar, the reserved names, the
//!   construction-time duplicate refusal and its `override` escape
//!   hatch, and registration-order listing.
//! - Resolution ([`resolve`]) — the hierarchy, the override rule, the
//!   three gates in order, silent skipping of a disabled service, and
//!   the zero-services refusal. It takes the positional operands as a
//!   `Vec<String>`; what it does not do is *parse* them off a command
//!   line.
//! - Readiness, the six lifecycle transitions with their exact topic
//!   strings and payload keys, ordered shutdown with the contract's
//!   budgets and second-signal escalation, `services.failure_policy`,
//!   and the `services.*` keys with their defaults.
//! - The exit-code taxonomy as values ([`LifecycleOutcome::exit_code`]),
//!   routed onto the shared `output::CliError` codes.
//!
//! Deferred until the SDK grows a command layer, because there is
//! nothing to hang them on:
//!
//! - Mounting `serve` / `serve <service>` as commands, and therefore
//!   the two-positional-arguments arity refusal *as a parse-time
//!   behavior*. [`resolve`] returns `USAGE`/2 for a 2-element operand
//!   vector, so the semantics are here and pinned; what is missing is
//!   the parser that would produce that vector.
//! - `--list` **as a flag**. [`list_services`] produces the rows in
//!   registration order; no flag exists to trigger it.
//! - The `--enable` / `--disable` / `--ready-timeout` / `--stop-timeout`
//!   / `--shutdown-timeout` flag names, and env-var spellings such as
//!   `<TOOL>_SERVICES_API_ENABLED`. The contract makes the latter
//!   contract "only for a port that has env resolution at all"; this
//!   port has none, and reads the key *names* out of the
//!   [`ServiceConfigs`] map a caller resolves however it likes.
//!
//! Building a whole command framework to close that gap is out of
//! scope for this module; the day `cli.rs` gains one, mounting these
//! pieces is a small, mechanical addition.
//!
//! # Deliberate non-goals
//!
//! Everything the contract lists under "Explicitly not required" is
//! absent: no REST/OpenAPI projection, no socket service, no
//! command-tree reflection, no permission/provenance/audit surface, no
//! `toolspec/policy` table (the *gate* is here as [`PolicyGate`], a
//! two-argument predicate, which is what the contract requires).
//!
//! # Sinks
//!
//! Rust *does* have an event bus ([`crate::bus`]), so the six
//! transitions publish under exactly the contract topic strings. The
//! bus is `Rc`-backed and therefore `!Send`, while the supervisor's
//! futures may move between tokio worker threads, so the supervisor
//! takes a narrow [`Publisher`] trait rather than a [`crate::bus::Bus`]
//! directly. There is no structured logger in this port, so the log
//! sink is [`StderrLogger`] — `key=value` pairs, per the contract's
//! floor for a port with neither.
//!
//! # Example
//!
//! ```no_run
//! use std::sync::Arc;
//! use hop_top_kit::serve::{
//!     resolve, CancelToken, ReadySignal, ResolveRequest, ServeFuture, Service,
//!     ServiceConfig, ServiceConfigs, ServiceRegistry, Supervisor, SupervisorOptions,
//! };
//!
//! struct Api;
//!
//! impl Service for Api {
//!     fn name(&self) -> &str { "api" }
//!     fn start<'a>(&'a self, cancel: CancelToken, ready: ReadySignal)
//!         -> ServeFuture<'a, Result<(), String>> {
//!         Box::pin(async move {
//!             ready.report();
//!             cancel.cancelled().await;
//!             Ok(())
//!         })
//!     }
//!     fn ready(&self) -> bool { true }
//!     fn stop<'a>(&'a self, _cancel: CancelToken)
//!         -> ServeFuture<'a, Result<(), String>> {
//!         Box::pin(async { Ok(()) })
//!     }
//! }
//!
//! # async fn demo() {
//! let mut registry = ServiceRegistry::new();
//! registry.register(Arc::new(Api)).expect("wiring");
//!
//! let mut configs = ServiceConfigs::new();
//! configs.insert("api".to_string(), ServiceConfig::enabled());
//!
//! let outcome = resolve(&registry, &ResolveRequest {
//!     args: Vec::new(),
//!     configs: Some(&configs),
//!     policy: None,
//! });
//!
//! let sig = hop_top_kit::serve::signal_controller().expect("signals");
//! let sup = Supervisor::new(registry, SupervisorOptions {
//!     escalate: Some(sig.escalate.clone()),
//!     ..SupervisorOptions::default()
//! });
//! let res = sup.run(sig.shutdown.clone(), &outcome.selected, &configs).await;
//! std::process::exit(res.exit_code);
//! # }
//! ```

mod cancel;
#[cfg(feature = "serve-cli")]
pub mod command;
mod config;
mod events;
mod registry;
mod resolve;
mod signals;
mod supervisor;

pub use cancel::CancelToken;
pub use config::{
    failure_error, parse_duration, worst_outcome, FailurePolicy, LifecycleOutcome, ServiceConfig,
    ServiceConfigs, SupervisorConfig, DEFAULT_READY_TIMEOUT, DEFAULT_SHUTDOWN_TIMEOUT,
    DEFAULT_STOP_TIMEOUT, KEY_ENABLED, KEY_FAILURE_POLICY, KEY_READY_TIMEOUT, KEY_SHUTDOWN_TIMEOUT,
    KEY_STOP_TIMEOUT,
};
pub use events::{
    default_topics, EventPayload, Publisher, ServeLogger, StderrLogger, ACTION_FAILED,
    ACTION_READY_REPORTED, ACTION_STARTED, ACTION_STOPPED, DEFAULT_TOPIC_PREFIX, OBJECT_SERVICE,
    OBJECT_SUPERVISOR, PAYLOAD_KEY_ADDRESS, PAYLOAD_KEY_ERROR, PAYLOAD_KEY_SERVICE, TRANSITIONS,
};
pub use registry::{
    is_reserved_name, validate_name, PolicyGate, ReadySignal, ServeFuture, Service,
    ServiceRegistrationError, ServiceRegistry, Verdict, NAME_PATTERN, RESERVED_NAMES,
};
pub use resolve::{
    list_services, resolve, start_order, ResolveOutcome, ResolveRequest, ServiceListing,
};
pub use signals::{signal_controller, SignalController, SHUTDOWN_SIGNALS};
pub use supervisor::{RunResult, Supervisor, SupervisorOptions};
