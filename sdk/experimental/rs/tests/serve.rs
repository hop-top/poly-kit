//! Conformance tests for the serve contract
//! (`docs/contracts/serve-lifecycle.md`, §"Cross-language parity").
//!
//! Every assertion here pins a clause of that section rather than the
//! current shape of the implementation. Where the contract is silent —
//! column layout, `elapsed_ms`, the exact wording of a message — the
//! tests are too.

#![cfg(feature = "serve")]

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hop_top_kit::serve::{
    default_topics, is_reserved_name, list_services, resolve, start_order, validate_name,
    CancelToken, EventPayload, FailurePolicy, LifecycleOutcome, PolicyGate, Publisher, ReadySignal,
    ResolveRequest, RunResult, ServeFuture, ServeLogger, Service, ServiceConfig, ServiceConfigs,
    ServiceRegistry, Supervisor, SupervisorConfig, SupervisorOptions, Verdict,
    DEFAULT_READY_TIMEOUT, DEFAULT_SHUTDOWN_TIMEOUT, DEFAULT_STOP_TIMEOUT, DEFAULT_TOPIC_PREFIX,
    RESERVED_NAMES,
};

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

type StartFn =
    Box<dyn Fn(CancelToken, ReadySignal) -> ServeFuture<'static, Result<(), String>> + Send + Sync>;
type StopFn = Box<dyn Fn(CancelToken) -> ServeFuture<'static, Result<(), String>> + Send + Sync>;

/// A service whose four capabilities are supplied per test.
struct Fake {
    name: String,
    start_fn: StartFn,
    stop_fn: StopFn,
    is_ready: Arc<AtomicBool>,
    validate_err: Option<String>,
    deps: Vec<String>,
    address: Option<String>,
    classes: Option<(String, String)>,
    /// Every lifecycle call, in order, appended to a shared log so a
    /// test can assert stop ordering across services.
    calls: Arc<Mutex<Vec<String>>>,
}

impl Fake {
    /// The well-behaved default: reports ready, then parks until
    /// cancelled, then returns cleanly.
    fn new(name: &str) -> Self {
        Fake {
            name: name.to_string(),
            start_fn: Box::new(|cancel, ready| {
                Box::pin(async move {
                    ready.report();
                    cancel.cancelled().await;
                    Ok(())
                })
            }),
            stop_fn: Box::new(|_| Box::pin(async { Ok(()) })),
            is_ready: Arc::new(AtomicBool::new(false)),
            validate_err: None,
            deps: Vec::new(),
            address: None,
            classes: None,
            calls: Arc::new(Mutex::new(Vec::new())),
        }
    }

    fn with_start(mut self, f: StartFn) -> Self {
        self.start_fn = f;
        self
    }

    fn with_stop(mut self, f: StopFn) -> Self {
        self.stop_fn = f;
        self
    }

    fn with_validate_err(mut self, msg: &str) -> Self {
        self.validate_err = Some(msg.to_string());
        self
    }

    fn with_deps(mut self, deps: &[&str]) -> Self {
        self.deps = deps.iter().map(|s| s.to_string()).collect();
        self
    }

    fn with_addr(mut self, addr: &str) -> Self {
        self.address = Some(addr.to_string());
        self
    }

    fn with_class(mut self, side_effect: &str, network: &str) -> Self {
        self.classes = Some((side_effect.to_string(), network.to_string()));
        self
    }

    fn with_calls(mut self, calls: Arc<Mutex<Vec<String>>>) -> Self {
        self.calls = calls;
        self
    }

    fn ready_flag(&self) -> Arc<AtomicBool> {
        self.is_ready.clone()
    }
}

impl Service for Fake {
    fn name(&self) -> &str {
        &self.name
    }

    fn start<'a>(
        &'a self,
        cancel: CancelToken,
        ready: ReadySignal,
    ) -> ServeFuture<'a, Result<(), String>> {
        self.calls
            .lock()
            .unwrap()
            .push(format!("start:{}", self.name));
        let flag = self.is_ready.clone();
        let inner = (self.start_fn)(cancel, ready);
        Box::pin(async move {
            flag.store(true, Ordering::SeqCst);
            let r = inner.await;
            flag.store(false, Ordering::SeqCst);
            r
        })
    }

    fn ready(&self) -> bool {
        self.is_ready.load(Ordering::SeqCst)
    }

    fn stop<'a>(&'a self, cancel: CancelToken) -> ServeFuture<'a, Result<(), String>> {
        self.calls
            .lock()
            .unwrap()
            .push(format!("stop:{}", self.name));
        (self.stop_fn)(cancel)
    }

    fn validate(&self) -> Option<String> {
        self.validate_err.clone()
    }

    fn depends_on(&self) -> Vec<String> {
        self.deps.clone()
    }

    fn addr(&self) -> Option<String> {
        self.address.clone()
    }

    fn class(&self) -> Option<(String, String)> {
        self.classes.clone()
    }
}

/// One published event, flattened for assertion.
#[derive(Clone, Debug, PartialEq, Eq)]
struct Recorded {
    topic: String,
    source: String,
    service: Option<String>,
    error: Option<String>,
    address: Option<String>,
    reason: Option<String>,
}

#[derive(Default)]
struct RecordingPublisher {
    events: Mutex<Vec<Recorded>>,
    /// Number of publishes to fail before succeeding. Exercises the
    /// "a publish failure never fails the lifecycle" rule.
    panics: AtomicUsize,
}

impl RecordingPublisher {
    fn events(&self) -> Vec<Recorded> {
        self.events.lock().unwrap().clone()
    }

    fn topics(&self) -> Vec<String> {
        self.events().into_iter().map(|e| e.topic).collect()
    }

    fn find(&self, topic: &str) -> Vec<Recorded> {
        self.events()
            .into_iter()
            .filter(|e| e.topic == topic)
            .collect()
    }
}

impl Publisher for RecordingPublisher {
    fn publish(&self, topic: &str, source: &str, payload: &EventPayload) {
        if self.panics.load(Ordering::SeqCst) > 0 {
            self.panics.fetch_sub(1, Ordering::SeqCst);
            panic!("publisher exploded");
        }
        self.events.lock().unwrap().push(Recorded {
            topic: topic.to_string(),
            source: source.to_string(),
            service: payload.service.clone(),
            error: payload.error.clone(),
            address: payload.address.clone(),
            reason: payload.reason.clone(),
        });
    }
}

/// One captured log line: level, message, and its structured fields.
type LogLine = (String, String, Vec<(String, String)>);

#[derive(Default)]
struct RecordingLogger {
    lines: Mutex<Vec<LogLine>>,
}

impl RecordingLogger {
    fn lines(&self) -> Vec<LogLine> {
        self.lines.lock().unwrap().clone()
    }

    fn at(&self, level: &str) -> Vec<(String, Vec<(String, String)>)> {
        self.lines()
            .into_iter()
            .filter(|(l, _, _)| l == level)
            .map(|(_, m, kv)| (m, kv))
            .collect()
    }
}

impl ServeLogger for RecordingLogger {
    fn info(&self, msg: &str, keyvals: &[(&str, String)]) {
        self.lines.lock().unwrap().push((
            "info".to_string(),
            msg.to_string(),
            keyvals
                .iter()
                .map(|(k, v)| (k.to_string(), v.clone()))
                .collect(),
        ));
    }

    fn error(&self, msg: &str, keyvals: &[(&str, String)]) {
        self.lines.lock().unwrap().push((
            "error".to_string(),
            msg.to_string(),
            keyvals
                .iter()
                .map(|(k, v)| (k.to_string(), v.clone()))
                .collect(),
        ));
    }
}

