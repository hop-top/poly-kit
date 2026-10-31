//! The registration seam: names, the four capabilities, the registry,
//! and the policy gate.

use std::collections::HashMap;
use std::fmt;
use std::future::Future;
use std::pin::Pin;
use std::sync::Arc;

use super::cancel::CancelToken;

/// Service identifier grammar: lowercase ASCII, digits, and internal
/// hyphens, starting with a letter.
///
/// An identifier is a CLI word, a config key segment, and an event
/// payload value at once, which is why the grammar is contract rather
/// than a local convention. Expressed as a predicate rather than a
/// regex so the `serve` feature does not pull `regex` in for one
/// pattern.
pub const NAME_PATTERN: &str = "^[a-z][a-z0-9-]*$";

/// Names reserved for selector vocabulary. Registering one would make
/// `serve <name>` ambiguous with an aggregate form, and is why the
/// inspection form is a `--list` flag rather than a `serve list` child.
pub const RESERVED_NAMES: &[&str] = &["all", "list", "none"];

/// Reports whether `name` is one of the reserved selector words.
pub fn is_reserved_name(name: &str) -> bool {
    RESERVED_NAMES.contains(&name)
}

/// Validates a service identifier against [`NAME_PATTERN`] and the
/// reserved set, returning the refusal message or `None`.
pub fn validate_name(name: &str) -> Option<String> {
    if name.is_empty() {
        return Some("serve: service name is empty".to_string());
    }
    if !matches_name_pattern(name) {
        return Some(format!(
            "serve: service name {name:?} must be lowercase letters, \
             digits, or hyphens, starting with a letter"
        ));
    }
    if is_reserved_name(name) {
        return Some(format!("serve: service name {name:?} is reserved"));
    }
    None
}

fn matches_name_pattern(name: &str) -> bool {
    let mut chars = name.chars();
    match chars.next() {
        Some(c) if c.is_ascii_lowercase() => {}
        _ => return false,
    }
    chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-')
}

/// A boxed future a service capability returns.
///
/// Rust has no `async fn` in object-safe traits before the async-fn-in-
/// trait dyn story lands, so the two blocking capabilities return a
/// pinned boxed future. `Send` because the supervisor may be driven on
/// a multi-threaded runtime.
pub type ServeFuture<'a, T> = Pin<Box<dyn Future<Output = T> + Send + 'a>>;

/// Handed to [`Service::start`] so a service reports readiness.
///
/// Reporting more than once per start is ignored rather than an error:
/// the supervisor reports ready at most once per start either way.
#[derive(Clone, Debug)]
pub struct ReadySignal(CancelToken);

impl ReadySignal {
    pub(crate) fn new(token: CancelToken) -> Self {
        ReadySignal(token)
    }

    /// Reports that every acquisition that can fail deterministically
    /// has succeeded. Idempotent.
    pub fn report(&self) {
        self.0.cancel();
    }
}

/// One long-running thing a tool can serve.
///
/// The four required capabilities are the contract's minimum: a name, a
/// start that blocks until cancelled or failed, a readiness report, and
/// a stop. Go expresses them as a four-method interface; a trait is the
/// Rust idiom for the same capability set. What is fixed is the
/// capability set and each one's behavior, not a method table.
///
/// `Send + Sync` because the supervisor drives services concurrently on
/// a tokio runtime that may move futures between worker threads.
pub trait Service: Send + Sync {
    /// Stable service identifier. Must satisfy [`validate_name`] and
    /// must not change across releases: renaming one is a breaking
    /// change to the command surface, the config file, and every
    /// subscriber.
    fn name(&self) -> &str;

    /// Begins serving. Resolves `Ok(())` when `cancel` fires (a clean
    /// stop) and `Err` when the service fails.
    ///
    /// `ready` must be reported after every acquisition that can fail
    /// deterministically has succeeded — the listener bound, the file
    /// created, the subscription attached.
    ///
    /// A start that returns before reporting ready is a start failure
    /// even when it returns `Ok`: it was asked to serve and it did not.
    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>>;

    /// Whether the service is currently accepting work. Readiness, not
    /// liveness: a ready service may be idle, and may later fail.
    fn ready(&self) -> bool;

