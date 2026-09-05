//! The supervisor: ordered start, readiness, failure policy, and the
//! ordered shutdown.

use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::{Duration, Instant};

use crate::output::CliError;

use super::cancel::CancelToken;
use super::config::{
    failure_error, worst_outcome, FailurePolicy, LifecycleOutcome, ServiceConfigs,
    SupervisorConfig, DEFAULT_READY_TIMEOUT, DEFAULT_STOP_TIMEOUT,
};
use super::events::{
    default_topics, Emitter, EventPayload, Publisher, ServeLogger, ACTION_FAILED,
    ACTION_READY_REPORTED, ACTION_STARTED, ACTION_STOPPED, DEFAULT_TOPIC_PREFIX, OBJECT_SERVICE,
    OBJECT_SUPERVISOR,
};
use super::registry::{ReadySignal, ServiceRegistry};
use super::resolve::{no_services_error, start_order};

/// What one supervised run produced.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct RunResult {
    /// The worst outcome observed, which is what the process exits on.
    pub outcome: LifecycleOutcome,
    /// The rendered failure carrying code and exit code; absent on a
    /// clean stop.
    pub error: Option<CliError>,
    /// Identifiers whose start was invoked, in invocation order.
    pub started: Vec<String>,
    /// Identifiers that reported ready, in report order.
    pub ready: Vec<String>,
    /// Identifier to the error it failed with.
    pub failed: HashMap<String, String>,
    /// The process exit code for this run.
    pub exit_code: i32,
}

/// Options for [`Supervisor`].
#[derive(Default)]
pub struct SupervisorOptions {
    pub config: SupervisorConfig,
    pub publisher: Option<Arc<dyn Publisher>>,
    pub logger: Option<Arc<dyn ServeLogger>>,
    /// Overrides the topic map. Defaults to
    /// [`default_topics`]`(`[`DEFAULT_TOPIC_PREFIX`]`)`.
    pub topics: Option<std::collections::BTreeMap<String, String>>,
    /// The `source` field on published events.
    pub event_source: Option<String>,
    /// Aborts the drain. A second signal fires this, so an operator can
    /// escalate without reaching for SIGKILL.
    pub escalate: Option<CancelToken>,
}

/// Runs a resolved set of services under one lifecycle: ordered start,
/// per-service readiness, policy-driven reaction to failure, and
/// ordered stop bounded by the configured budgets.
///
/// A Supervisor holds no process-global state, so two can run
/// concurrently in one process — which is exactly what a test does.
pub struct Supervisor {
    registry: ServiceRegistry,
    failure_policy: FailurePolicy,
    shutdown_timeout: Duration,
    emitter: Emitter,
    escalate: Option<CancelToken>,
}

impl Supervisor {
    /// Builds a supervisor over `registry`.
    pub fn new(registry: ServiceRegistry, opts: SupervisorOptions) -> Self {
        let shutdown_timeout = if opts.config.shutdown_timeout.is_zero() {
            SupervisorConfig::default().shutdown_timeout
        } else {
            opts.config.shutdown_timeout
        };
        let emitter = Emitter::new(
            opts.topics
                .unwrap_or_else(|| default_topics(DEFAULT_TOPIC_PREFIX)),
            opts.event_source
                .unwrap_or_else(|| DEFAULT_TOPIC_PREFIX.to_string()),
            opts.publisher,
            opts.logger,
        );
        Supervisor {
            registry,
            failure_policy: opts.config.failure_policy,
            shutdown_timeout,
            emitter,
            escalate: opts.escalate,
        }
    }

    /// Starts every service in `selected`, stays up serving until the
    /// run ends, and stops everything in reverse start order.
    ///
    /// The run ends when `cancel` fires (the clean path: a signal, or
    /// the caller's own shutdown), when a failure trips the failure
    /// policy, or when every started service has returned. `run`
    /// always performs the ordered stop before returning, so a caller
    /// never has to clean up after it.
    ///
    /// `selected` is normally [`super::ResolveOutcome::selected`]; run
    /// does not re-resolve and does not consult enablement, because the
    /// decision the caller already made is the one to honor.
    pub async fn run(
        &self,
        cancel: CancelToken,
        selected: &[String],
        configs: &ServiceConfigs,
    ) -> RunResult {
        let mut st = RunState::new();

        if selected.is_empty() {
            return self.finish(&st, LifecycleOutcome::NoServices, Some(no_services_error()));
        }

        let order = match start_order(&self.registry, selected) {
            Ok(o) => o,
            Err(e) => {
                let mut err = CliError::usage(e.0);
                err.exit_code = LifecycleOutcome::ConfigInvalid.exit_code();
                return self.finish(&st, LifecycleOutcome::ConfigInvalid, Some(err));
            }
        };

        // The run token is the caller's cancel plus a cancel the
        // supervisor itself trips when the failure policy says to bring
        // everything down. Cancelled once, so every service observes
        // cancellation at the same instant; nothing is queued behind
        // another service's drain.
        let run_token = CancelToken::new();
        {
            let outer = cancel.clone();
            let inner = run_token.clone();
            tokio::spawn(async move {
                outer.cancelled().await;
                inner.cancel();
            });
        }

        let start_failed = self.start_all(&run_token, &order, configs, &mut st).await;
        if !start_failed {
            self.emit_aggregate_ready(&st);
            // This await is the whole point of `serve`: the supervisor
            // stays up here until something ends the run. A naive
            // implementation that skipped it would return Ok
            // immediately and exit 0 without ever having served.
            self.await_run(&run_token, &mut st).await;
        }
        run_token.cancel();
        self.stop_all(&mut st, configs).await;
        let outcome = worst_outcome(&st.observed);
        self.finish(&st, outcome, None)
    }