/// A gate that denies exactly one side-effect class.
struct DenyGate(&'static str);

impl PolicyGate for DenyGate {
    fn allow(&self, side_effect: &str, _network: &str) -> Verdict {
        if side_effect == self.0 {
            Verdict::deny("class not permitted")
        } else {
            Verdict::allow()
        }
    }
}

fn registry_of(services: Vec<Arc<dyn Service>>) -> ServiceRegistry {
    let mut r = ServiceRegistry::new();
    for s in services {
        r.register(s).expect("test wiring");
    }
    r
}

fn configs(entries: &[(&str, ServiceConfig)]) -> ServiceConfigs {
    entries
        .iter()
        .map(|(n, c)| ((*n).to_string(), c.clone()))
        .collect()
}

/// Every supervised run in this suite is bounded.
///
/// A `#[tokio::test]` has no per-test timeout, so a defect that makes
/// `run` block forever would hang the whole suite instead of failing
/// one assertion — which is exactly what a mutation run does to a
/// naive test. Bounding here turns "blocks forever" into a named
/// failure. The budget is far larger than any timing this suite uses,
/// so it never competes with a deliberate short timeout in a test.
async fn bounded(fut: impl std::future::Future<Output = RunResult>) -> RunResult {
    match tokio::time::timeout(Duration::from_secs(20), fut).await {
        Ok(res) => res,
        Err(_) => panic!("supervised run did not finish within its bound"),
    }
}

fn args(v: &[&str]) -> Vec<String> {
    v.iter().map(|s| (*s).to_string()).collect()
}

// ---------------------------------------------------------------------------
// The name grammar and the reserved set
// ---------------------------------------------------------------------------

#[test]
fn accepts_the_contract_name_grammar() {
    for ok in ["api", "a", "web-ui", "svc2", "a-b-c", "x9"] {
        assert_eq!(validate_name(ok), None, "{ok} should be valid");
    }
}

#[test]
fn rejects_anything_outside_the_name_grammar() {
    for bad in ["", "API", "1api", "-api", "api_x", "api.x", "ap i", "api!"] {
        assert!(validate_name(bad).is_some(), "{bad:?} should be refused");
    }
}

#[test]
fn reserves_exactly_all_none_and_list() {
    assert_eq!(RESERVED_NAMES, ["all", "list", "none"]);
    for r in RESERVED_NAMES {
        assert!(is_reserved_name(r));
        assert!(
            validate_name(r).is_some(),
            "reserved {r:?} must not validate as a service name"
        );
    }
    assert!(!is_reserved_name("api"));
}

// ---------------------------------------------------------------------------
// The registration seam
// ---------------------------------------------------------------------------

#[test]
fn lists_in_registration_order() {
    let r = registry_of(vec![
        Arc::new(Fake::new("zulu")),
        Arc::new(Fake::new("alpha")),
        Arc::new(Fake::new("mike")),
    ]);
    assert_eq!(r.names(), ["zulu", "alpha", "mike"]);
}

#[test]
fn refuses_a_duplicate_name_at_registration() {
    let mut r = ServiceRegistry::new();
    r.register(Arc::new(Fake::new("api"))).expect("first");
    let err = r
        .register(Arc::new(Fake::new("api")))
        .expect_err("duplicate must be refused, not last-writer-wins");
    assert!(err.0.contains("already registered"), "{}", err.0);
    assert!(err.0.contains("override"), "must name the escape hatch");
    // The first registration is still the one that stands.
    assert_eq!(r.names(), ["api"]);
    assert_eq!(r.len(), 1);
}

#[test]
fn refuses_an_invalid_or_reserved_name_at_registration() {
    let mut r = ServiceRegistry::new();
    assert!(r.register(Arc::new(Fake::new("API"))).is_err());
    assert!(r.register(Arc::new(Fake::new("list"))).is_err());
    assert!(r.is_empty());
}

#[test]
fn override_replaces_in_place_and_keeps_position() {
    let mut r = registry_of(vec![
        Arc::new(Fake::new("a")),
        Arc::new(Fake::new("b")),
        Arc::new(Fake::new("c")),
    ]);
    let replacement = Arc::new(Fake::new("b").with_addr("replaced:1"));
    r.override_service(replacement).expect("override");
    assert_eq!(r.names(), ["a", "b", "c"], "position must be preserved");
    assert_eq!(
        r.lookup("b").unwrap().addr().as_deref(),
        Some("replaced:1"),
        "the replacement must be the one that stands"
    );
    assert_eq!(r.len(), 3);
}

#[test]
fn override_still_refuses_an_invalid_name() {
    let mut r = ServiceRegistry::new();
    assert!(r.override_service(Arc::new(Fake::new("all"))).is_err());
    assert!(r.override_service(Arc::new(Fake::new("Nope"))).is_err());
}

#[test]
fn override_registers_a_name_that_was_not_there() {
    let mut r = registry_of(vec![Arc::new(Fake::new("a"))]);
    r.override_service(Arc::new(Fake::new("b"))).expect("new");
    assert_eq!(r.names(), ["a", "b"]);
}

// ---------------------------------------------------------------------------
// resolve — the hierarchy
// ---------------------------------------------------------------------------

#[test]
fn supervisor_form_selects_every_configured_and_enabled_service() {
    let r = registry_of(vec![
        Arc::new(Fake::new("api")),
        Arc::new(Fake::new("worker")),
    ]);
    let cfg = configs(&[
        ("api", ServiceConfig::enabled()),
        ("worker", ServiceConfig::enabled()),
    ]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: Vec::new(),
            configs: Some(&cfg),
            policy: None,
        },
    );
    assert_eq!(out.selected, ["api", "worker"]);
    assert!(!out.explicit);
    assert!(out.error.is_none());
}

#[test]
fn skips_a_disabled_service_silently_rather_than_failing() {
    let r = registry_of(vec![
        Arc::new(Fake::new("api")),
        Arc::new(Fake::new("worker")),
    ]);
    let cfg = configs(&[
        ("api", ServiceConfig::enabled()),
        ("worker", ServiceConfig::disabled()),
    ]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: Vec::new(),
            configs: Some(&cfg),
            policy: None,
        },
    );
    assert_eq!(out.selected, ["api"]);
    assert_eq!(out.skipped, ["worker"]);
    assert!(
        out.error.is_none(),
        "a skipped service must not affect the exit code"
    );
}

#[test]
fn ignores_a_service_with_no_config_block_at_all() {
    let r = registry_of(vec![
        Arc::new(Fake::new("api")),
        Arc::new(Fake::new("worker")),
    ]);
    let cfg = configs(&[("api", ServiceConfig::enabled())]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: Vec::new(),
            configs: Some(&cfg),
            policy: None,
        },
    );
    assert_eq!(out.selected, ["api"]);
    assert!(
        out.skipped.is_empty(),
        "unconfigured is not the same as configured-and-disabled"
    );
}

#[test]
fn selection_preserves_registration_order_not_argument_order() {
    let r = registry_of(vec![
        Arc::new(Fake::new("zulu")),
        Arc::new(Fake::new("alpha")),
    ]);
    let cfg = configs(&[
        ("alpha", ServiceConfig::enabled()),
        ("zulu", ServiceConfig::enabled()),
    ]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: Vec::new(),
            configs: Some(&cfg),
            policy: None,
        },
    );
    assert_eq!(out.selected, ["zulu", "alpha"]);
}

// ---------------------------------------------------------------------------
// resolve — the override rule
// ---------------------------------------------------------------------------

#[test]
fn starts_a_disabled_service_when_it_is_named_explicitly() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let cfg = configs(&[("api", ServiceConfig::disabled())]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["api"]),
            configs: Some(&cfg),
            policy: None,
        },
    );
    assert_eq!(out.selected, ["api"]);
    assert!(out.explicit);
    assert!(out.error.is_none());
}

#[test]
fn starts_a_service_with_no_config_block_at_all_when_named() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["api"]),
            configs: None,
            policy: None,
        },
    );
    assert_eq!(out.selected, ["api"]);
    assert!(out.error.is_none());
}

#[test]
fn the_same_disabled_service_is_refused_under_the_supervisor_form() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let cfg = configs(&[("api", ServiceConfig::disabled())]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: Vec::new(),
            configs: Some(&cfg),
            policy: None,
        },
    );
    assert!(out.selected.is_empty());
    assert_eq!(out.outcome, Some(LifecycleOutcome::NoServices));
    assert_eq!(out.error.as_ref().unwrap().exit_code, 2);
}

#[test]
fn the_override_rule_does_not_override_the_configuration_gate() {
    let r = registry_of(vec![Arc::new(
        Fake::new("api").with_validate_err("missing addr"),
    )]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["api"]),
            configs: None,
            policy: None,
        },
    );
    assert_eq!(out.outcome, Some(LifecycleOutcome::ConfigInvalid));
    assert_eq!(out.error.as_ref().unwrap().exit_code, 2);
    assert!(out.selected.is_empty());
}

#[test]
fn the_override_rule_does_not_override_the_policy_gate() {
    let r = registry_of(vec![Arc::new(
        Fake::new("api").with_class("write", "outbound"),
    )]);
    let gate = DenyGate("write");
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["api"]),
            configs: None,
            policy: Some(&gate),
        },
    );
    assert_eq!(out.outcome, Some(LifecycleOutcome::PolicyDenied));
    assert_eq!(out.error.as_ref().unwrap().exit_code, 5);
    assert_eq!(out.error.as_ref().unwrap().code, "UNAUTHORIZED");
}

