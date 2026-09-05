//! The hierarchy, the override rule, and dependency ordering.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use crate::output::CliError;

use super::config::{LifecycleOutcome, ServiceConfigs};
use super::registry::{PolicyGate, Service, ServiceRegistrationError, ServiceRegistry};

/// A parsed `serve` invocation.
#[derive(Default)]
pub struct ResolveRequest<'a> {
    /// Positional arguments after the `serve` word. Empty is the
    /// supervisor form, exactly one the selector form, two or more a
    /// usage error.
    pub args: Vec<String>,
    /// Resolved `services.<name>` blocks. A service with no entry is
    /// not configured, and the supervisor form skips it.
    pub configs: Option<&'a ServiceConfigs>,
    /// The third validation gate. Omitted passes everything.
    pub policy: Option<&'a dyn PolicyGate>,
}

/// The result of resolving a request against a registry.
#[derive(Debug, Default, PartialEq, Eq)]
pub struct ResolveOutcome {
    /// Identifiers to run, in registration order. Empty when `error` is
    /// set.
    pub selected: Vec<String>,
    /// True when the selector form was used. Under it `selected` holds
    /// exactly one name and aggregate enablement was overridden rather
    /// than consulted.
    pub explicit: bool,
    /// Configured-but-disabled services the supervisor form passed
    /// over. Skipping is not an error and must not affect the exit
    /// code.
    pub skipped: Vec<String>,
    /// The refusal, already carrying its code and exit code.
    pub error: Option<CliError>,
    /// The outcome the refusal corresponds to.
    pub outcome: Option<LifecycleOutcome>,
}

/// Turns a `serve` invocation into a runnable set, applying the
/// hierarchy and the override rule. Pure: nothing is started, nothing
/// binds, nothing is written.
///
/// Selector form runs the named service **even when
/// `services.<name>.enabled` is false**, provided all three gates pass
/// in order — registration, then configuration, then policy.
/// Enablement is not a gate there: an operator naming a service on the
/// command line has already made the decision the flag exists to
/// automate.
///
/// Supervisor form runs every service that is both configured and
/// enabled, in registration order, skipping a disabled one silently.
/// Resolving to zero services is a usage error, not a clean exit: a
/// process that exits 0 without listening is indistinguishable from a
/// successful start to systemd or a container runtime.
pub fn resolve(registry: &ServiceRegistry, req: &ResolveRequest<'_>) -> ResolveOutcome {
    if req.args.len() > 1 {
        return ResolveOutcome {
            outcome: Some(LifecycleOutcome::InvalidSelection),
            error: Some(CliError::usage(format!(
                "serve accepts at most one service name, got {}",
                req.args.len()
            ))),
            ..ResolveOutcome::default()
        };
    }
    if req.args.len() == 1 {
        return resolve_explicit(registry, req, &req.args[0]);
    }
    resolve_aggregate(registry, req)
}

/// The selector form and its override rule.
fn resolve_explicit(
    registry: &ServiceRegistry,
    req: &ResolveRequest<'_>,
    name: &str,
) -> ResolveOutcome {
    let base = ResolveOutcome {
        explicit: true,
        ..ResolveOutcome::default()
    };

    // Gate 1: registration.
    let Some(svc) = registry.lookup(name) else {
        let known = registry.names();
        let mut err = CliError::not_found(format!(
            "unknown service {name:?}; known: {}",
            known.join(", ")
        ));
        if let Some(fix) = nearest_name(name, &known) {
            err.suggested_fix = format!("did you mean {fix:?}?");
        }
        return ResolveOutcome {
            outcome: Some(LifecycleOutcome::UnknownService),
            error: Some(err),
            ..base
        };
    };

    // Gate 2: configuration.
    if let Some(invalid) = svc.validate() {
        return ResolveOutcome {
            outcome: Some(LifecycleOutcome::ConfigInvalid),
            error: Some(CliError::usage(format!("service {name:?}: {invalid}"))),
            ..base
        };
    }

    // Gate 3: policy.
    if let Some(denied) = check_policy(req.policy, svc.as_ref()) {
        return ResolveOutcome {
            outcome: Some(LifecycleOutcome::PolicyDenied),
            error: Some(denied),
            ..base
        };
    }

    // Enablement is deliberately not consulted here.
    ResolveOutcome {
        selected: vec![name.to_string()],
        explicit: true,
        ..ResolveOutcome::default()
    }
}