    /// Starts each service in order, waiting for each to report ready
    /// (or fail, or exhaust its budget) before starting the next.
    ///
    /// Serial start is what makes `depends_on` mean anything: a
    /// dependent must not begin acquiring before its dependency is
    /// accepting work. Returns true when a start failure
    /// short-circuits the sequence.
    async fn start_all(
        &self,
        run_token: &CancelToken,
        order: &[String],
        configs: &ServiceConfigs,
        st: &mut RunState,
    ) -> bool {
        for name in order {
            let Some(svc) = self.registry.lookup(name) else {
                let msg = format!("service {name:?} disappeared from the registry");
                st.note_failure(name, &msg, LifecycleOutcome::StartFailed);
                self.emit_failed(st, name, &msg, "unregistered");
                return true;
            };

            let ready_token = CancelToken::new();
            let ready = ReadySignal::new(ready_token.clone());
            st.started.push(name.clone());
            self.emitter.emit(
                OBJECT_SERVICE,
                ACTION_STARTED,
                &EventPayload::for_service(name, st.elapsed_ms()),
            );

            let svc_for_task = svc.clone();
            let token = run_token.clone();
            let tx = st.exits.0.clone();
            let reporting = name.clone();
            tokio::spawn(async move {
                let outcome = svc_for_task.start(token, ready).await;
                // The receiver is dropped only after the run has ended,
                // so a send failure means nobody is listening any more.
                let _ = tx.send((reporting, outcome)).await;
            });
            st.live.insert(name.clone());

            if self.await_ready(name, &ready_token, configs, st).await {
                return true;
            }
        }
        false
    }

    /// Blocks until `name` reports ready, fails, or exhausts its
    /// readiness budget. A service that has not reported ready within
    /// the budget is a start failure.
    async fn await_ready(
        &self,
        name: &str,
        ready_token: &CancelToken,
        configs: &ServiceConfigs,
        st: &mut RunState,
    ) -> bool {
        let budget = configs
            .get(name)
            .map(|c| c.ready_timeout)
            .unwrap_or(DEFAULT_READY_TIMEOUT);

        enum Won {
            Ready,
            Exited(Result<(), String>),
            Timeout,
        }

        // One deadline for the whole wait, not a timer per iteration:
        // the budget is "ready within ready_timeout of being started",
        // so an earlier service settling mid-wait must not silently
        // extend it.
        let deadline = tokio::time::sleep(budget);
        tokio::pin!(deadline);

        // Only the service being started can be waited on here: an
        // earlier service exiting mid-start is left in the channel for
        // `await_run` to pick up rather than being mistaken for this
        // one's outcome.
        let won = loop {
            let settled = tokio::select! {
                biased;
                () = ready_token.cancelled() => break Won::Ready,
                got = st.exits.1.recv() => got,
                () = &mut deadline => break Won::Timeout,
            };
            match settled {
                Some((who, outcome)) if who == name => break Won::Exited(outcome),
                Some((who, outcome)) => st.pending.push((who, outcome)),
                // Every sender is held by a live task or by `st.exits.0`,
                // which the run holds for its whole duration, so the
                // channel cannot close under us.
                None => break Won::Timeout,
            }
        };

        match won {
            Won::Ready => {
                st.ready.push(name.to_string());
                let mut payload = EventPayload::for_service(name, st.elapsed_ms());
                payload.address = self.addr_of(name);
                self.emitter
                    .emit(OBJECT_SERVICE, ACTION_READY_REPORTED, &payload);
                false
            }
            Won::Timeout => {
                let msg = format!("not ready within {budget:?}");
                st.note_failure(name, &msg, LifecycleOutcome::StartFailed);
                self.emit_failed(st, name, &msg, "ready_timeout");
                true
            }
            // The service returned before reporting ready. That is a
            // start failure even when it returned cleanly: it was asked
            // to serve and it did not.
            Won::Exited(exit) => {
                st.live.remove(name);
                let msg = exit
                    .err()
                    .unwrap_or_else(|| "returned before reporting ready".to_string());
                st.note_failure(name, &msg, LifecycleOutcome::StartFailed);
                self.emit_failed(st, name, &msg, "start");
                true
            }
        }
    }