#[test]
fn evaluates_the_gates_in_order_registration_config_policy() {
    // A service that is unregistered, would fail config, and would fail
    // policy must be refused as unregistered — the first gate.
    let r = registry_of(vec![Arc::new(Fake::new("other"))]);
    let gate = DenyGate("write");
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["ghost"]),
            configs: None,
            policy: Some(&gate),
        },
    );
    assert_eq!(out.outcome, Some(LifecycleOutcome::UnknownService));

    // Registered but config-invalid AND policy-denied: config wins.
    let r2 = registry_of(vec![Arc::new(
        Fake::new("api")
            .with_validate_err("bad")
            .with_class("write", "outbound"),
    )]);
    let out2 = resolve(
        &r2,
        &ResolveRequest {
            args: args(&["api"]),
            configs: None,
            policy: Some(&gate),
        },
    );
    assert_eq!(out2.outcome, Some(LifecycleOutcome::ConfigInvalid));
}

#[test]
fn passes_an_unclassified_service_through_the_policy_gate() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let gate = DenyGate("write");
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["api"]),
            configs: None,
            policy: Some(&gate),
        },
    );
    assert_eq!(out.selected, ["api"]);
}

#[test]
fn passes_every_service_when_no_gate_is_wired() {
    let r = registry_of(vec![Arc::new(
        Fake::new("api").with_class("write", "outbound"),
    )]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["api"]),
            configs: None,
            policy: None,
        },
    );
    assert_eq!(out.selected, ["api"]);
}

#[test]
fn the_policy_gate_also_applies_under_the_supervisor_form() {
    let r = registry_of(vec![Arc::new(
        Fake::new("api").with_class("write", "outbound"),
    )]);
    let cfg = configs(&[("api", ServiceConfig::enabled())]);
    let gate = DenyGate("write");
    let out = resolve(
        &r,
        &ResolveRequest {
            args: Vec::new(),
            configs: Some(&cfg),
            policy: Some(&gate),
        },
    );
    assert_eq!(out.outcome, Some(LifecycleOutcome::PolicyDenied));
    assert_eq!(out.error.as_ref().unwrap().exit_code, 5);
}

// ---------------------------------------------------------------------------
// resolve — invalid selection
// ---------------------------------------------------------------------------

#[test]
fn refuses_two_or_more_positional_arguments_as_usage_2() {
    let r = registry_of(vec![
        Arc::new(Fake::new("api")),
        Arc::new(Fake::new("worker")),
    ]);
    for n in [2usize, 3] {
        let a: Vec<&str> = vec!["api", "worker", "extra"][..n].to_vec();
        let out = resolve(
            &r,
            &ResolveRequest {
                args: args(&a),
                configs: None,
                policy: None,
            },
        );
        assert_eq!(out.outcome, Some(LifecycleOutcome::InvalidSelection));
        let err = out.error.as_ref().unwrap();
        assert_eq!(err.code, "USAGE");
        assert_eq!(err.exit_code, 2);
    }
}

#[test]
fn refuses_an_unknown_service_as_not_found_3_naming_the_known_set() {
    let r = registry_of(vec![
        Arc::new(Fake::new("api")),
        Arc::new(Fake::new("worker")),
    ]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["ghost"]),
            configs: None,
            policy: None,
        },
    );
    assert_eq!(out.outcome, Some(LifecycleOutcome::UnknownService));
    let err = out.error.as_ref().unwrap();
    assert_eq!(err.code, "NOT_FOUND");
    assert_eq!(err.exit_code, 3);
    assert!(err.message.contains("api"), "{}", err.message);
    assert!(err.message.contains("worker"), "{}", err.message);
}

#[test]
fn suggests_the_nearest_name_on_a_near_miss() {
    let r = registry_of(vec![Arc::new(Fake::new("worker"))]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: args(&["workr"]),
            configs: None,
            policy: None,
        },
    );
    assert!(out.error.as_ref().unwrap().suggested_fix.contains("worker"));
}

#[test]
fn refuses_a_reserved_word_as_a_selection() {
    // `list` can never be registered, so selecting it is NOT_FOUND —
    // which is exactly why `--list` is a flag and not a `serve list`
    // child.
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    for word in RESERVED_NAMES {
        let out = resolve(
            &r,
            &ResolveRequest {
                args: args(&[word]),
                configs: None,
                policy: None,
            },
        );
        assert_eq!(
            out.outcome,
            Some(LifecycleOutcome::UnknownService),
            "{word}"
        );
        assert_eq!(out.error.as_ref().unwrap().exit_code, 3);
    }
}

#[test]
fn refuses_zero_resolved_services_under_the_supervisor_form() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let out = resolve(
        &r,
        &ResolveRequest {
            args: Vec::new(),
            configs: None,
            policy: None,
        },
    );
    assert_eq!(out.outcome, Some(LifecycleOutcome::NoServices));
    let err = out.error.as_ref().unwrap();
    assert_eq!(err.code, "USAGE");
    assert_eq!(err.exit_code, 2, "must be 2, never a clean 0");
    assert!(!err.suggested_fix.is_empty());
}

#[test]
fn refuses_an_empty_registry_under_the_supervisor_form() {
    let out = resolve(
        &ServiceRegistry::new(),
        &ResolveRequest {
            args: Vec::new(),
            configs: None,
            policy: None,
        },
    );
    assert_eq!(out.outcome, Some(LifecycleOutcome::NoServices));
    assert_eq!(out.error.as_ref().unwrap().exit_code, 2);
}

// ---------------------------------------------------------------------------
// The exit-code taxonomy
// ---------------------------------------------------------------------------

#[test]
fn maps_every_outcome_onto_the_contract_table() {
    use LifecycleOutcome::*;
    let table: &[(LifecycleOutcome, &str, i32)] = &[
        (CleanStop, "OK", 0),
        (InvalidSelection, "USAGE", 2),
        (ConfigInvalid, "USAGE", 2),
        (NoServices, "USAGE", 2),
        (UnknownService, "NOT_FOUND", 3),
        (PolicyDenied, "UNAUTHORIZED", 5),
        (StartFailed, "GENERIC", 1),
        (RuntimeCrash, "GENERIC", 1),
        (ShutdownTimeout, "GENERIC", 1),
    ];
    for (outcome, code, exit) in table {
        assert_eq!(outcome.code(), *code, "{outcome:?}");
        assert_eq!(outcome.exit_code(), *exit, "{outcome:?}");
    }
}

#[test]
fn treats_a_clean_stop_as_success_and_everything_else_as_failure() {
    use LifecycleOutcome::*;
    assert!(!CleanStop.is_failure());
    for o in [
        InvalidSelection,
        ConfigInvalid,
        NoServices,
        UnknownService,
        PolicyDenied,
        StartFailed,
        RuntimeCrash,
        ShutdownTimeout,
    ] {
        assert!(o.is_failure(), "{o:?}");
    }
}

#[test]
fn worst_outcome_keeps_the_first_failure_across_a_whole_run() {
    use hop_top_kit::serve::worst_outcome;
    use LifecycleOutcome::*;
    assert_eq!(worst_outcome(&[]), CleanStop);
    assert_eq!(worst_outcome(&[CleanStop, CleanStop]), CleanStop);
    assert_eq!(worst_outcome(&[RuntimeCrash, StartFailed]), RuntimeCrash);
    assert_eq!(
        worst_outcome(&[CleanStop, StartFailed, RuntimeCrash]),
        StartFailed
    );
}

#[test]
fn a_serve_failure_wrapping_a_transient_error_keeps_exit_6() {
    use hop_top_kit::serve::failure_error;
    let mut failed = HashMap::new();
    failed.insert("api".to_string(), "TRANSIENT: upstream 503".to_string());
    let err = failure_error(LifecycleOutcome::RuntimeCrash, &failed);
    assert_eq!(err.code, "TRANSIENT");
    assert_eq!(err.exit_code, 6);
    assert_eq!(err.transience, "transient");
}

#[test]
fn one_permanent_failure_makes_the_whole_run_permanent() {
    use hop_top_kit::serve::failure_error;
    let mut failed = HashMap::new();
    failed.insert("api".to_string(), "TRANSIENT: upstream 503".to_string());
    failed.insert("worker".to_string(), "bind: address in use".to_string());
    let err = failure_error(LifecycleOutcome::RuntimeCrash, &failed);
    assert_eq!(err.code, "GENERIC");
    assert_eq!(err.exit_code, 1);
}

// ---------------------------------------------------------------------------
// start_order
// ---------------------------------------------------------------------------