/// The supervisor form.
fn resolve_aggregate(registry: &ServiceRegistry, req: &ResolveRequest<'_>) -> ResolveOutcome {
    let empty = ServiceConfigs::new();
    let configs = req.configs.unwrap_or(&empty);
    let mut out = ResolveOutcome::default();

    for name in registry.names() {
        let Some(cfg) = configs.get(&name) else {
            continue; // not configured
        };
        if !cfg.enabled {
            out.skipped.push(name);
            continue;
        }
        let Some(svc) = registry.lookup(&name) else {
            continue;
        };
        if let Some(invalid) = svc.validate() {
            return ResolveOutcome {
                selected: Vec::new(),
                explicit: false,
                skipped: out.skipped,
                outcome: Some(LifecycleOutcome::ConfigInvalid),
                error: Some(CliError::usage(format!("service {name:?}: {invalid}"))),
            };
        }
        if let Some(denied) = check_policy(req.policy, svc.as_ref()) {
            return ResolveOutcome {
                selected: Vec::new(),
                explicit: false,
                skipped: out.skipped,
                outcome: Some(LifecycleOutcome::PolicyDenied),
                error: Some(denied),
            };
        }
        out.selected.push(name);
    }

    if out.selected.is_empty() {
        out.error = Some(no_services_error());
        out.outcome = Some(LifecycleOutcome::NoServices);
    }
    out
}

/// The refusal a supervisor invocation resolving to zero services
/// carries. USAGE/2, never a clean 0.
pub(crate) fn no_services_error() -> CliError {
    let mut err = CliError::usage(
        "no services configured and enabled; enable one under services.* \
         or name one explicitly",
    );
    err.suggested_fix = "set services.<name>.enabled: true, or run: serve <service>".to_string();
    err
}

fn check_policy(gate: Option<&dyn PolicyGate>, svc: &dyn Service) -> Option<CliError> {
    let gate = gate?;
    let (side_effect, network) = svc.class()?;
    let verdict = gate.allow(&side_effect, &network);
    if verdict.ok {
        return None;
    }
    let mut msg = format!(
        "service {:?} denied by policy (side_effect={side_effect}, network={network})",
        svc.name()
    );
    if let Some(reason) = verdict.reason {
        msg.push_str(": ");
        msg.push_str(&reason);
    }
    Some(CliError::unauthorized(msg))
}

/// The registered name closest to `want` by edit distance, or `None`
/// when nothing is close enough to suggest.
///
/// The contract lists nearest-name suggestion under "explicitly not
/// required" — it is a courtesy on top of the required `NOT_FOUND`
/// refusal, kept here because it costs one function and matches the
/// reference ports.
fn nearest_name(want: &str, known: &[String]) -> Option<String> {
    let limit = want.chars().count() / 2 + 1;
    let mut sorted: Vec<&String> = known.iter().collect();
    sorted.sort();
    let mut best: Option<(&String, usize)> = None;
    for k in sorted {
        let d = edit_distance(want, k);
        if d > limit {
            continue;
        }
        if best.is_none_or(|(_, bd)| d < bd) {
            best = Some((k, d));
        }
    }
    best.map(|(k, _)| k.clone())
}