    /// Publishes the supervisor-scoped readiness event once every
    /// started service is ready.
    fn emit_aggregate_ready(&self, st: &RunState) {
        if !st.started.is_empty() && st.ready.len() == st.started.len() {
            self.emitter.emit(
                OBJECT_SUPERVISOR,
                ACTION_READY_REPORTED,
                &EventPayload {
                    elapsed_ms: st.elapsed_ms(),
                    ..EventPayload::default()
                },
            );
        }
    }

    /// Blocks while the services run. Returns when the run token
    /// fires, when the failure policy trips, or when the last running
    /// service has exited.
    async fn await_run(&self, run_token: &CancelToken, st: &mut RunState) {
        while !st.live.is_empty() {
            // Anything that settled while an earlier service was still
            // being awaited for readiness is drained first.
            let settled = match st.pending.pop() {
                Some(early) => Some(early),
                None => tokio::select! {
                    biased;
                    () = run_token.cancelled() => None,
                    got = st.exits.1.recv() => got,
                },
            };

            let Some((name, exit)) = settled else {
                return;
            };
            if !st.live.remove(&name) {
                continue;
            }

            if let Err(msg) = exit {
                st.note_failure(&name, &msg, LifecycleOutcome::RuntimeCrash);
                self.emit_failed(st, &name, &msg, "runtime");
                if self.failure_policy == FailurePolicy::FailFast {
                    run_token.cancel();
                    return;
                }
                continue;
            }
            // A clean return under isolate is not a failure of that
            // service, but the process must not survive as an empty
            // shell: when the last one is gone the run is over.
            if !st.mark_stopped(&name) {
                self.emitter.emit(
                    OBJECT_SERVICE,
                    ACTION_STOPPED,
                    &EventPayload::for_service(&name, st.elapsed_ms()),
                );
            }
        }

        if !st.failed.is_empty() && self.failure_policy == FailurePolicy::Isolate {
            st.observed.push(LifecycleOutcome::RuntimeCrash);
        }
    }

    /// Invokes stop in the exact reverse of the order services actually
    /// started, one at a time, so a dependent is always fully stopped
    /// before its dependency.
    ///
    /// Each stop is bounded by that service's budget. One that exceeds
    /// it is abandoned — logged, emitted as failed, and the supervisor
    /// proceeds to the next rather than blocking the whole shutdown on
    /// one straggler. Exceeding the total budget ends the sequence with
    /// `shutdown-timeout`.
    async fn stop_all(&self, st: &mut RunState, configs: &ServiceConfigs) {
        let order = st.started.clone();
        let deadline = Instant::now() + self.shutdown_timeout;

        for i in (0..order.len()).rev() {
            let name = &order[i];

            // A second signal aborts the drain: the remaining services
            // are abandoned and the run exits with the crash code.
            if self.escalate.as_ref().is_some_and(|e| e.is_cancelled()) {
                let msg = "drain aborted by second signal".to_string();
                st.observed.push(LifecycleOutcome::RuntimeCrash);
                for abandoned in order[..=i].iter() {
                    st.failed.insert(abandoned.clone(), msg.clone());
                    self.emit_failed(st, abandoned, &msg, "escalated");
                }
                return;
            }

            let now = Instant::now();
            if now >= deadline {
                st.observed.push(LifecycleOutcome::ShutdownTimeout);
                let msg = format!(
                    "shutdown budget {:?} exhausted before stopping",
                    self.shutdown_timeout
                );
                self.emit_failed(st, name, &msg, "shutdown_timeout");
                continue;
            }

            let Some(svc) = self.registry.lookup(name) else {
                continue;
            };
            let budget = configs
                .get(name)
                .map(|c| c.stop_timeout)
                .unwrap_or(DEFAULT_STOP_TIMEOUT)
                .min(deadline - now);

            let stop_token = CancelToken::new();
            let outcome = tokio::select! {
                biased;
                r = svc.stop(stop_token.clone()) => Some(r),
                () = tokio::time::sleep(budget) => None,
            };

            match outcome {
                None => {
                    // Abandoned, not awaited: the stop future is
                    // dropped so one straggler cannot hold the whole
                    // shutdown. Cancelling first gives a cooperative
                    // implementation the chance to notice.
                    stop_token.cancel();
                    let over_total = Instant::now() >= deadline;
                    let msg = format!("stop exceeded {budget:?}");
                    st.note_failure(
                        name,
                        &msg,
                        if over_total {
                            LifecycleOutcome::ShutdownTimeout
                        } else {
                            LifecycleOutcome::RuntimeCrash
                        },
                    );
                    self.emit_failed(
                        st,
                        name,
                        &msg,
                        if over_total {
                            "shutdown_timeout"
                        } else {
                            "stop_timeout"
                        },
                    );
                }
                Some(Err(msg)) => {
                    st.note_failure(name, &msg, LifecycleOutcome::RuntimeCrash);
                    self.emit_failed(st, name, &msg, "stop");
                }
                Some(Ok(())) => {
                    // A service that returned on its own already
                    // reported stopped when it did; stop released its
                    // resources, and the event is not repeated — one
                    // stopped per service per run.
                    if !st.mark_stopped(name) {
                        self.emitter.emit(
                            OBJECT_SERVICE,
                            ACTION_STOPPED,
                            &EventPayload::for_service(name, st.elapsed_ms()),
                        );
                    }
                }
            }
        }
    }