    /// Drains in-flight work and releases resources. The supervisor
    /// bounds it by the stop timeout and abandons a stop that exceeds
    /// it, so an implementation must respect `cancel` rather than
    /// assume it will be allowed to finish.
    fn stop<'a>(&'a self, cancel: CancelToken) -> ServeFuture<'a, Result<(), String>>;

    /// Optional configuration gate — the second of the three validation
    /// gates. Returns a refusal message, or `None` when the resolved
    /// configuration is complete and usable.
    fn validate(&self) -> Option<String> {
        None
    }

    /// Optional ordering declaration. Start order is topological over
    /// these, ties broken by registration order; stop order is the
    /// exact reverse of the order services actually started.
    fn depends_on(&self) -> Vec<String> {
        Vec::new()
    }

    /// Optional address declaration. Read once the service reports
    /// ready and carried into the readiness event, so an operator
    /// learns where the service actually bound — including a port the
    /// kernel picked for a wildcard address, which configuration
    /// cannot reveal.
    fn addr(&self) -> Option<String> {
        None
    }

    /// Optional policy declaration: the `kit/side-effect` and
    /// `kit/network` classes, in that order. A service that omits it is
    /// unclassified and passes the policy gate.
    fn class(&self) -> Option<(String, String)> {
        None
    }
}

/// The verdict a [`PolicyGate`] returns.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Verdict {
    pub ok: bool,
    pub reason: Option<String>,
}

impl Verdict {
    /// Allows the service.
    pub fn allow() -> Self {
        Verdict {
            ok: true,
            reason: None,
        }
    }

    /// Denies the service, carrying the reason into the refusal.
    pub fn deny(reason: impl Into<String>) -> Self {
        Verdict {
            ok: false,
            reason: Some(reason.into()),
        }
    }
}

/// The third validation gate. A service whose declared class the gate
/// denies is refused at `UNAUTHORIZED`, exit 5.
///
/// The contract requires the gate, not Go's YAML-driven
/// `side_effect × network` table: a two-argument predicate satisfies
/// it. A registry with no gate passes every service, because a tool
/// that has wired no policy has expressed no restriction.
pub trait PolicyGate: Send + Sync {
    fn allow(&self, side_effect: &str, network: &str) -> Verdict;
}

/// Returned when a registration is rejected at construction time.
///
/// A duplicate name or an invalid one is a wiring bug in the tool's
/// entry point, not a runtime condition. Go panics; this port returns a
/// typed error the caller is expected to propagate or `expect` at
/// wiring time. What the contract forbids is last-writer-wins — the
/// error has no path that leaves the second registration silently
/// winning.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ServiceRegistrationError(pub String);

impl fmt::Display for ServiceRegistrationError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for ServiceRegistrationError {}

/// The seam kit-owned and adopter-owned services register into. A tool
/// builds one before the root command parses; the supervisor reads it.
#[derive(Default, Clone)]
pub struct ServiceRegistry {
    by_name: HashMap<String, Arc<dyn Service>>,
    order: Vec<String>,
}

impl ServiceRegistry {
    /// An empty registry.
    pub fn new() -> Self {
        ServiceRegistry::default()
    }

    /// Adds `svc` under its name.
    ///
    /// Fails on an invalid name and on a duplicate. An adopter
    /// deliberately replacing a kit-shipped service calls
    /// [`ServiceRegistry::override_service`] instead — the documented
    /// escape hatch, and the only path that accepts a duplicate name.
    pub fn register(&mut self, svc: Arc<dyn Service>) -> Result<(), ServiceRegistrationError> {
        let name = svc.name().to_string();
        if let Some(msg) = validate_name(&name) {
            return Err(ServiceRegistrationError(msg));
        }
        if self.by_name.contains_key(&name) {
            return Err(ServiceRegistrationError(format!(
                "serve: service {name:?} already registered (use override to replace)"
            )));
        }
        self.order.push(name.clone());
        self.by_name.insert(name, svc);
        Ok(())
    }

    /// Registers `svc`, replacing any service already under its name
    /// and keeping that name's original position in [`Self::list`].
    ///
    /// Still refuses an invalid name: override lifts the collision
    /// rule, not the grammar.
    pub fn override_service(
        &mut self,
        svc: Arc<dyn Service>,
    ) -> Result<(), ServiceRegistrationError> {
        let name = svc.name().to_string();
        if let Some(msg) = validate_name(&name) {
            return Err(ServiceRegistrationError(msg));
        }
        if !self.by_name.contains_key(&name) {
            self.order.push(name.clone());
        }
        self.by_name.insert(name, svc);
        Ok(())
    }

    /// The service registered under `name`, if any.
    pub fn lookup(&self, name: &str) -> Option<Arc<dyn Service>> {
        self.by_name.get(name).cloned()
    }

    /// Every registered identifier, in registration order.
    pub fn names(&self) -> Vec<String> {
        self.order.clone()
    }

    /// Every registered service, in registration order.
    pub fn list(&self) -> Vec<Arc<dyn Service>> {
        self.order
            .iter()
            .filter_map(|n| self.by_name.get(n).cloned())
            .collect()
    }

    /// Number of registered services.
    pub fn len(&self) -> usize {
        self.by_name.len()
    }

    /// Whether nothing is registered.
    pub fn is_empty(&self) -> bool {
        self.by_name.is_empty()
    }
}