fn edit_distance(a: &str, b: &str) -> usize {
    let a: Vec<char> = a.chars().collect();
    let b: Vec<char> = b.chars().collect();
    let mut prev: Vec<usize> = (0..=b.len()).collect();
    let mut cur = vec![0usize; b.len() + 1];
    for i in 1..=a.len() {
        cur[0] = i;
        for j in 1..=b.len() {
            let cost = usize::from(a[i - 1] != b[j - 1]);
            cur[j] = (prev[j] + 1).min(cur[j - 1] + 1).min(prev[j - 1] + cost);
        }
        std::mem::swap(&mut prev, &mut cur);
    }
    prev[b.len()]
}

/// `selected` in topological order over the optional `depends_on`
/// declarations, ties broken by the order in `selected` (which
/// [`resolve`] returns in registration order).
///
/// A dependency naming a service outside `selected` is ignored rather
/// than an error: under the selector form exactly one service runs, and
/// its dependencies are the operator's business, not a reason to refuse
/// a deliberate single-service start.
///
/// A dependency cycle is refused in the same class as a name collision:
/// it is a wiring bug that can only be fixed by editing the
/// registrations, and there is no order the supervisor could pick that
/// would be right.
pub fn start_order(
    registry: &ServiceRegistry,
    selected: &[String],
) -> Result<Vec<String>, ServiceRegistrationError> {
    let in_set: HashSet<&String> = selected.iter().collect();
    let mut deps: HashMap<String, Vec<String>> = HashMap::new();
    for name in selected {
        let Some(svc) = registry.lookup(name) else {
            continue;
        };
        let want: Vec<String> = svc
            .depends_on()
            .into_iter()
            .filter(|d| in_set.contains(d) && d != name)
            .collect();
        if !want.is_empty() {
            deps.insert(name.clone(), want);
        }
    }

    #[derive(Clone, Copy, PartialEq)]
    enum Mark {
        Grey,
        Black,
    }
    let mut mark: HashMap<String, Mark> = HashMap::new();
    let mut out: Vec<String> = Vec::new();
    for name in selected {
        visit(name, &deps, &mut mark, &mut out, &mut Vec::new())?;
    }
    return Ok(out);

    fn visit(
        name: &str,
        deps: &HashMap<String, Vec<String>>,
        mark: &mut HashMap<String, Mark>,
        out: &mut Vec<String>,
        path: &mut Vec<String>,
    ) -> Result<(), ServiceRegistrationError> {
        match mark.get(name) {
            Some(Mark::Black) => return Ok(()),
            Some(Mark::Grey) => {
                path.push(name.to_string());
                return Err(ServiceRegistrationError(format!(
                    "serve: dependency cycle: {}",
                    path.join(" -> ")
                )));
            }
            None => {}
        }
        mark.insert(name.to_string(), Mark::Grey);
        path.push(name.to_string());
        if let Some(want) = deps.get(name) {
            for w in want {
                visit(w, deps, mark, out, path)?;
            }
        }
        path.pop();
        mark.insert(name.to_string(), Mark::Black);
        out.push(name.to_string());
        Ok(())
    }
}

/// One row of the `--list` inspection form.
///
/// The *columns* are not contract — a port renders them through its own
/// output layer — but the registration ordering is, so this returns
/// rows rather than rendered text and leaves formatting to the caller.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ServiceListing {
    pub name: String,
    pub configured: bool,
    pub enabled: bool,
    pub ready: bool,
}

/// Every registered service with its configured, enabled and ready
/// state, in registration order so the listing mirrors the adopter's
/// wiring.
pub fn list_services(
    registry: &ServiceRegistry,
    configs: Option<&ServiceConfigs>,
) -> Vec<ServiceListing> {
    let empty = ServiceConfigs::new();
    let configs = configs.unwrap_or(&empty);
    registry
        .list()
        .iter()
        .map(|svc: &Arc<dyn Service>| {
            let cfg = configs.get(svc.name());
            ServiceListing {
                name: svc.name().to_string(),
                configured: cfg.is_some(),
                enabled: cfg.is_some_and(|c| c.enabled),
                ready: svc.ready(),
            }
        })
        .collect()
}