    fn addr_of(&self, name: &str) -> Option<String> {
        self.registry
            .lookup(name)
            .and_then(|s| s.addr())
            .filter(|a| !a.is_empty())
    }

    fn emit_failed(&self, st: &RunState, name: &str, error: &str, reason: &str) {
        self.emitter.emit(
            OBJECT_SERVICE,
            ACTION_FAILED,
            &EventPayload {
                service: Some(name.to_string()),
                error: Some(error.to_string()),
                reason: Some(reason.to_string()),
                elapsed_ms: st.elapsed_ms(),
                ..EventPayload::default()
            },
        );
    }

    /// Assembles the result from everything the run observed.
    fn finish(&self, st: &RunState, outcome: LifecycleOutcome, err: Option<CliError>) -> RunResult {
        let error = err.or_else(|| {
            if outcome.is_failure() {
                Some(failure_error(outcome, &st.failed))
            } else {
                None
            }
        });
        self.emitter.emit(
            OBJECT_SUPERVISOR,
            ACTION_STOPPED,
            &EventPayload {
                reason: Some(outcome.as_str().to_string()),
                elapsed_ms: st.elapsed_ms(),
                ..EventPayload::default()
            },
        );
        let exit_code = error.as_ref().map_or(0, |e| e.exit_code);
        RunResult {
            outcome,
            error,
            started: st.started.clone(),
            ready: st.ready.clone(),
            failed: st.failed.clone(),
            exit_code,
        }
    }
}

/// One service's start settling: who it was and how it ended.
type ServiceExit = (String, Result<(), String>);
type ExitSender = tokio::sync::mpsc::Sender<ServiceExit>;
type ExitReceiver = tokio::sync::mpsc::Receiver<ServiceExit>;

/// The mutable half of one run, kept off the Supervisor so two runs can
/// share one registry without sharing state.
struct RunState {
    started: Vec<String>,
    ready: Vec<String>,
    failed: HashMap<String, String>,
    observed: Vec<LifecycleOutcome>,
    /// Identifiers whose start task is still running.
    live: HashSet<String>,
    /// Every service reports its exit down one channel rather than the
    /// supervisor racing a vector of JoinHandles. `tokio::select!` is
    /// not variadic and this crate carries no `futures` combinator
    /// dependency, so a single rendezvous point is both simpler and
    /// cheaper than hand-rolling `select_all`.
    exits: (ExitSender, ExitReceiver),
    /// Exits that arrived while a *different* service was being awaited
    /// for readiness, held for `await_run` to process in order.
    pending: Vec<ServiceExit>,
    stopped: HashSet<String>,
    begin: Instant,
}

impl RunState {
    fn new() -> Self {
        // Buffered by more than any plausible service count so a
        // service reporting its exit never blocks on a supervisor that
        // is busy elsewhere.
        let (tx, rx) = tokio::sync::mpsc::channel(64);
        RunState {
            started: Vec::new(),
            ready: Vec::new(),
            failed: HashMap::new(),
            observed: Vec::new(),
            live: HashSet::new(),
            exits: (tx, rx),
            pending: Vec::new(),
            stopped: HashSet::new(),
            begin: Instant::now(),
        }
    }

    fn elapsed_ms(&self) -> u128 {
        self.begin.elapsed().as_millis()
    }

    fn note_failure(&mut self, name: &str, error: &str, outcome: LifecycleOutcome) {
        self.failed.insert(name.to_string(), error.to_string());
        self.observed.push(outcome);
    }

    /// Records that `name`'s stopped event has been emitted and reports
    /// whether it had been already. A service reports stopped once per
    /// run, whichever path noticed it first.
    fn mark_stopped(&mut self, name: &str) -> bool {
        !self.stopped.insert(name.to_string())
    }
}