#[test]
fn start_order_is_registration_order_with_no_declarations() {
    let r = registry_of(vec![
        Arc::new(Fake::new("a")),
        Arc::new(Fake::new("b")),
        Arc::new(Fake::new("c")),
    ]);
    let sel = args(&["a", "b", "c"]);
    assert_eq!(start_order(&r, &sel).unwrap(), ["a", "b", "c"]);
}

#[test]
fn start_order_puts_a_dependency_before_its_dependent() {
    let r = registry_of(vec![
        Arc::new(Fake::new("web").with_deps(&["db"])),
        Arc::new(Fake::new("db")),
    ]);
    let sel = args(&["web", "db"]);
    assert_eq!(start_order(&r, &sel).unwrap(), ["db", "web"]);
}

#[test]
fn start_order_ignores_a_dependency_outside_the_selected_set() {
    let r = registry_of(vec![
        Arc::new(Fake::new("web").with_deps(&["db"])),
        Arc::new(Fake::new("db")),
    ]);
    let sel = args(&["web"]);
    assert_eq!(start_order(&r, &sel).unwrap(), ["web"]);
}

#[test]
fn start_order_refuses_a_dependency_cycle() {
    let r = registry_of(vec![
        Arc::new(Fake::new("a").with_deps(&["b"])),
        Arc::new(Fake::new("b").with_deps(&["a"])),
    ]);
    let sel = args(&["a", "b"]);
    let err = start_order(&r, &sel).expect_err("a cycle has no right order");
    assert!(err.0.contains("cycle"), "{}", err.0);
}

// ---------------------------------------------------------------------------
// Lifecycle topics
// ---------------------------------------------------------------------------

#[test]
fn produces_exactly_the_six_contract_topic_strings() {
    let topics = default_topics(DEFAULT_TOPIC_PREFIX);
    let mut got: Vec<&String> = topics.values().collect();
    got.sort();
    let mut want = [
        "kit.serve.service.failed".to_string(),
        "kit.serve.service.ready_reported".to_string(),
        "kit.serve.service.started".to_string(),
        "kit.serve.service.stopped".to_string(),
        "kit.serve.supervisor.ready_reported".to_string(),
        "kit.serve.supervisor.stopped".to_string(),
    ];
    want.sort();
    let want_refs: Vec<&String> = want.iter().collect();
    assert_eq!(got, want_refs);
    assert_eq!(topics.len(), 6);
}

#[test]
fn never_emits_a_bare_ready_action() {
    for topic in default_topics(DEFAULT_TOPIC_PREFIX).values() {
        assert!(
            !topic.ends_with(".ready"),
            "{topic} would fail the bus past-tense validator"
        );
    }
}

#[test]
fn every_serve_topic_passes_the_bus_published_topic_validator() {
    #[cfg(feature = "bus")]
    {
        use hop_top_kit::bus::{validate, validate_topic, Topic};
        for topic in default_topics(DEFAULT_TOPIC_PREFIX).values() {
            let t = Topic(topic.clone());
            validate(&t).unwrap_or_else(|e| panic!("{topic}: {e}"));
            validate_topic(&t).unwrap_or_else(|e| panic!("{topic}: {e}"));
        }
    }
}

#[test]
fn rebrands_the_prefix_but_falls_back_on_an_empty_one() {
    let t = default_topics("acme.serve");
    assert_eq!(t["service.started"], "acme.serve.service.started");
    let fallback = default_topics("");
    assert_eq!(fallback["service.started"], "kit.serve.service.started");
}

// ---------------------------------------------------------------------------
// Configuration defaults
// ---------------------------------------------------------------------------

#[test]
fn configuration_defaults_match_the_contract_table() {
    let c = ServiceConfig::default();
    assert!(!c.enabled, "enabled defaults to false");
    assert_eq!(c.ready_timeout, Duration::from_secs(30));
    assert_eq!(c.stop_timeout, Duration::from_secs(30));
    assert_eq!(DEFAULT_READY_TIMEOUT, Duration::from_secs(30));
    assert_eq!(DEFAULT_STOP_TIMEOUT, Duration::from_secs(30));
    assert_eq!(DEFAULT_SHUTDOWN_TIMEOUT, Duration::from_secs(60));

    let s = SupervisorConfig::default();
    assert_eq!(s.failure_policy, FailurePolicy::FailFast);
    assert_eq!(s.shutdown_timeout, Duration::from_secs(60));
}

#[test]
fn failure_policy_has_exactly_the_two_contract_values() {
    assert_eq!(FailurePolicy::FailFast.as_str(), "fail-fast");
    assert_eq!(FailurePolicy::Isolate.as_str(), "isolate");
    assert_eq!(
        FailurePolicy::parse("fail-fast"),
        Some(FailurePolicy::FailFast)
    );
    assert_eq!(
        FailurePolicy::parse("isolate"),
        Some(FailurePolicy::Isolate)
    );
    assert_eq!(FailurePolicy::parse("failfast"), None);
    assert_eq!(FailurePolicy::parse(""), None);
}

#[test]
fn the_services_config_key_names_are_the_contract_spelling() {
    use hop_top_kit::serve::{
        KEY_ENABLED, KEY_FAILURE_POLICY, KEY_READY_TIMEOUT, KEY_SHUTDOWN_TIMEOUT, KEY_STOP_TIMEOUT,
    };
    assert_eq!(KEY_ENABLED, "enabled");
    assert_eq!(KEY_READY_TIMEOUT, "ready_timeout");
    assert_eq!(KEY_STOP_TIMEOUT, "stop_timeout");
    assert_eq!(KEY_FAILURE_POLICY, "failure_policy");
    assert_eq!(KEY_SHUTDOWN_TIMEOUT, "shutdown_timeout");
}

// ---------------------------------------------------------------------------
// The --list inspection rows
// ---------------------------------------------------------------------------

#[test]
fn list_services_reports_state_in_registration_order() {
    let r = registry_of(vec![
        Arc::new(Fake::new("zulu")),
        Arc::new(Fake::new("alpha")),
        Arc::new(Fake::new("mike")),
    ]);
    let cfg = configs(&[
        ("zulu", ServiceConfig::enabled()),
        ("alpha", ServiceConfig::disabled()),
    ]);
    let rows = list_services(&r, Some(&cfg));
    assert_eq!(
        rows.iter().map(|r| r.name.as_str()).collect::<Vec<_>>(),
        ["zulu", "alpha", "mike"]
    );
    assert!(rows[0].configured && rows[0].enabled);
    assert!(rows[1].configured && !rows[1].enabled);
    assert!(!rows[2].configured && !rows[2].enabled);
    assert!(rows.iter().all(|r| !r.ready), "nothing is running");
}

// ---------------------------------------------------------------------------
// Supervisor
// ---------------------------------------------------------------------------

fn supervisor(r: ServiceRegistry, pubr: Arc<RecordingPublisher>) -> Supervisor {
    Supervisor::new(
        r,
        SupervisorOptions {
            publisher: Some(pubr),
            ..SupervisorOptions::default()
        },
    )
}

#[tokio::test]
async fn starts_reports_ready_and_stops_cleanly_on_a_signal() {
    let calls = Arc::new(Mutex::new(Vec::new()));
    let r = registry_of(vec![Arc::new(Fake::new("api").with_calls(calls.clone()))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cancel = CancelToken::new();

    let c = cancel.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(30)).await;
        c.cancel();
    });

    let res = bounded(sup.run(
        cancel,
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;

    assert_eq!(res.outcome, LifecycleOutcome::CleanStop);
    assert_eq!(res.exit_code, 0, "a signal-initiated stop exits 0");
    assert!(res.error.is_none());
    assert_eq!(res.started, ["api"]);
    assert_eq!(res.ready, ["api"]);
    assert!(res.failed.is_empty());
    assert_eq!(*calls.lock().unwrap(), ["start:api", "stop:api"]);

    let topics = pubr.topics();
    assert!(topics.contains(&"kit.serve.service.started".to_string()));
    assert!(topics.contains(&"kit.serve.service.ready_reported".to_string()));
    assert!(topics.contains(&"kit.serve.supervisor.ready_reported".to_string()));
    assert!(topics.contains(&"kit.serve.service.stopped".to_string()));
    assert!(topics.contains(&"kit.serve.supervisor.stopped".to_string()));
    assert!(
        !topics.contains(&"kit.serve.service.failed".to_string()),
        "a clean run must not report a failure"
    );
}

/// The trap the TS port found, in its Rust shape: an implementation
/// that resolved as soon as the last service was spawned would return
/// `CleanStop`/0 having served for zero time. This pins that `run`
/// actually *stays up* until something ends it.
#[tokio::test]
async fn a_started_supervisor_stays_up_serving_rather_than_returning() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cancel = CancelToken::new();

    let cfg = configs(&[("api", ServiceConfig::enabled())]);
    let sel = args(&["api"]);
    let mut running = Box::pin(sup.run(cancel.clone(), &sel, &cfg));

    // Give the runtime ample opportunity to drive the future to
    // completion. A naive `run` that never awaited the services would
    // be finished by now.
    for _ in 0..50 {
        tokio::task::yield_now().await;
    }
    let quick = tokio::time::timeout(Duration::from_millis(200), &mut running).await;
    assert!(
        quick.is_err(),
        "run must still be serving; a run that returns without a signal \
         would exit 0 without ever having served"
    );

    // And it must end promptly once cancelled, not hang.
    cancel.cancel();
    let res = tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("run must return once cancelled");
    assert_eq!(res.outcome, LifecycleOutcome::CleanStop);
    assert_eq!(res.exit_code, 0);
}

