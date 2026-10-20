package serve

import (
	"context"
	"os"
	"time"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/bus"
)

// SupervisorConfig is the supervisor-scoped half of the services
// block (serve-lifecycle.md §"Configuration surface"). The per-service half
// travels in [Request].Configs.
type SupervisorConfig struct {
	// FailurePolicy is services.failure_policy. Empty means
	// DefaultFailurePolicy (fail-fast).
	FailurePolicy FailurePolicy

	// ShutdownTimeout is services.shutdown_timeout, the total budget
	// across every service's Stop. Zero means
	// DefaultShutdownTimeout.
	ShutdownTimeout time.Duration
}

// Option configures a [Supervisor].
type Option func(*Supervisor)

// WithPublisher wires the bus the supervisor publishes lifecycle
// events to. Without one, only the log counterpart runs.
func WithPublisher(p Publisher) Option {
	return func(s *Supervisor) { s.pub = p }
}

// WithLogger wires the structured logger the supervisor writes the
// operator-legible startup and shutdown trace to.
func WithLogger(l Logger) Option {
	return func(s *Supervisor) { s.log = l }
}

// WithTopics overrides the lifecycle topic set. Defaults to
// DefaultTopics(DefaultTopicPrefix); adopters rebrand the prefix the
// same way every other kit emitter's prefix is rebranded.
func WithTopics(t bus.TopicMap) Option {
	return func(s *Supervisor) { s.topics = t }
}

// WithEventSource sets the bus Event.Source field. Defaults to
// DefaultTopicPrefix.
func WithEventSource(src string) Option {
	return func(s *Supervisor) { s.source = src }
}

// WithClock replaces the supervisor's notion of elapsed time. Tests
// use it to keep event payloads deterministic; production never sets
// it.
func WithClock(now func() time.Time) Option {
	return func(s *Supervisor) {
		if now != nil {
			s.now = now
		}
	}
}

// WithEscalation wires the second-signal channel from
// [SignalContext]. A receive on it during shutdown abandons the drain
// and ends the run with the crash code, so an operator can escalate
// without reaching for SIGKILL (serve-lifecycle.md §"Signals").
func WithEscalation(ch <-chan os.Signal) Option {
	return func(s *Supervisor) { s.escalate = ch }
}

// WithRunObserver hands fn the record of each run as the run begins,
// before any service starts. It is how a status surface outside the
// services — the control channel `service status` answers on — reads
// what the supervisor saw: fn keeps the [RunStatus] and reads it when
// asked, for as long as it likes. The record is live for the whole run
// and safe for concurrent use. fn runs on Run's goroutine and must not
// block; each Run hands it a record of its own.
func WithRunObserver(fn func(RunStatus)) Option {
	return func(s *Supervisor) { s.observe = fn }
}

// Supervisor runs a resolved set of services under one lifecycle:
// ordered start, per-service readiness, policy-driven reaction to
// failure, and ordered stop bounded by the configured budgets
// (serve-lifecycle.md §"Readiness", §"Shutdown", §"One service fails while
// others run").
//
// A Supervisor holds no global state and no package-level registry:
// everything it touches arrives through New and Run, so two of them
// can run concurrently in the same process — which is exactly what a
// test binary does.
type Supervisor struct {
	reg      *Registry
	cfg      SupervisorConfig
	pub      Publisher
	log      Logger
	topics   bus.TopicMap
	source   string
	now      func() time.Time
	escalate <-chan os.Signal
	observe  func(RunStatus)
}

