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
//! # What this module is, and how it mounts
//!
//! Two halves, two features:
//!
//! - `serve` — the **library half**, usable from any parser: the
//!   registration seam ([`ServiceRegistry`], [`Service`]) with the four
//!   capabilities, the name grammar, the reserved names, the
//!   construction-time duplicate refusal and its `override` escape
//!   hatch, and registration-order listing; resolution ([`resolve`]) —
//!   the hierarchy, the override rule, the three gates in order, silent
//!   skipping of a disabled service, the zero-services and the arity
//!   refusals; readiness, the six lifecycle transitions with their exact
//!   topic strings and payload keys, ordered shutdown with the
//!   contract's budgets and second-signal escalation,
//!   `services.failure_policy`, the `services.*` keys with their
//!   defaults, and the exit-code taxonomy ([`LifecycleOutcome::exit_code`])
//!   routed onto the shared `output::CliError` codes.
//! - `serve-cli` — the **command half**, [`command`]: `serve [SERVICE]`
//!   with `--list`, `--enable`/`--disable`, `--ready-timeout`,
//!   `--stop-timeout` and `--shutdown-timeout`, built as a kit
//!   `CommandSpec` by [`command::serve_command`], mounted on a kit root
//!   by [`command::mount`], and run from the handler's `Invocation` to
//!   an exit code by [`command::run`]. No parsing-provider type is part
//!   of the contract.
//!
//! Configuration keys resolve through [`configs_from_lookup`] (features
//! `serve` and `config`): `<TOOL>_SERVICES_<NAME>_ENABLED`,
//! `_READY_TIMEOUT` and `_STOP_TIMEOUT` read from a config `EnvLookup`,
//! with file values synced into it ranking below the environment.
//! [`configs_from_keys`] is the same resolution over any dotted-key
//! reader, for callers without the config feature.
//!
//! # Deliberate non-goals
//!
//! Everything the contract lists under "Explicitly not required" is
//! absent: no REST/OpenAPI projection, no command-tree reflection, no
//! permission/provenance/audit surface, no `toolspec/policy` table (the *gate* is here as [`PolicyGate`], a
//! two-argument predicate, which is what the contract requires). A socket
//! service ships apart, as feature `socket` (`crate::socket`).
//!
//! # Sinks
//!
//! Rust *does* have an event bus ([`crate::bus`]), so the six
//! transitions publish under exactly the contract topic strings. The
//! supervisor takes a narrow [`Publisher`] trait rather than a
//! [`crate::bus::Bus`] directly: `serve` does not depend on the `bus`
//! feature, and an adopter can route lifecycle events to the kit bus or
//! to any sink of their own. There is no structured logger in this port, so the log
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
#[cfg(all(feature = "config", not(target_arch = "wasm32")))]
pub use config::configs_from_lookup;
pub use config::{
    configs_from_keys, failure_error, parse_duration, worst_outcome, FailurePolicy,
    LifecycleOutcome, ServiceConfig, ServiceConfigs, SupervisorConfig, DEFAULT_READY_TIMEOUT,
    DEFAULT_SHUTDOWN_TIMEOUT, DEFAULT_STOP_TIMEOUT, KEY_ENABLED, KEY_FAILURE_POLICY,
    KEY_READY_TIMEOUT, KEY_SHUTDOWN_TIMEOUT, KEY_STOP_TIMEOUT,
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