#[tokio::test]
async fn the_service_stays_ready_for_the_whole_time_it_serves() {
    let fake = Fake::new("api");
    let flag = fake.ready_flag();
    let r = registry_of(vec![Arc::new(fake)]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cancel = CancelToken::new();
    let cfg = configs(&[("api", ServiceConfig::enabled())]);
    let sel = args(&["api"]);

    let mut running = Box::pin(sup.run(cancel.clone(), &sel, &cfg));
    let _ = tokio::time::timeout(Duration::from_millis(80), &mut running).await;
    assert!(
        flag.load(Ordering::SeqCst),
        "the service must actually be up while the supervisor serves"
    );
    cancel.cancel();
    let res = running.await;
    assert_eq!(res.exit_code, 0);
    assert!(!flag.load(Ordering::SeqCst), "and down once it has stopped");
}

#[tokio::test]
async fn carries_the_resolved_address_on_ready_reported_and_nowhere_else() {
    let r = registry_of(vec![Arc::new(
        Fake::new("api").with_addr("127.0.0.1:54321"),
    )]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cancel = CancelToken::new();
    cancel.cancel();
    let _ = bounded(sup.run(
        cancel,
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;

    let ready = pubr.find("kit.serve.service.ready_reported");
    assert_eq!(ready.len(), 1);
    assert_eq!(ready[0].address.as_deref(), Some("127.0.0.1:54321"));
    for e in pubr.events() {
        if !e.topic.ends_with(".ready_reported") {
            assert!(e.address.is_none(), "{} carried an address", e.topic);
        }
    }
}

#[tokio::test]
async fn carries_the_service_identifier_in_the_payload_never_in_the_topic() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cancel = CancelToken::new();
    cancel.cancel();
    let _ = bounded(sup.run(
        cancel,
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;

    // Compared against literals, not against `default_topics`: asserting
    // a published topic is "one of what default_topics returns" is
    // self-referential — a defect in the topic builder moves both sides
    // together and the test stays green. The six strings are contract,
    // so spelling them out here is the point.
    const CONTRACT: [&str; 6] = [
        "kit.serve.service.started",
        "kit.serve.service.ready_reported",
        "kit.serve.service.failed",
        "kit.serve.service.stopped",
        "kit.serve.supervisor.ready_reported",
        "kit.serve.supervisor.stopped",
    ];
    for e in pubr.events() {
        assert!(
            CONTRACT.contains(&e.topic.as_str()),
            "{} is not one of the six contract topics",
            e.topic
        );
        if e.topic.starts_with("kit.serve.service.") {
            assert_eq!(e.service.as_deref(), Some("api"), "{}", e.topic);
        }
        assert_eq!(e.source, "kit.serve");
    }
}

#[tokio::test]
async fn reports_the_aggregate_ready_only_when_every_service_is_ready() {
    // `slow` never reports ready, so its readiness budget lapses and no
    // aggregate ready is published.
    let r = registry_of(vec![
        Arc::new(Fake::new("fast")),
        Arc::new(Fake::new("slow").with_start(Box::new(|cancel, _ready| {
            Box::pin(async move {
                cancel.cancelled().await;
                Ok(())
            })
        }))),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cfg = configs(&[
        ("fast", ServiceConfig::enabled()),
        (
            "slow",
            ServiceConfig::enabled().with_ready_timeout(Duration::from_millis(60)),
        ),
    ]);
    let res = bounded(sup.run(CancelToken::new(), &args(&["fast", "slow"]), &cfg)).await;

    assert_eq!(res.ready, ["fast"]);
    assert_eq!(res.outcome, LifecycleOutcome::StartFailed);
    assert!(
        pubr.find("kit.serve.supervisor.ready_reported").is_empty(),
        "aggregate ready must wait for every started service"
    );
}

/// The aggregate event is published only when every started service is
/// ready — asserted as an invariant over the whole run.
///
/// Note on coverage: the count guard inside `emit_aggregate_ready` is
/// currently *unreachable defensively*. A service that does not report
/// ready inside its budget is a start failure, and a start failure
/// short-circuits the start sequence before the aggregate is ever
/// considered — so by construction `ready == started` at that point.
/// Removing the guard therefore does not change observable behavior
/// today, and no test can prove otherwise without asserting on
/// internals. It is kept because it is the invariant the contract
/// states ("the aggregate is ready when every started service is
/// ready"), and because a future concurrent-start path would reach it
/// for real. This test pins the observable half.
#[tokio::test]
async fn publishes_the_aggregate_ready_only_alongside_a_full_ready_set() {
    let r = registry_of(vec![
        Arc::new(Fake::new("first")),
        Arc::new(Fake::new("second")),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cfg = configs(&[
        ("first", ServiceConfig::enabled()),
        ("second", ServiceConfig::enabled()),
    ]);
    let cancel = CancelToken::new();
    cancel.cancel();
    let res = bounded(sup.run(cancel, &args(&["first", "second"]), &cfg)).await;

    assert_eq!(res.started, ["first", "second"]);
    assert_eq!(
        pubr.find("kit.serve.supervisor.ready_reported").len(),
        1,
        "every started service was ready, so the aggregate is published once"
    );
    assert_eq!(
        res.ready.len(),
        res.started.len(),
        "the aggregate must never be published while a started service is not ready"
    );
}

#[tokio::test]
async fn stops_in_the_exact_reverse_of_start_order() {
    let calls = Arc::new(Mutex::new(Vec::new()));
    let r = registry_of(vec![
        Arc::new(Fake::new("a").with_calls(calls.clone())),
        Arc::new(Fake::new("b").with_calls(calls.clone())),
        Arc::new(Fake::new("c").with_calls(calls.clone())),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cancel = CancelToken::new();
    cancel.cancel();
    let cfg = configs(&[
        ("a", ServiceConfig::enabled()),
        ("b", ServiceConfig::enabled()),
        ("c", ServiceConfig::enabled()),
    ]);
    let res = bounded(sup.run(cancel, &args(&["a", "b", "c"]), &cfg)).await;

    assert_eq!(res.started, ["a", "b", "c"]);
    let got = calls.lock().unwrap().clone();
    let stops: Vec<&String> = got.iter().filter(|c| c.starts_with("stop:")).collect();
    assert_eq!(stops, ["stop:c", "stop:b", "stop:a"]);
}

#[tokio::test]
async fn treats_a_start_failure_as_start_failed_at_exit_1() {
    let r = registry_of(vec![Arc::new(Fake::new("api").with_start(Box::new(
        |_c, _r| Box::pin(async { Err("bind: address already in use".to_string()) }),
    )))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let res = bounded(sup.run(
        CancelToken::new(),
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;

    assert_eq!(res.outcome, LifecycleOutcome::StartFailed);
    assert_eq!(res.exit_code, 1);
    assert_eq!(res.error.as_ref().unwrap().code, "GENERIC");
    assert!(res.failed["api"].contains("address already in use"));
    assert!(res.ready.is_empty());

    let failed = pubr.find("kit.serve.service.failed");
    assert_eq!(failed.len(), 1);
    assert_eq!(failed[0].service.as_deref(), Some("api"));
    assert!(failed[0]
        .error
        .as_deref()
        .unwrap()
        .contains("address already in use"));
}

#[tokio::test]
async fn treats_a_readiness_timeout_as_a_start_failure() {
    let r = registry_of(vec![Arc::new(Fake::new("api").with_start(Box::new(
        |cancel, _ready| {
            Box::pin(async move {
                cancel.cancelled().await;
                Ok(())
            })
        },
    )))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cfg = configs(&[(
        "api",
        ServiceConfig::enabled().with_ready_timeout(Duration::from_millis(50)),
    )]);
    let res = bounded(sup.run(CancelToken::new(), &args(&["api"]), &cfg)).await;

    assert_eq!(res.outcome, LifecycleOutcome::StartFailed);
    assert_eq!(res.exit_code, 1);
    assert!(res.ready.is_empty());
    let failed = pubr.find("kit.serve.service.failed");
    assert_eq!(failed[0].reason.as_deref(), Some("ready_timeout"));
}

#[tokio::test]
async fn treats_a_service_returning_before_ready_as_a_start_failure() {
    // Returns Ok, but never reported ready: it was asked to serve and
    // it did not.
    let r = registry_of(vec![Arc::new(
        Fake::new("api").with_start(Box::new(|_c, _r| Box::pin(async { Ok(()) }))),
    )]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let res = bounded(sup.run(
        CancelToken::new(),
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;
    assert_eq!(res.outcome, LifecycleOutcome::StartFailed);
    assert_eq!(
        res.exit_code, 1,
        "returning Ok without serving is not success"
    );
}

#[tokio::test]
async fn does_not_start_a_later_service_after_an_earlier_one_fails() {
    let calls = Arc::new(Mutex::new(Vec::new()));
    let r = registry_of(vec![
        Arc::new(
            Fake::new("first")
                .with_calls(calls.clone())
                .with_start(Box::new(|_c, _r| Box::pin(async { Err("no".to_string()) }))),
        ),
        Arc::new(Fake::new("second").with_calls(calls.clone())),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cfg = configs(&[
        ("first", ServiceConfig::enabled()),
        ("second", ServiceConfig::enabled()),
    ]);
    let res = bounded(sup.run(CancelToken::new(), &args(&["first", "second"]), &cfg)).await;

    assert_eq!(res.started, ["first"]);
    let got = calls.lock().unwrap().clone();
    assert!(
        !got.iter().any(|c| c == "start:second"),
        "serial start must short-circuit: {got:?}"
    );
}

#[tokio::test]
async fn brings_everything_down_under_fail_fast_when_one_service_crashes() {
    let calls = Arc::new(Mutex::new(Vec::new()));
    let r = registry_of(vec![
        Arc::new(Fake::new("stable").with_calls(calls.clone())),
        Arc::new(
            Fake::new("flaky")
                .with_calls(calls.clone())
                .with_start(Box::new(|_c, ready| {
                    Box::pin(async move {
                        ready.report();
                        tokio::time::sleep(Duration::from_millis(30)).await;
                        Err("upstream reset".to_string())
                    })
                })),
        ),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = Supervisor::new(
        r,
        SupervisorOptions {
            config: SupervisorConfig {
                failure_policy: FailurePolicy::FailFast,
                ..SupervisorConfig::default()
            },
            publisher: Some(pubr.clone()),
            ..SupervisorOptions::default()
        },
    );
    let cfg = configs(&[
        ("stable", ServiceConfig::enabled()),
        ("flaky", ServiceConfig::enabled()),
    ]);
    let res = tokio::time::timeout(
        Duration::from_secs(5),
        sup.run(CancelToken::new(), &args(&["stable", "flaky"]), &cfg),
    )
    .await
    .expect("fail-fast must not hang");

    assert_eq!(res.outcome, LifecycleOutcome::RuntimeCrash);
    assert_eq!(res.exit_code, 1);
    assert_eq!(res.failed["flaky"], "upstream reset");
    let got = calls.lock().unwrap().clone();
    assert!(
        got.contains(&"stop:stable".to_string()),
        "fail-fast must bring the healthy service down too: {got:?}"
    );
}

#[tokio::test]
async fn keeps_the_rest_running_under_isolate_until_the_last_one_stops() {
    let r = registry_of(vec![
        Arc::new(Fake::new("stable")),
        Arc::new(Fake::new("flaky").with_start(Box::new(|_c, ready| {
            Box::pin(async move {
                ready.report();
                tokio::time::sleep(Duration::from_millis(20)).await;
                Err("crashed".to_string())
            })
        }))),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = Supervisor::new(
        r,
        SupervisorOptions {
            config: SupervisorConfig {
                failure_policy: FailurePolicy::Isolate,
                ..SupervisorConfig::default()
            },
            publisher: Some(pubr),
            ..SupervisorOptions::default()
        },
    );
    let cfg = configs(&[
        ("stable", ServiceConfig::enabled()),
        ("flaky", ServiceConfig::enabled()),
    ]);
    let cancel = CancelToken::new();
    let sel = args(&["stable", "flaky"]);
    let mut running = Box::pin(sup.run(cancel.clone(), &sel, &cfg));

    // The crash alone must not end the run under isolate.
    let early = tokio::time::timeout(Duration::from_millis(120), &mut running).await;
    assert!(early.is_err(), "isolate must keep the survivor running");

    cancel.cancel();
    let res = tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("must end once cancelled");
    assert_eq!(res.outcome, LifecycleOutcome::RuntimeCrash);
    assert_eq!(res.exit_code, 1, "the run still exits on the worst outcome");
    assert!(res.failed.contains_key("flaky"));
    assert!(!res.failed.contains_key("stable"));
}

#[tokio::test]
async fn abandons_a_stop_that_exceeds_its_budget_and_moves_on() {
    let calls = Arc::new(Mutex::new(Vec::new()));
    let r = registry_of(vec![
        Arc::new(Fake::new("quick").with_calls(calls.clone())),
        Arc::new(
            Fake::new("sticky")
                .with_calls(calls.clone())
                .with_stop(Box::new(|_c| {
                    Box::pin(async {
                        tokio::time::sleep(Duration::from_secs(30)).await;
                        Ok(())
                    })
                })),
        ),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cancel = CancelToken::new();
    cancel.cancel();
    let cfg = configs(&[
        ("quick", ServiceConfig::enabled()),
        (
            "sticky",
            ServiceConfig::enabled().with_stop_timeout(Duration::from_millis(50)),
        ),
    ]);
    let res = tokio::time::timeout(
        Duration::from_secs(5),
        sup.run(cancel, &args(&["quick", "sticky"]), &cfg),
    )
    .await
    .expect("one straggler must not block the whole shutdown");

    assert!(res.failed.contains_key("sticky"));
    let got = calls.lock().unwrap().clone();
    assert!(
        got.contains(&"stop:quick".to_string()),
        "the next service must still be stopped: {got:?}"
    );
    let failed = pubr.find("kit.serve.service.failed");
    assert!(failed
        .iter()
        .any(|e| e.reason.as_deref() == Some("stop_timeout")));
}

#[tokio::test]
async fn reports_a_stop_that_fails_as_a_failure() {
    let r = registry_of(vec![Arc::new(Fake::new("api").with_stop(Box::new(|_c| {
        Box::pin(async { Err("drain failed".to_string()) })
    })))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cancel = CancelToken::new();
    cancel.cancel();
    let res = bounded(sup.run(
        cancel,
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;
    assert_eq!(res.exit_code, 1);
    assert_eq!(res.failed["api"], "drain failed");
    assert!(pubr
        .find("kit.serve.service.failed")
        .iter()
        .any(|e| e.reason.as_deref() == Some("stop")));
}

#[tokio::test]
async fn abandons_the_drain_when_the_escalation_signal_fires() {
    let calls = Arc::new(Mutex::new(Vec::new()));
    let r = registry_of(vec![
        Arc::new(Fake::new("a").with_calls(calls.clone())),
        Arc::new(Fake::new("b").with_calls(calls.clone())),
    ]);
    let escalate = CancelToken::new();
    escalate.cancel();
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = Supervisor::new(
        r,
        SupervisorOptions {
            publisher: Some(pubr.clone()),
            escalate: Some(escalate),
            ..SupervisorOptions::default()
        },
    );
    let cancel = CancelToken::new();
    cancel.cancel();
    let cfg = configs(&[
        ("a", ServiceConfig::enabled()),
        ("b", ServiceConfig::enabled()),
    ]);
    let res = bounded(sup.run(cancel, &args(&["a", "b"]), &cfg)).await;

    assert_eq!(res.exit_code, 1, "escalation exits with the crash code");
    let got = calls.lock().unwrap().clone();
    assert!(
        !got.iter().any(|c| c.starts_with("stop:")),
        "the drain must be abandoned, not completed: {got:?}"
    );
    assert!(pubr
        .find("kit.serve.service.failed")
        .iter()
        .any(|e| e.reason.as_deref() == Some("escalated")));
}

#[tokio::test]
async fn refuses_an_empty_selection_rather_than_exiting_0() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let res = bounded(sup.run(CancelToken::new(), &[], &ServiceConfigs::new())).await;
    assert_eq!(res.outcome, LifecycleOutcome::NoServices);
    assert_eq!(res.exit_code, 2);
    assert!(res.started.is_empty());
}

#[tokio::test]
async fn refuses_a_dependency_cycle_at_run_rather_than_serving_a_wrong_order() {
    let r = registry_of(vec![
        Arc::new(Fake::new("a").with_deps(&["b"])),
        Arc::new(Fake::new("b").with_deps(&["a"])),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cfg = configs(&[
        ("a", ServiceConfig::enabled()),
        ("b", ServiceConfig::enabled()),
    ]);
    let res = bounded(sup.run(CancelToken::new(), &args(&["a", "b"]), &cfg)).await;
    assert_eq!(res.outcome, LifecycleOutcome::ConfigInvalid);
    assert_eq!(res.exit_code, 2);
    assert!(res.started.is_empty());
}

#[tokio::test]
async fn emits_the_log_counterpart_when_no_bus_is_wired() {
    let log = Arc::new(RecordingLogger::default());
    let r = registry_of(vec![Arc::new(Fake::new("api").with_addr("127.0.0.1:9"))]);
    let sup = Supervisor::new(
        r,
        SupervisorOptions {
            logger: Some(log.clone()),
            ..SupervisorOptions::default()
        },
    );
    let cancel = CancelToken::new();
    cancel.cancel();
    let _ = bounded(sup.run(
        cancel,
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;

    let info = log.at("info");
    let msgs: Vec<&String> = info.iter().map(|(m, _)| m).collect();
    for want in ["serve: started", "serve: ready_reported", "serve: stopped"] {
        assert!(msgs.iter().any(|m| *m == want), "missing {want}: {msgs:?}");
    }
    // The identifier and address are separable fields, not interpolated
    // into the message text.
    let ready = info
        .iter()
        .find(|(m, _)| m == "serve: ready_reported")
        .expect("ready line");
    assert!(ready.1.iter().any(|(k, v)| k == "service" && v == "api"));
    assert!(ready
        .1
        .iter()
        .any(|(k, v)| k == "address" && v == "127.0.0.1:9"));
    assert!(
        !ready.0.contains("api"),
        "the identifier must be a field, not message text: {:?}",
        ready.0
    );
}

#[tokio::test]
async fn logs_a_failure_at_error_level_not_info() {
    let log = Arc::new(RecordingLogger::default());
    let r = registry_of(vec![Arc::new(Fake::new("api").with_start(Box::new(
        |_c, _r| Box::pin(async { Err("boom".to_string()) }),
    )))]);
    let sup = Supervisor::new(
        r,
        SupervisorOptions {
            logger: Some(log.clone()),
            ..SupervisorOptions::default()
        },
    );
    let _ = bounded(sup.run(
        CancelToken::new(),
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;

    let errors = log.at("error");
    assert_eq!(errors.len(), 1, "exactly the one failed transition");
    assert_eq!(errors[0].0, "serve: failed");
    assert!(errors[0].1.iter().any(|(k, v)| k == "error" && v == "boom"));
    assert!(
        !log.at("info").iter().any(|(m, _)| m == "serve: failed"),
        "a failure must not be logged at info"
    );
}

#[tokio::test]
async fn survives_a_publisher_that_panics() {
    let pubr = Arc::new(RecordingPublisher::default());
    pubr.panics.store(1, Ordering::SeqCst);
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let sup = supervisor(r, pubr.clone());
    let cancel = CancelToken::new();
    cancel.cancel();
    // The deliberate panic below would otherwise print a scary
    // backtrace; the assertion, not the output, is what matters.
    let previous = std::panic::take_hook();
    std::panic::set_hook(Box::new(|_| {}));
    let res = bounded(sup.run(
        cancel,
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;
    std::panic::set_hook(previous);
    // The lifecycle completed despite the first publish blowing up.
    assert_eq!(res.outcome, LifecycleOutcome::CleanStop);
    assert_eq!(res.exit_code, 0);
    assert!(
        !pubr.events().is_empty(),
        "later events must still be published"
    );
}

#[tokio::test]
async fn runs_twice_on_one_registry_each_observing_only_its_own_cancellation() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let cfg = configs(&[("api", ServiceConfig::enabled())]);
    let sel = args(&["api"]);

    for _ in 0..2 {
        let pubr = Arc::new(RecordingPublisher::default());
        let sup = supervisor(r.clone(), pubr);
        let cancel = CancelToken::new();
        let c = cancel.clone();
        tokio::spawn(async move {
            tokio::time::sleep(Duration::from_millis(20)).await;
            c.cancel();
        });
        let res = tokio::time::timeout(Duration::from_secs(5), sup.run(cancel, &sel, &cfg))
            .await
            .expect("a second run must serve until its own cancellation");
        assert_eq!(res.outcome, LifecycleOutcome::CleanStop);
        assert_eq!(res.ready, ["api"]);
    }
}

#[tokio::test]
async fn returns_promptly_when_handed_an_already_cancelled_token() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cancel = CancelToken::new();
    cancel.cancel();
    let res = tokio::time::timeout(
        Duration::from_secs(5),
        sup.run(
            cancel,
            &args(&["api"]),
            &configs(&[("api", ServiceConfig::enabled())]),
        ),
    )
    .await
    .expect("an already-cancelled run must not hang");
    assert_eq!(res.outcome, LifecycleOutcome::CleanStop);
    assert_eq!(res.exit_code, 0);
}

#[tokio::test]
async fn emits_stopped_once_per_service_per_run() {
    let r = registry_of(vec![Arc::new(Fake::new("api"))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr.clone());
    let cancel = CancelToken::new();
    let c = cancel.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(20)).await;
        c.cancel();
    });
    let _ = bounded(sup.run(
        cancel,
        &args(&["api"]),
        &configs(&[("api", ServiceConfig::enabled())]),
    ))
    .await;
    assert_eq!(
        pubr.find("kit.serve.service.stopped").len(),
        1,
        "a service reports stopped once, whichever path noticed it first"
    );
    assert_eq!(pubr.find("kit.serve.supervisor.stopped").len(), 1);
}

// ---------------------------------------------------------------------------
// Cancellation ordering
// ---------------------------------------------------------------------------

#[tokio::test]
async fn cancels_once_before_stopping_anything() {
    // The contract fixes the order: "cancel once so every service
    // observes cancellation at the same instant, then stop in the exact
    // reverse of the order services actually started".
    //
    // Coverage note, recorded because it is easy to over-claim here:
    // moving the supervisor's own `run_token.cancel()` to *after* the
    // drain does not change anything observable, and no test in this
    // suite kills that mutation. On the signalled path the caller's
    // token has already cancelled the run token, so services observe
    // cancellation either way; on the fail-fast path `await_run`
    // cancels before returning. The line is kept because it makes the
    // invariant unconditional rather than dependent on which path ended
    // the run — not because a test can distinguish it.
    //
    // What this test does pin is the observable half: cancellation
    // reaches the services, and the drain runs after it.
    #[derive(Debug, PartialEq, Eq)]
    enum Ev {
        Cancelled(String),
        Stop(String),
    }
    let log: Arc<Mutex<Vec<Ev>>> = Arc::new(Mutex::new(Vec::new()));

    let mk = |name: &'static str, log: Arc<Mutex<Vec<Ev>>>| {
        let seen = log.clone();
        let stopped = log.clone();
        Fake::new(name)
            .with_start(Box::new(move |cancel, ready| {
                let seen = seen.clone();
                Box::pin(async move {
                    ready.report();
                    cancel.cancelled().await;
                    seen.lock().unwrap().push(Ev::Cancelled(name.to_string()));
                    Ok(())
                })
            }))
            .with_stop(Box::new(move |_c| {
                let stopped = stopped.clone();
                Box::pin(async move {
                    stopped.lock().unwrap().push(Ev::Stop(name.to_string()));
                    Ok(())
                })
            }))
    };

    let r = registry_of(vec![
        Arc::new(mk("a", log.clone())),
        Arc::new(mk("b", log.clone())),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cancel = CancelToken::new();
    let c = cancel.clone();
    tokio::spawn(async move {
        tokio::time::sleep(Duration::from_millis(20)).await;
        c.cancel();
    });
    let cfg = configs(&[
        ("a", ServiceConfig::enabled()),
        ("b", ServiceConfig::enabled()),
    ]);
    let _ = bounded(sup.run(cancel, &args(&["a", "b"]), &cfg)).await;

    let events = log.lock().unwrap();
    let first_stop = events.iter().position(|e| matches!(e, Ev::Stop(_)));
    let last_cancel = events.iter().rposition(|e| matches!(e, Ev::Cancelled(_)));
    let (Some(first_stop), Some(last_cancel)) = (first_stop, last_cancel) else {
        panic!("expected both cancellations and stops in {events:?}");
    };
    assert!(
        last_cancel < first_stop,
        "every service must observe cancellation before the drain begins; \
         got {events:?}"
    );
}

// ---------------------------------------------------------------------------
// Signals
// ---------------------------------------------------------------------------

#[test]
fn listens_for_exactly_sigint_and_sigterm() {
    use hop_top_kit::serve::SHUTDOWN_SIGNALS;
    assert_eq!(SHUTDOWN_SIGNALS, ["SIGINT", "SIGTERM"]);
}

/// Raising a real signal at the process would fight the test harness
/// and every sibling test, so the escalation *ladder* is exercised
/// against the tokens the controller hands out: the first signal fires
/// `shutdown`, the second fires `escalate`, and a supervisor holding
/// both reacts to each in turn.
#[tokio::test]
async fn the_first_signal_drains_and_a_second_escalates() {
    let shutdown = CancelToken::new();
    let escalate = CancelToken::new();
    let calls = Arc::new(Mutex::new(Vec::new()));
    let r = registry_of(vec![Arc::new(Fake::new("api").with_calls(calls.clone()))]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = Supervisor::new(
        r,
        SupervisorOptions {
            publisher: Some(pubr),
            escalate: Some(escalate.clone()),
            ..SupervisorOptions::default()
        },
    );
    let cfg = configs(&[("api", ServiceConfig::enabled())]);
    let sel = args(&["api"]);
    let mut running = Box::pin(sup.run(shutdown.clone(), &sel, &cfg));

    // Still serving before any signal.
    assert!(
        tokio::time::timeout(Duration::from_millis(60), &mut running)
            .await
            .is_err()
    );

    // Second signal lands with the first, so the drain is abandoned.
    escalate.cancel();
    shutdown.cancel();
    let res = tokio::time::timeout(Duration::from_secs(5), running)
        .await
        .expect("escalation must end the run");
    assert_eq!(res.exit_code, 1, "escalation exits with the crash code");
    assert!(
        !calls.lock().unwrap().iter().any(|c| c.starts_with("stop:")),
        "an escalated drain calls no stop"
    );
}

#[tokio::test]
async fn a_signal_controller_hands_out_two_independent_tokens() {
    let sig = hop_top_kit::serve::signal_controller().expect("install handler");
    assert!(!sig.shutdown.is_cancelled());
    assert!(!sig.escalate.is_cancelled());
    sig.stop();
    // Idempotent.
    sig.stop();
}

/// The readiness budget is measured from the moment a service was
/// started, not restarted each time some *other* service settles.
///
/// The earlier services report ready immediately (so the serial start
/// sequence advances to `slow`) and only *exit* later, while `slow` is
/// being awaited. Each of those exits wakes the readiness loop, so a
/// budget rebuilt per iteration would be extended three times over.
#[tokio::test]
async fn an_earlier_service_settling_does_not_extend_a_later_readiness_budget() {
    let brief = |name: &'static str, exit_after: u64| {
        Fake::new(name).with_start(Box::new(move |_c, ready| {
            Box::pin(async move {
                // Ready at once: the start sequence must reach `slow`.
                ready.report();
                // Exit later, landing in the channel mid-wait.
                tokio::time::sleep(Duration::from_millis(exit_after)).await;
                Ok(())
            })
        }))
    };
    let r = registry_of(vec![
        Arc::new(brief("brief-a", 30)),
        Arc::new(brief("brief-b", 55)),
        Arc::new(brief("brief-c", 80)),
        // Never reports ready: its budget must expire on schedule.
        Arc::new(Fake::new("slow").with_start(Box::new(|cancel, _ready| {
            Box::pin(async move {
                cancel.cancelled().await;
                Ok(())
            })
        }))),
    ]);
    let pubr = Arc::new(RecordingPublisher::default());
    let sup = supervisor(r, pubr);
    let cfg = configs(&[
        ("brief-a", ServiceConfig::enabled()),
        ("brief-b", ServiceConfig::enabled()),
        ("brief-c", ServiceConfig::enabled()),
        (
            "slow",
            ServiceConfig::enabled().with_ready_timeout(Duration::from_millis(100)),
        ),
    ]);

    let began = std::time::Instant::now();
    let res = bounded(sup.run(
        CancelToken::new(),
        &args(&["brief-a", "brief-b", "brief-c", "slow"]),
        &cfg,
    ))
    .await;
    let took = began.elapsed();

    assert_eq!(res.outcome, LifecycleOutcome::StartFailed);
    assert!(res.failed.contains_key("slow"));
    // One budget plus slack, not four. A timer rebuilt on each of the
    // three intervening exits would push this past 200ms.
    assert!(
        took < Duration::from_millis(180),
        "budget was extended by an earlier exit; run took {took:?}"
    );
}

// ---------------------------------------------------------------------------
// Duration grammar: the `30s`-style strings the contract's config keys
// and timeout flags carry
// ---------------------------------------------------------------------------

#[test]
fn parse_duration_accepts_every_unit() {
    use hop_top_kit::serve::parse_duration;
    let cases: &[(&str, Duration)] = &[
        ("7ns", Duration::from_nanos(7)),
        ("7us", Duration::from_micros(7)),
        ("7ms", Duration::from_millis(7)),
        ("7s", Duration::from_secs(7)),
        ("7m", Duration::from_secs(7 * 60)),
        ("7h", Duration::from_secs(7 * 3600)),
    ];
    for (input, want) in cases {
        assert_eq!(parse_duration(input).as_ref(), Ok(want), "{input}");
    }
}

#[test]
fn parse_duration_concatenates_groups_and_takes_decimals() {
    use hop_top_kit::serve::parse_duration;
    assert_eq!(parse_duration("1m30s"), Ok(Duration::from_secs(90)));
    assert_eq!(
        parse_duration("1h2m3s4ms"),
        Ok(Duration::from_millis(3_600_000 + 120_000 + 3_000 + 4))
    );
    assert_eq!(parse_duration("1.5s"), Ok(Duration::from_millis(1500)));
    assert_eq!(parse_duration("0.25m"), Ok(Duration::from_secs(15)));
    assert_eq!(parse_duration(".5s"), Ok(Duration::from_millis(500)));
    assert_eq!(parse_duration("0s"), Ok(Duration::ZERO));
}

#[test]
fn parse_duration_refuses_what_the_grammar_excludes_and_names_the_token() {
    use hop_top_kit::serve::parse_duration;
    let bare = parse_duration("30").unwrap_err();
    assert!(
        bare.contains("missing unit") && bare.contains("\"30\""),
        "{bare}"
    );

    let unknown = parse_duration("30d").unwrap_err();
    assert!(
        unknown.contains("unknown unit") && unknown.contains("\"d\""),
        "{unknown}"
    );

    let negative = parse_duration("-30s").unwrap_err();
    assert!(negative.contains("sign"), "{negative}");
    assert!(parse_duration("+30s").is_err());

    let empty = parse_duration("").unwrap_err();
    assert!(empty.contains("empty"), "{empty}");

    let unitfirst = parse_duration("s30").unwrap_err();
    assert!(unitfirst.contains("missing number"), "{unitfirst}");

    assert!(parse_duration(".s").is_err(), "a lone dot is not a number");
    assert!(parse_duration("1.2.3s").is_err());
    assert!(parse_duration(" 30s").is_err(), "no whitespace tolerance");
    assert!(parse_duration("99999999999999999999999999h").is_err());
}

#[test]
fn parse_duration_round_trips_the_three_defaults() {
    use hop_top_kit::serve::parse_duration;
    assert_eq!(parse_duration("30s"), Ok(DEFAULT_READY_TIMEOUT));
    assert_eq!(parse_duration("30s"), Ok(DEFAULT_STOP_TIMEOUT));
    assert_eq!(parse_duration("60s"), Ok(DEFAULT_SHUTDOWN_TIMEOUT));
    assert_eq!(parse_duration("1m"), Ok(DEFAULT_SHUTDOWN_TIMEOUT));
}