// NewSupervisor returns a Supervisor over reg with cfg. Defaults are
// filled in for every zero field, so the zero SupervisorConfig is the
// documented default configuration rather than an unusable one.
func NewSupervisor(reg *Registry, cfg SupervisorConfig, opts ...Option) *Supervisor {
	if cfg.FailurePolicy == "" {
		cfg.FailurePolicy = DefaultFailurePolicy
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	s := &Supervisor{
		reg:    reg,
		cfg:    cfg,
		topics: DefaultTopics(DefaultTopicPrefix),
		source: DefaultTopicPrefix,
		now:    time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Result is what one supervised run produced.
type Result struct {
	// Outcome is the worst outcome observed across the whole run,
	// which is what the process exits on (serve-lifecycle.md §"Exit behavior").
	Outcome LifecycleOutcome

	// Err is the rendered failure carrying Code and ExitCode, nil on
	// a clean stop.
	Err *output.Error

	// Started is the identifiers of services whose Start was invoked,
	// in the order they were invoked. Stop runs in the exact reverse.
	Started []string

	// Ready is the identifiers that reported ready, in report order.
	Ready []string

	// Failed maps a service identifier to the error it returned. It
	// includes optional services, whose failures do not count toward
	// Outcome; Services says which ones those are.
	Failed map[string]error

	// Services is every started service's final record, in start
	// order: state, whether it was optional, and for a failed one the
	// reason and error text. It is the JSON-ready form of Started,
	// Ready and Failed together.
	Services []ServiceStatus
}

// ExitCode is the process exit code for this run.
func (r Result) ExitCode() int { return ExitCodeFor(r.Outcome) }

// Run starts every service in selected, waits for the run to end, and
// stops everything in reverse start order.
//
// The run ends when ctx is canceled (the clean path: a signal, or the
// caller's own shutdown), when a service failure trips the failure
// policy, or when every started service has returned. Run always
// performs the ordered stop before returning, so a caller never has to
// clean up after it.
//
// selected is normally Outcome.Selected from [Resolve]; Run does not
// re-resolve and does not consult enablement, because the decision the
// caller already made is the one the contract says to honor.
func (s *Supervisor) Run(ctx context.Context, selected []string, configs map[string]Config) Result {
	st := newRunState(s.now, selected, configs)
	if s.observe != nil {
		s.observe(st)
	}
	em := &emitter{
		topics: s.topics, pub: s.pub, log: s.log, source: s.source,
		optional: st.optional,
	}

	if len(selected) == 0 {
		return s.result(st, OutcomeNoServices, output.UsageError(
			"no services configured and enabled; enable one under services.* or name one explicitly",
		))
	}

	order := StartOrder(s.reg, selected)

	// The run context is the caller's, plus a cancel the supervisor
	// itself trips when the failure policy says to bring everything
	// down. Every service observes cancellation at the same instant;
	// nothing is queued behind another service's drain.
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	// Every Start sees the run's own record, so a service answering
	// for its dependencies reports what the supervisor saw.
	runCtx = ContextWithRunView(runCtx, st)

	sup := s.startAll(runCtx, order, configs, st, em)

	// A start failure short-circuits: services already running are
	// stopped in reverse order, and the outcome is start-failed.
	if sup.startErr != nil {
		return s.shutdown(ctx, cancelRun, sup, st, em)
	}
	// Every service failing to start, optional ones included, leaves
	// nothing serving: the run is down, not degraded.
	if st.allFailed() {
		st.record(OutcomeStartFailed)
		return s.shutdown(ctx, cancelRun, sup, st, em)
	}

	s.emitAggregateReady(runCtx, st, em)
	s.await(runCtx, sup, st, em, cancelRun)

	return s.shutdown(ctx, cancelRun, sup, st, em)
}

// noteFailure records a failure and emits its event. An optional
// service's failure is recorded and emitted like any other, but is not
// an outcome of the run.
func (s *Supervisor) noteFailure(
	ctx context.Context,
	name string,
	err error,
	outcome LifecycleOutcome,
	reason string,
	st *runState,
	em *emitter,
) {
	st.markFailed(name, err, reason)
	if !st.isOptional(name) {
		st.record(outcome)
	}
	em.emit(ctx, ObjectService, ActionFailed, EventPayload{
		Qualifiers: bus.Qualifiers{Reason: reason},
		Service:    name, Error: err.Error(), ElapsedMS: st.elapsedMS(),
	})
}

// finish assembles the Result from everything the run observed.
func (s *Supervisor) finish(st *runState, em *emitter) Result {
	started, ready, failed, observed := st.snapshot()
	worst := WorstOutcome(observed)
	res := Result{
		Outcome:  worst,
		Started:  started,
		Ready:    ready,
		Failed:   failed,
		Services: st.Services(),
	}
	if IsFailure(worst) {
		res.Err = failureError(worst, failed, st.optional)
	}
	em.emit(context.Background(), ObjectSupervisor, ActionStopped, EventPayload{
		Qualifiers: bus.Qualifiers{Reason: string(worst)},
		ElapsedMS:  st.elapsedMS(),
	})
	return res
}

// result is finish's short circuit for a run that never started.
func (s *Supervisor) result(st *runState, o LifecycleOutcome, err *output.Error) Result {
	started, ready, failed, _ := st.snapshot()
	return Result{Outcome: o, Err: err, Started: started, Ready: ready, Failed: failed}
}

// addrOf reads the optional [Addressed] declaration.
func addrOf(reg *Registry, name string) string {
	svc, ok := reg.Lookup(name)
	if !ok {
		return ""
	}
	a, ok := svc.(Addressed)
	if !ok {
		return ""
	}
	return a.Addr()
}
